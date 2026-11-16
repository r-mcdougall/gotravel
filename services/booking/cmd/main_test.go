package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/dbtest"
	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/internal/platform/messagingtest"
)

// rabbitmqURL is a throwaway RabbitMQ the service consumes catalog events from.
var rabbitmqURL string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	url, terminate, err := dbtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer terminate()

	rabbitmq, terminateRabbitMQ, err := messagingtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer terminateRabbitMQ()
	rabbitmqURL = rabbitmq

	for key, value := range map[string]string{"DATABASE_URL": url, "RABBITMQ_URL": rabbitmqURL} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return m.Run()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func fetch(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		return -1, ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1, ""
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

// startService runs the service on a free port and waits until it is healthy.
// stop shuts it down and returns what run returned.
func startService(t *testing.T) (addr string, stop func() error) {
	t.Helper()
	return startServiceWithLogs(t, io.Discard)
}

func startServiceWithLogs(t *testing.T, logs io.Writer) (addr string, stop func() error) {
	t.Helper()
	addr = freeAddr(t)
	t.Setenv("HTTP_ADDR", addr)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, logs) }()

	require.Eventually(t, func() bool {
		code, _ := fetch(t, "http://"+addr+"/healthz")
		return code == http.StatusOK
	}, 10*time.Second, 10*time.Millisecond)

	return addr, func() error {
		cancel()
		return <-done
	}
}

func queryCount(t *testing.T, query string) int {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer func() { _ = conn.Close(t.Context()) }()

	var count int
	require.NoError(t, conn.QueryRow(t.Context(), query).Scan(&count))
	return count
}

func TestRun_servesOperationalEndpoints(t *testing.T) {
	addr, stop := startService(t)

	code, body := fetch(t, "http://"+addr+"/healthz")
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{"status":"ok"}`, body)

	code, body = fetch(t, "http://"+addr+"/metrics")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "go_goroutines")

	code, body = fetch(t, "http://"+addr+"/nope")
	require.Equal(t, http.StatusNotFound, code)
	require.JSONEq(t, `{"error":{"code":"NOT_FOUND","message":"route not found"}}`, body)

	require.NoError(t, stop())
}

func TestRun_healthcheckSucceedsWhileServiceRuns(t *testing.T) {
	_, stop := startService(t)

	require.NoError(t, run(t.Context(), []string{"-healthcheck"}, io.Discard))

	require.NoError(t, stop())
}

func TestRun_healthcheckFailsWhenServiceIsDown(t *testing.T) {
	t.Setenv("HTTP_ADDR", freeAddr(t))

	require.Error(t, run(t.Context(), []string{"-healthcheck"}, io.Discard))
}

func TestRun_healthcheckIgnoresUnrelatedConfigProblems(t *testing.T) {
	_, stop := startService(t)
	t.Setenv("SHUTDOWN_TIMEOUT", "soon")

	require.NoError(t, run(t.Context(), []string{"-healthcheck"}, io.Discard))

	require.NoError(t, stop())
}

func TestRun_failsOnInvalidConfig(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT", "soon")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "SHUTDOWN_TIMEOUT")
}

func TestRun_failsWithoutDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "DATABASE_URL is required")
}

func TestRun_failsWithoutRabbitMQURL(t *testing.T) {
	t.Setenv("RABBITMQ_URL", "")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "RABBITMQ_URL is required")
}

func TestRun_failsWhenDatabaseIsUnreachable(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://booking_svc:x@127.0.0.1:1/booking")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "apply migrations")
}

func TestRun_failsOnInvalidLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "loud")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "invalid log level")
}

func TestRun_appliesMigrationsOnStart(t *testing.T) {
	_, stop := startService(t)

	tables := queryCount(t,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name = 'flight_inventories'`)
	require.Equal(t, 1, tables)

	require.NoError(t, stop())
}

// logSignal closes ready once a log line containing match is written.
type logSignal struct {
	match string
	ready chan struct{}
	once  sync.Once
}

func (s *logSignal) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(s.match)) {
		s.once.Do(func() { close(s.ready) })
	}
	return len(p), nil
}

func TestRun_appliesFlightChangedEventsToTheInventory(t *testing.T) {
	// Messages published before the queue exists are dropped, so wait until the consumer has declared it.
	started := &logSignal{match: "rabbitmq consumer started", ready: make(chan struct{})}
	_, stop := startServiceWithLogs(t, started)
	select {
	case <-started.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("flight.changed consumer did not start")
	}

	const flightID = "0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"
	publisher := messaging.NewPublisher(rabbitmqURL, "catalog.events")
	t.Cleanup(func() { _ = publisher.Close() })
	body := fmt.Sprintf(`{
		"event_id": "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e",
		"type": "flight.changed",
		"occurred_at": "2026-11-16T10:00:00Z",
		"data": {
			"flight_id": %q, "version": 1,
			"flight_number": "GA410", "airline_code": "GA",
			"origin_code": "CGK", "destination_code": "DPS",
			"departure_at": "2027-03-14T23:00:00Z", "arrival_at": "2027-03-15T00:55:00Z",
			"price": 1250000, "capacity": 180
		}
	}`, flightID)
	require.NoError(t, publisher.Publish(t.Context(), []messaging.Message{{
		ID: "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e", Type: "flight.changed", Timestamp: time.Now(), Body: []byte(body),
	}}))

	require.Eventually(t, func() bool {
		conn, err := pgx.Connect(t.Context(), os.Getenv("DATABASE_URL"))
		if err != nil {
			return false
		}
		defer func() { _ = conn.Close(t.Context()) }()

		var capacity int32
		err = conn.QueryRow(t.Context(), `SELECT capacity FROM flight_inventories WHERE flight_id = $1`, flightID).Scan(&capacity)
		return err == nil && capacity == 180
	}, 10*time.Second, 50*time.Millisecond, "the event reaches the inventory")

	require.NoError(t, stop())
}
