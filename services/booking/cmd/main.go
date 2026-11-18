package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/internal/platform/config"
	"github.com/r-mcdougall/gotravel/internal/platform/database"
	"github.com/r-mcdougall/gotravel/internal/platform/health"
	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
	"github.com/r-mcdougall/gotravel/internal/platform/logging"
	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/internal/platform/metrics"
	"github.com/r-mcdougall/gotravel/internal/platform/outbox"
	"github.com/r-mcdougall/gotravel/services/booking/internal/booking"
	"github.com/r-mcdougall/gotravel/services/booking/migrations"
)

const (
	serviceName    = "booking"
	eventsExchange = "booking.events"
	defaultHoldTTL = 15 * time.Minute
)

var flightChanged = messaging.Subscription{
	Exchange:   "catalog.events",
	RoutingKey: "flight.changed",
	Queue:      "booking.flight-changed",
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", serviceName, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet(serviceName, flag.ContinueOnError)
	healthCheck := fs.Bool("healthcheck", false, "probe /healthz and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}

	var loader config.Loader
	base := config.LoadBase(&loader)
	databaseURL := loader.Required("DATABASE_URL")
	rabbitmqURL := loader.Required("RABBITMQ_URL")
	publicKeyFile := loader.Required("JWT_PUBLIC_KEY_FILE")
	holdTTL := loader.Duration("HOLD_TTL", defaultHoldTTL)

	if *healthCheck {
		return health.Probe(ctx, base.HTTPAddr)
	}
	if err := loader.Err(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if holdTTL <= 0 {
		return fmt.Errorf("load config: HOLD_TTL must be positive, got %s", holdTTL)
	}

	log, err := logging.New(stdout, serviceName, base.LogLevel)
	if err != nil {
		return err
	}

	publicKey, err := authn.LoadPublicKey(publicKeyFile)
	if err != nil {
		return fmt.Errorf("load jwt public key: %w", err)
	}

	appMetrics := metrics.New()

	if err = database.Migrate(ctx, log, databaseURL, migrations.FS); err != nil {
		return err
	}

	pool, err := database.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	pgStore := booking.NewPgStore(pool)

	publisher := messaging.NewPublisher(rabbitmqURL, eventsExchange)
	defer func() { _ = publisher.Close() }()
	relay := outbox.NewRelay(pool, publisher, log.With(slog.String("component", "outbox_relay")))
	defer runInBackground(ctx, relay.Run)()

	events := booking.NewEventHandler(pgStore)
	defer runInBackground(ctx, func(ctx context.Context) {
		messaging.Consume(ctx, rabbitmqURL, flightChanged, events.HandleFlightChanged,
			log.With(slog.String("component", "flight_changed_consumer")))
	})()

	expiredHolds := prometheus.NewCounter(prometheus.CounterOpts{
		Name: "booking_holds_expired_total",
		Help: "Holds that expired before they were paid or cancelled.",
	})
	appMetrics.Registry().MustRegister(expiredHolds)
	expirer := booking.NewExpirer(pgStore, expiredHolds, log.With(slog.String("component", "hold_expirer")))
	defer runInBackground(ctx, expirer.Run)()

	service := booking.NewService(pgStore, holdTTL)
	handler := booking.NewHandler(service, log, authn.NewVerifier(publicKey))

	router := httpserver.NewRouter(log, appMetrics, handler.Routes)
	return httpserver.Run(ctx, log, httpserver.New(base.HTTPAddr, router), base.ShutdownTimeout)
}

func runInBackground(ctx context.Context, fn func(context.Context)) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(ctx)
	}()

	return func() {
		cancel()
		<-done
	}
}
