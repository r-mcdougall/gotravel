package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-chi/chi/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/config"
	"github.com/r-mcdougall/gotravel/internal/platform/database"
	"github.com/r-mcdougall/gotravel/internal/platform/health"
	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
	"github.com/r-mcdougall/gotravel/internal/platform/logging"
	"github.com/r-mcdougall/gotravel/internal/platform/metrics"
	"github.com/r-mcdougall/gotravel/services/payment/migrations"
)

const serviceName = "payment"

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

	if *healthCheck {
		return health.Probe(ctx, base.HTTPAddr)
	}
	if err := loader.Err(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	log, err := logging.New(stdout, serviceName, base.LogLevel)
	if err != nil {
		return err
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

	router := httpserver.NewRouter(log, appMetrics, func(chi.Router) {})
	return httpserver.Run(ctx, log, httpserver.New(base.HTTPAddr, router), base.ShutdownTimeout)
}
