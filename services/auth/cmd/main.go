package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/internal/platform/config"
	"github.com/r-mcdougall/gotravel/internal/platform/database"
	"github.com/r-mcdougall/gotravel/internal/platform/health"
	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
	"github.com/r-mcdougall/gotravel/internal/platform/logging"
	"github.com/r-mcdougall/gotravel/internal/platform/metrics"
	"github.com/r-mcdougall/gotravel/services/auth/internal/auth"
	"github.com/r-mcdougall/gotravel/services/auth/internal/token"
	"github.com/r-mcdougall/gotravel/services/auth/migrations"
)

const (
	serviceName = "auth"

	defaultAccessTokenTTL  = 15 * time.Minute
	defaultRefreshTokenTTL = 7 * 24 * time.Hour
)

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
	privateKeyFile := loader.Required("JWT_PRIVATE_KEY_FILE")
	accessTTL := loader.Duration("ACCESS_TOKEN_TTL", defaultAccessTokenTTL)
	refreshTTL := loader.Duration("REFRESH_TOKEN_TTL", defaultRefreshTokenTTL)

	if *healthCheck {
		return health.Probe(ctx, base.HTTPAddr)
	}
	if err := loader.Err(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if accessTTL <= 0 || refreshTTL <= 0 {
		return errors.New("load config: ACCESS_TOKEN_TTL and REFRESH_TOKEN_TTL must be positive")
	}

	log, err := logging.New(stdout, serviceName, base.LogLevel)
	if err != nil {
		return err
	}

	privateKey, err := authn.LoadPrivateKey(privateKeyFile)
	if err != nil {
		return fmt.Errorf("load jwt private key: %w", err)
	}

	if err = database.Migrate(ctx, log, databaseURL, migrations.FS); err != nil {
		return err
	}

	pool, err := database.Connect(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	service := auth.NewService(auth.NewPgStore(pool), token.NewIssuer(privateKey, accessTTL), refreshTTL)
	handler := auth.NewHandler(service, log, authn.NewVerifier(privateKey.Public().(ed25519.PublicKey)))

	router := httpserver.NewRouter(log, metrics.New(), handler.Routes)
	return httpserver.Run(ctx, log, httpserver.New(base.HTTPAddr, router), base.ShutdownTimeout)
}
