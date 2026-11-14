package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // the planner needs time zones even where the system has none
)

const (
	defaultBaseURL = "http://localhost:8080"
	defaultDays    = 14
	defaultWorkers = 8

	requestTimeout = 15 * time.Second

	// seedZone decides which calendar day is "tomorrow".
	seedZone = "Asia/Jakarta"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := execute(ctx, os.Args[1:], os.Getenv, time.Now(), os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

type config struct {
	baseURL string
	days    int
	workers int
}

// parseConfig reads and checks the flags.
func parseConfig(args []string, out io.Writer) (config, error) {
	var cfg config

	flags := flag.NewFlagSet("seed", flag.ContinueOnError)
	flags.SetOutput(out)
	flags.StringVar(&cfg.baseURL, "base-url", defaultBaseURL, "address of the gateway")
	flags.IntVar(&cfg.days, "days", defaultDays, "number of days to create flights for, starting tomorrow")
	flags.IntVar(&cfg.workers, "workers", defaultWorkers, "number of requests in flight at the same time")
	if err := flags.Parse(args); err != nil {
		return config{}, err
	}

	if cfg.days < 1 {
		return config{}, errors.New("-days must be at least 1")
	}
	if cfg.workers < 1 {
		return config{}, errors.New("-workers must be at least 1")
	}
	if u, err := url.Parse(cfg.baseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return config{}, fmt.Errorf("-base-url must look like http://host:port, got %q", cfg.baseURL)
	}
	return cfg, nil
}

// adminCredentials reads the admin account from the environment, the same variables the auth service uses to create it.
func adminCredentials(getenv func(string) string) (email, password string, err error) {
	var missing []string
	if email = getenv("ADMIN_EMAIL"); email == "" {
		missing = append(missing, "ADMIN_EMAIL")
	}
	if password = getenv("ADMIN_PASSWORD"); password == "" {
		missing = append(missing, "ADMIN_PASSWORD")
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf("%s must be set", strings.Join(missing, " and "))
	}
	return email, password, nil
}

// execute does the work of the command; main only wires it to the process. Progress goes to out, and any problem comes back as an error.
func execute(ctx context.Context, args []string, getenv func(string) string, now time.Time, out io.Writer) error {
	cfg, err := parseConfig(args, out)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}

	email, password, err := adminCredentials(getenv)
	if err != nil {
		return err
	}

	zone, err := time.LoadLocation(seedZone)
	if err != nil {
		return fmt.Errorf("load time zone %s: %w", seedZone, err)
	}
	from := now.In(zone).AddDate(0, 0, 1)
	flights, err := plan(from, cfg.days)
	if err != nil {
		return fmt.Errorf("plan flights: %w", err)
	}

	log := slog.New(slog.NewTextHandler(out, nil))
	log.Info("seeding the catalog",
		slog.String("base_url", cfg.baseURL),
		slog.String("from", from.Format(time.DateOnly)),
		slog.Int("days", cfg.days),
		slog.Int("flights", len(flights)),
		slog.Int("workers", cfg.workers),
	)

	// One idle connection per worker, so requests reuse connections instead of reconnecting.
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment, MaxIdleConnsPerHost: cfg.workers}
	api := newClient(cfg.baseURL, &http.Client{Transport: transport, Timeout: requestTimeout})
	if err := api.login(ctx, email, password); err != nil {
		return err
	}

	result := run(ctx, api, flights, cfg.workers, log)
	log.Info("seeding finished",
		slog.Int("created", result.created),
		slog.Int("existing", result.existing),
		slog.Int("failed", result.failed),
	)

	switch {
	case result.aborted:
		return fmt.Errorf("stopped after %d failures; %d of %d flights were tried", result.failed, result.processed(), len(flights))
	case ctx.Err() != nil:
		return fmt.Errorf("interrupted after %d of %d flights", result.processed(), len(flights))
	case result.failed > 0:
		return fmt.Errorf("%d of %d flights failed", result.failed, len(flights))
	}
	return nil
}
