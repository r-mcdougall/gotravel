package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // airport time zones must resolve in the distroless image, which has no zoneinfo

	"github.com/prometheus/client_golang/prometheus"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/internal/platform/cache"
	"github.com/r-mcdougall/gotravel/internal/platform/config"
	"github.com/r-mcdougall/gotravel/internal/platform/database"
	"github.com/r-mcdougall/gotravel/internal/platform/health"
	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
	"github.com/r-mcdougall/gotravel/internal/platform/logging"
	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/internal/platform/metrics"
	"github.com/r-mcdougall/gotravel/internal/platform/outbox"
	"github.com/r-mcdougall/gotravel/services/catalog/internal/catalog"
	"github.com/r-mcdougall/gotravel/services/catalog/migrations"
)

const (
	serviceName           = "catalog"
	eventsExchange        = "catalog.events"
	defaultSearchCacheTTL = 60 * time.Second
)

var availabilityChanged = messaging.Subscription{
	Exchange:   "booking.events",
	RoutingKey: "flight.availability_changed",
	Queue:      "catalog.flight-availability-changed",
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
	publicKeyFile := loader.Required("JWT_PUBLIC_KEY_FILE")
	rabbitmqURL := loader.Required("RABBITMQ_URL")
	bookingURL := loader.Required("BOOKING_URL")
	redisURL := loader.String("REDIS_URL", "")
	searchCacheTTL := loader.Duration("SEARCH_CACHE_TTL", defaultSearchCacheTTL)

	if *healthCheck {
		return health.Probe(ctx, base.HTTPAddr)
	}
	if err := loader.Err(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if searchCacheTTL <= 0 {
		return fmt.Errorf("load config: SEARCH_CACHE_TTL must be positive, got %s", searchCacheTTL)
	}
	if u, err := url.Parse(bookingURL); err != nil || u.Host == "" {
		return fmt.Errorf("load config: BOOKING_URL must be an absolute URL, got %q", bookingURL)
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
	routeCache, closeCache, err := newSearchCache(ctx, log, redisURL, searchCacheTTL, appMetrics.Registry())
	if err != nil {
		return err
	}
	defer closeCache()

	if err = database.Migrate(ctx, log, databaseURL, migrations.FS); err != nil {
		return err
	}

	pool, err := database.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	publisher := messaging.NewPublisher(rabbitmqURL, eventsExchange)
	defer func() { _ = publisher.Close() }()
	relay := outbox.NewRelay(pool, publisher, log.With(slog.String("component", "outbox_relay")))
	defer runInBackground(ctx, relay.Run)()

	pgStore := catalog.NewPgStore(pool)
	reference, err := catalog.LoadReference(ctx, pgStore)
	if err != nil {
		return fmt.Errorf("load reference data: %w", err)
	}

	events := catalog.NewEventHandler(pgStore, reference, routeCache)
	defer runInBackground(ctx, func(ctx context.Context) {
		messaging.Consume(ctx, rabbitmqURL, availabilityChanged, events.HandleAvailabilityChanged,
			log.With(slog.String("component", "availability_consumer")))
	})()

	service := catalog.NewService(pgStore, reference, routeCache, catalog.NewBookingClient(bookingURL))
	handler := catalog.NewHandler(service, log, authn.NewVerifier(publicKey))

	router := httpserver.NewRouter(log, appMetrics, handler.Routes)
	return httpserver.Run(ctx, log, httpserver.New(base.HTTPAddr, router), base.ShutdownTimeout)
}

func newSearchCache(
	ctx context.Context, log *slog.Logger, redisURL string, ttl time.Duration, registerer prometheus.Registerer,
) (routeCache *catalog.RouteDayCache, closeCache func(), err error) {
	if redisURL == "" {
		log.InfoContext(ctx, "search cache disabled: REDIS_URL is not set")
		return nil, func() {}, nil
	}

	client, err := cache.New(redisURL)
	if err != nil {
		return nil, nil, fmt.Errorf("create search cache: %w", err)
	}

	log.InfoContext(ctx, "search cache enabled", slog.Duration("ttl", ttl))
	return catalog.NewRouteDayCache(client, ttl, log, registerer), func() { _ = client.Close() }, nil
}

// runInBackground starts fn and returns a function that stops it and waits until it has returned.
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
