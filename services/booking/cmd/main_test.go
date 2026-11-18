package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/internal/platform/dbtest"
	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/internal/platform/messagingtest"
)

// rabbitmqURL is a throwaway RabbitMQ the service consumes catalog events from.
var rabbitmqURL string

// testSigningKey signs the tokens the tests send; its public half is what the
// service under test loads from JWT_PUBLIC_KEY_FILE.
var testSigningKey ed25519.PrivateKey

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

	keyDir, err := os.MkdirTemp("", "booking-test-keys")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(keyDir) }()

	privatePEM, publicPEM, err := authn.GenerateKeyPair()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	testSigningKey, err = authn.ParsePrivateKey(privatePEM)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	keyFile := filepath.Join(keyDir, "jwt_public.pem")
	if err := os.WriteFile(keyFile, publicPEM, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	for key, value := range map[string]string{
		"DATABASE_URL": url, "RABBITMQ_URL": rabbitmqURL, "JWT_PUBLIC_KEY_FILE": keyFile,
	} {
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

func TestRun_failsWithoutJWTPublicKeyFile(t *testing.T) {
	t.Setenv("JWT_PUBLIC_KEY_FILE", "")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "JWT_PUBLIC_KEY_FILE is required")
}

func TestRun_failsWhenJWTPublicKeyFileIsMissing(t *testing.T) {
	t.Setenv("JWT_PUBLIC_KEY_FILE", filepath.Join(t.TempDir(), "missing.pem"))

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "load jwt public key")
}

func TestRun_failsOnInvalidHoldTTL(t *testing.T) {
	for _, value := range []string{"soon", "0s", "-5m"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("HOLD_TTL", value)

			err := run(t.Context(), nil, io.Discard)

			require.ErrorContains(t, err, "HOLD_TTL")
		})
	}
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

// logSignal closes ready once a log line containing every match is written.
type logSignal struct {
	match []string
	ready chan struct{}
	once  sync.Once
}

func (s *logSignal) Write(p []byte) (int, error) {
	for _, m := range s.match {
		if !bytes.Contains(p, []byte(m)) {
			return len(p), nil
		}
	}
	s.once.Do(func() { close(s.ready) })
	return len(p), nil
}

// startServiceWithConsumer starts the service and waits until the consumer logging as component
// has declared its queue, since messages published before that are dropped.
func startServiceWithConsumer(t *testing.T, component string) (addr string, stop func() error) {
	t.Helper()
	started := &logSignal{
		match: []string{"rabbitmq consumer started", `"component":"` + component + `"`},
		ready: make(chan struct{}),
	}
	addr, stop = startServiceWithLogs(t, started)
	select {
	case <-started.ready:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s did not start", component)
	}
	return addr, stop
}

func TestRun_appliesFlightChangedEventsToTheInventory(t *testing.T) {
	truncateBookingTables(t)
	_, stop := startServiceWithConsumer(t, "flight_changed_consumer")

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

const (
	johnID = "11111111-1111-4111-8111-111111111111"
	janeID = "22222222-2222-4222-8222-222222222222"
)

func signToken(t *testing.T, subject string) string {
	t.Helper()

	claims := authn.AccessClaims{
		Role: authn.RoleUser,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    authn.IssuerName,
			Subject:   subject,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(testSigningKey)
	require.NoError(t, err)
	return signed
}

func send(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	require.NoError(t, err)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(respBody)
}

func errorCode(t *testing.T, body string) string {
	t.Helper()
	var parsed struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &parsed), body)
	return parsed.Error.Code
}

func truncateBookingTables(t *testing.T) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer func() { _ = conn.Close(t.Context()) }()

	_, err = conn.Exec(t.Context(), "TRUNCATE passengers, bookings, outbox, flight_inventories")
	require.NoError(t, err)
}

// resetFlight empties the booking tables and adds one upcoming flight with the given capacity.
func resetFlight(t *testing.T, id string, capacity int) {
	t.Helper()
	truncateBookingTables(t)

	conn, err := pgx.Connect(t.Context(), os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer func() { _ = conn.Close(t.Context()) }()

	_, err = conn.Exec(t.Context(), `
		INSERT INTO flight_inventories (flight_id, version, flight_number, airline_code, origin_code, destination_code,
		                                departure_at, arrival_at, price, capacity)
		VALUES ($1, 1, 'GA410', 'GA', 'CGK', 'DPS', now() + interval '7 days', now() + interval '7 days 2 hours', 1250000, $2)`,
		id, capacity)
	require.NoError(t, err)
}

func TestRun_bookingEndpointsRequireAToken(t *testing.T) {
	addr, stop := startService(t)

	const id = "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e"
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/bookings"},
		{http.MethodGet, "/api/v1/bookings"},
		{http.MethodGet, "/api/v1/bookings/" + id},
		{http.MethodPost, "/api/v1/bookings/" + id + "/cancel"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			code, body := send(t, route.method, "http://"+addr+route.path, "", "")

			require.Equal(t, http.StatusUnauthorized, code, body)
			require.Equal(t, "UNAUTHORIZED", errorCode(t, body))
		})
	}

	require.NoError(t, stop())
}

func TestRun_userHoldsListsAndCancelsABooking(t *testing.T) {
	const flightID = "0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"
	resetFlight(t, flightID, 3)
	addr, stop := startService(t)
	bookings := "http://" + addr + "/api/v1/bookings"
	john, jane := signToken(t, johnID), signToken(t, janeID)

	type booking struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Flight struct {
			ID           string `json:"id"`
			FlightNumber string `json:"flight_number"`
		} `json:"flight"`
		Passengers []struct {
			FullName  string `json:"full_name"`
			BirthDate string `json:"birth_date"`
		} `json:"passengers"`
		PassengerCount int32  `json:"passenger_count"`
		SeatPrice      int64  `json:"seat_price"`
		TotalPrice     int64  `json:"total_price"`
		ExpiresAt      string `json:"expires_at"`
	}
	holdBody := fmt.Sprintf(`{"flight_id": %q, "passengers": [
		{"full_name": "John Doe", "birth_date": "1990-05-17"},
		{"full_name": "Jane Smith", "birth_date": "1992-08-03"}
	]}`, flightID)

	code, body := send(t, http.MethodPost, bookings, john, holdBody)
	require.Equal(t, http.StatusCreated, code, body)
	var held booking
	require.NoError(t, json.Unmarshal([]byte(body), &held))
	require.Equal(t, "HELD", held.Status)
	require.Equal(t, flightID, held.Flight.ID)
	require.Equal(t, int32(2), held.PassengerCount)
	require.Equal(t, int64(2_500_000), held.TotalPrice)
	require.Len(t, held.Passengers, 2)
	expiresAt, err := time.Parse(time.RFC3339, held.ExpiresAt)
	require.NoError(t, err)
	require.WithinDuration(t, time.Now().Add(15*time.Minute), expiresAt, time.Minute, "the default hold lasts 15 minutes")

	code, body = send(t, http.MethodPost, bookings, jane, holdBody)
	require.Equal(t, http.StatusConflict, code, body)
	require.Equal(t, "NOT_ENOUGH_SEATS", errorCode(t, body), "one seat is left for two passengers")

	code, body = send(t, http.MethodPost, bookings, john, `{"flight_id": "GA410", "passengers": []}`)
	require.Equal(t, http.StatusBadRequest, code, body)
	require.Equal(t, "VALIDATION_ERROR", errorCode(t, body))
	require.Contains(t, body, `"field":"flight_id"`)
	require.Contains(t, body, `"field":"passengers"`)

	code, body = send(t, http.MethodPost, bookings, john, `{"flight_id": "x", "unknown": true}`)
	require.Equal(t, http.StatusBadRequest, code, body)
	require.Equal(t, "VALIDATION_ERROR", errorCode(t, body), "unknown fields are rejected")

	code, body = send(t, http.MethodGet, bookings+"/"+held.ID, john, "")
	require.Equal(t, http.StatusOK, code, body)
	var fetched booking
	require.NoError(t, json.Unmarshal([]byte(body), &fetched))
	require.Equal(t, held, fetched)

	code, body = send(t, http.MethodGet, bookings+"/"+held.ID, jane, "")
	require.Equal(t, http.StatusNotFound, code, body)
	require.Equal(t, "NOT_FOUND", errorCode(t, body), "another user's booking looks missing")

	code, body = send(t, http.MethodGet, bookings+"?page_size=1", john, "")
	require.Equal(t, http.StatusOK, code, body)
	var list struct {
		Data []booking `json:"data"`
		Meta struct {
			Page     int32 `json:"page"`
			PageSize int32 `json:"page_size"`
			Total    int64 `json:"total"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &list))
	require.Equal(t, int64(1), list.Meta.Total)
	require.Equal(t, int32(1), list.Meta.PageSize)
	require.Equal(t, []booking{held}, list.Data)

	code, body = send(t, http.MethodGet, bookings, jane, "")
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"data":[]`)

	code, body = send(t, http.MethodGet, bookings+"?page=0", john, "")
	require.Equal(t, http.StatusBadRequest, code, body)
	require.Equal(t, "VALIDATION_ERROR", errorCode(t, body))

	code, body = send(t, http.MethodPost, bookings+"/"+held.ID+"/cancel", john, "")
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"status":"CANCELLED"`)

	code, body = send(t, http.MethodPost, bookings+"/"+held.ID+"/cancel", john, "")
	require.Equal(t, http.StatusConflict, code, body)
	require.Equal(t, "BOOKING_NOT_HELD", errorCode(t, body))

	code, body = send(t, http.MethodPost, bookings, jane, holdBody)
	require.Equal(t, http.StatusCreated, code, body, "the cancelled seats can be held again")

	require.NoError(t, stop())
}

func TestRun_expiresHoldsThatAreNotPaidInTime(t *testing.T) {
	const flightID = "0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"
	resetFlight(t, flightID, 3)
	t.Setenv("HOLD_TTL", "1s")
	addr, stop := startService(t)
	john := signToken(t, johnID)

	code, body := fetch(t, "http://"+addr+"/metrics")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "booking_holds_expired_total 0")

	code, body = send(t, http.MethodPost, "http://"+addr+"/api/v1/bookings", john, fmt.Sprintf(
		`{"flight_id": %q, "passengers": [{"full_name": "John Doe", "birth_date": "1990-05-17"}]}`, flightID))
	require.Equal(t, http.StatusCreated, code, body)
	var held struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &held))

	require.Eventually(t, func() bool {
		return bookingStatus(t.Context(), addr, john, held.ID) == "EXPIRED"
	}, 15*time.Second, 100*time.Millisecond, "the expirer runs every 5 seconds")

	require.Zero(t, queryCount(t, `SELECT held_seats FROM flight_inventories`), "the seat is released")
	code, body = fetch(t, "http://"+addr+"/metrics")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "booking_holds_expired_total 1")

	require.NoError(t, stop())
}

// bookingStatus returns the status of a booking, or "" when it cannot be read; safe inside require.Eventually.
func bookingStatus(ctx context.Context, addr, token, id string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/api/v1/bookings/"+id, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	var booking struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(resp.Body).Decode(&booking) != nil {
		return ""
	}
	return booking.Status
}

func TestRun_publishesSeatAvailabilityToRabbitMQ(t *testing.T) {
	const flightID = "0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"
	resetFlight(t, flightID, 3)

	// Messages published before the queue exists are dropped, so wait until the consumer has declared it.
	started := &logSignal{match: []string{"rabbitmq consumer started"}, ready: make(chan struct{})}
	received := make(chan messaging.Delivery, 10)
	ctx, cancel := context.WithCancel(t.Context())
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		messaging.Consume(ctx, rabbitmqURL, messaging.Subscription{
			Exchange: "booking.events", RoutingKey: "flight.availability_changed", Queue: "test.booking-availability",
		}, func(_ context.Context, d messaging.Delivery) error {
			received <- d
			return nil
		}, slog.New(slog.NewTextHandler(started, nil)))
	}()
	t.Cleanup(func() {
		cancel()
		<-consumerDone
	})
	select {
	case <-started.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("test consumer did not start")
	}

	addr, stop := startService(t)
	code, body := send(t, http.MethodPost, "http://"+addr+"/api/v1/bookings", signToken(t, johnID), fmt.Sprintf(
		`{"flight_id": %q, "passengers": [{"full_name": "John Doe", "birth_date": "1990-05-17"}]}`, flightID))
	require.Equal(t, http.StatusCreated, code, body)

	var delivery messaging.Delivery
	select {
	case delivery = <-received:
	case <-time.After(10 * time.Second):
		t.Fatal("no flight.availability_changed event reached RabbitMQ")
	}

	require.Equal(t, "flight.availability_changed", delivery.Type)
	var event struct {
		EventID string `json:"event_id"`
		Data    struct {
			FlightID            string `json:"flight_id"`
			AvailabilityVersion int64  `json:"availability_version"`
			AvailableSeats      int32  `json:"available_seats"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(delivery.Body, &event))
	require.Equal(t, delivery.ID, event.EventID)
	require.Equal(t, flightID, event.Data.FlightID)
	require.Equal(t, int64(1), event.Data.AvailabilityVersion)
	require.Equal(t, int32(2), event.Data.AvailableSeats, "three seats, one held")

	require.NoError(t, stop())
}

func TestRun_confirmsABookingWhenItsPaymentSucceeds(t *testing.T) {
	const flightID = "0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"
	resetFlight(t, flightID, 3)
	addr, stop := startServiceWithConsumer(t, "payment_succeeded_consumer")
	john := signToken(t, johnID)

	code, body := send(t, http.MethodPost, "http://"+addr+"/api/v1/bookings", john, fmt.Sprintf(
		`{"flight_id": %q, "passengers": [{"full_name": "John Doe", "birth_date": "1990-05-17"}]}`, flightID))
	require.Equal(t, http.StatusCreated, code, body)
	var held struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &held))

	const paymentID = "9d8c7b6a-5f4e-4d3c-8b2a-1f0e9d8c7b6a"
	publisher := messaging.NewPublisher(rabbitmqURL, "payment.events")
	t.Cleanup(func() { _ = publisher.Close() })
	require.NoError(t, publisher.Publish(t.Context(), []messaging.Message{{
		ID: "4b3a2f1e-0d9c-4b8a-a7f6-e5d4c3b2a190", Type: "payment.succeeded", Timestamp: time.Now(),
		Body: []byte(fmt.Sprintf(`{
			"event_id": "4b3a2f1e-0d9c-4b8a-a7f6-e5d4c3b2a190",
			"type": "payment.succeeded",
			"occurred_at": "2026-11-19T10:00:00Z",
			"data": {"payment_id": %q, "booking_id": %q, "amount": 1250000}
		}`, paymentID, held.ID)),
	}}))

	require.Eventually(t, func() bool {
		return bookingStatus(t.Context(), addr, john, held.ID) == "CONFIRMED"
	}, 10*time.Second, 50*time.Millisecond, "the event confirms the booking")

	code, body = send(t, http.MethodGet, "http://"+addr+"/api/v1/bookings/"+held.ID, john, "")
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"payment_id":"`+paymentID+`"`)
	require.Equal(t, 1, queryCount(t, `SELECT sold_seats FROM flight_inventories`))
	require.Zero(t, queryCount(t, `SELECT held_seats FROM flight_inventories`))

	require.NoError(t, stop())
}
