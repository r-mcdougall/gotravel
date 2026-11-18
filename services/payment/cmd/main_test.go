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
	"net/http/httptest"
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

// rabbitmqURL is a throwaway RabbitMQ the service publishes payment events to.
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

	keyDir, err := os.MkdirTemp("", "payment-test-keys")
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

	// Tests that talk to booking point BOOKING_URL at a fake booking service; the rest never reach it.
	for key, value := range map[string]string{
		"DATABASE_URL": url, "RABBITMQ_URL": rabbitmqURL, "JWT_PUBLIC_KEY_FILE": keyFile, "BOOKING_URL": "http://127.0.0.1:1",
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

func TestRun_failsWithoutBookingURL(t *testing.T) {
	t.Setenv("BOOKING_URL", "")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "BOOKING_URL is required")
}

func TestRun_failsOnInvalidBookingURL(t *testing.T) {
	for _, value := range []string{"booking:8080", "/api/v1", "http://"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("BOOKING_URL", value)

			err := run(t.Context(), nil, io.Discard)

			require.ErrorContains(t, err, "BOOKING_URL must be an absolute URL")
		})
	}
}

func TestRun_failsWhenDatabaseIsUnreachable(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://payment_svc:x@127.0.0.1:1/payment")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "apply migrations")
}

func TestRun_appliesMigrationsOnStart(t *testing.T) {
	_, stop := startService(t)

	tables := queryCount(t,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name IN ('payments', 'outbox')`)
	require.Equal(t, 2, tables)

	require.NoError(t, stop())
}

const (
	johnID             = "11111111-1111-4111-8111-111111111111"
	janeID             = "22222222-2222-4222-8222-222222222222"
	heldBookingID      = "5f0c6f1e-2b7a-4d3e-9c8b-7a6e5d4c3b21"
	confirmedBookingID = "6b1d7a2f-3c8b-4e4f-8d9c-8b7f6e5d4c32"
	unknownBookingID   = "7c2e8b3a-4d9c-4f5a-9e0d-9c8a7f6e5d43"
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

func truncatePaymentTables(t *testing.T) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer func() { _ = conn.Close(t.Context()) }()

	_, err = conn.Exec(t.Context(), "TRUNCATE payments, outbox")
	require.NoError(t, err)
}

// startFakeBooking points BOOKING_URL at a booking service that shows ownerToken's
// held and confirmed bookings and answers 404 for anything else.
func startFakeBooking(t *testing.T, ownerToken string) {
	t.Helper()

	statuses := map[string]string{heldBookingID: "HELD", confirmedBookingID: "CONFIRMED"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/v1/bookings/")
		status, ok := statuses[id]
		if !ok || r.Header.Get("Authorization") != "Bearer "+ownerToken {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":          id,
			"status":      status,
			"total_price": 2_500_000,
			"expires_at":  time.Now().Add(15 * time.Minute),
		})
	}))
	t.Cleanup(server.Close)
	t.Setenv("BOOKING_URL", server.URL)
}

func TestRun_paymentEndpointsRequireAToken(t *testing.T) {
	addr, stop := startService(t)

	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/payments"},
		{http.MethodGet, "/api/v1/payments/" + heldBookingID},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			code, body := send(t, route.method, "http://"+addr+route.path, "", "")

			require.Equal(t, http.StatusUnauthorized, code, body)
			require.Equal(t, "UNAUTHORIZED", errorCode(t, body))
		})
	}

	require.NoError(t, stop())
}

func TestRun_userPaysAHeldBooking(t *testing.T) {
	truncatePaymentTables(t)
	john, jane := signToken(t, johnID), signToken(t, janeID)
	startFakeBooking(t, john)
	addr, stop := startService(t)
	payments := "http://" + addr + "/api/v1/payments"

	type paymentResponse struct {
		ID        string `json:"id"`
		BookingID string `json:"booking_id"`
		Amount    int64  `json:"amount"`
		Status    string `json:"status"`
		CreatedAt string `json:"created_at"`
		UpdatedAt string `json:"updated_at"`
	}

	code, body := send(t, http.MethodPost, payments, john, fmt.Sprintf(`{"booking_id": %q, "outcome": "failed"}`, heldBookingID))
	require.Equal(t, http.StatusCreated, code, body)
	require.Contains(t, body, `"status":"FAILED"`)

	code, body = send(t, http.MethodPost, payments, john, fmt.Sprintf(`{"booking_id": %q}`, heldBookingID))
	require.Equal(t, http.StatusCreated, code, body)
	var paid paymentResponse
	require.NoError(t, json.Unmarshal([]byte(body), &paid))
	require.Equal(t, heldBookingID, paid.BookingID)
	require.Equal(t, int64(2_500_000), paid.Amount)
	require.Equal(t, "SUCCEEDED", paid.Status)

	code, body = send(t, http.MethodPost, payments, john, fmt.Sprintf(`{"booking_id": %q}`, heldBookingID))
	require.Equal(t, http.StatusConflict, code, body)
	require.Equal(t, "ALREADY_PAID", errorCode(t, body))

	code, body = send(t, http.MethodGet, payments+"/"+paid.ID, john, "")
	require.Equal(t, http.StatusOK, code, body)
	var fetched paymentResponse
	require.NoError(t, json.Unmarshal([]byte(body), &fetched))
	require.Equal(t, paid, fetched)

	code, body = send(t, http.MethodGet, payments+"/"+paid.ID, jane, "")
	require.Equal(t, http.StatusNotFound, code, body)
	require.Equal(t, "NOT_FOUND", errorCode(t, body), "another user's payment looks missing")

	code, body = send(t, http.MethodPost, payments, jane, fmt.Sprintf(`{"booking_id": %q}`, heldBookingID))
	require.Equal(t, http.StatusNotFound, code, body)
	require.Equal(t, "NOT_FOUND", errorCode(t, body), "another user's booking cannot be paid")

	require.Equal(t, 2, queryCount(t, `SELECT count(*) FROM payments`))

	require.NoError(t, stop())
}

func TestRun_refusesPaymentsBookingDoesNotAllow(t *testing.T) {
	truncatePaymentTables(t)
	john := signToken(t, johnID)
	startFakeBooking(t, john)
	addr, stop := startService(t)
	payments := "http://" + addr + "/api/v1/payments"

	for _, tt := range []struct {
		name, body, wantCode string
		wantStatus           int
	}{
		{"confirmed booking", fmt.Sprintf(`{"booking_id": %q}`, confirmedBookingID), "BOOKING_NOT_PAYABLE", http.StatusConflict},
		{"unknown booking", fmt.Sprintf(`{"booking_id": %q}`, unknownBookingID), "NOT_FOUND", http.StatusNotFound},
		{"malformed booking id", `{"booking_id": "abc", "outcome": "maybe"}`, "VALIDATION_ERROR", http.StatusBadRequest},
		{"unknown field", fmt.Sprintf(`{"booking_id": %q, "amount": 1}`, heldBookingID), "VALIDATION_ERROR", http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			code, body := send(t, http.MethodPost, payments, john, tt.body)

			require.Equal(t, tt.wantStatus, code, body)
			require.Equal(t, tt.wantCode, errorCode(t, body))
		})
	}

	require.Zero(t, queryCount(t, `SELECT count(*) FROM payments`))

	require.NoError(t, stop())
}

func TestRun_answersServiceUnavailableWhenBookingIsDown(t *testing.T) {
	addr, stop := startService(t)

	code, body := send(t, http.MethodPost, "http://"+addr+"/api/v1/payments", signToken(t, johnID),
		fmt.Sprintf(`{"booking_id": %q}`, heldBookingID))

	require.Equal(t, http.StatusServiceUnavailable, code, body)
	require.Equal(t, "BOOKING_UNAVAILABLE", errorCode(t, body))

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

func TestRun_publishesPaymentSucceededToRabbitMQ(t *testing.T) {
	truncatePaymentTables(t)

	// Messages published before the queue exists are dropped, so wait until the consumer has declared it.
	started := &logSignal{match: []string{"rabbitmq consumer started"}, ready: make(chan struct{})}
	received := make(chan messaging.Delivery, 10)
	ctx, cancel := context.WithCancel(t.Context())
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		messaging.Consume(ctx, rabbitmqURL, messaging.Subscription{
			Exchange: "payment.events", RoutingKey: "payment.succeeded", Queue: "test.payment-succeeded",
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

	john := signToken(t, johnID)
	startFakeBooking(t, john)
	addr, stop := startService(t)
	code, body := send(t, http.MethodPost, "http://"+addr+"/api/v1/payments", john, fmt.Sprintf(`{"booking_id": %q}`, heldBookingID))
	require.Equal(t, http.StatusCreated, code, body)
	var paid struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &paid))

	var delivery messaging.Delivery
	select {
	case delivery = <-received:
	case <-time.After(10 * time.Second):
		t.Fatal("no payment.succeeded event reached RabbitMQ")
	}

	require.Equal(t, "payment.succeeded", delivery.Type)
	var event struct {
		EventID string `json:"event_id"`
		Data    struct {
			PaymentID string `json:"payment_id"`
			BookingID string `json:"booking_id"`
			Amount    int64  `json:"amount"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(delivery.Body, &event))
	require.Equal(t, delivery.ID, event.EventID)
	require.Equal(t, paid.ID, event.Data.PaymentID)
	require.Equal(t, heldBookingID, event.Data.BookingID)
	require.Equal(t, int64(2_500_000), event.Data.Amount)

	require.NoError(t, stop())
}

func TestRun_reversesAPaymentBookingRejected(t *testing.T) {
	truncatePaymentTables(t)
	john := signToken(t, johnID)
	startFakeBooking(t, john)
	addr, stop := startServiceWithConsumer(t, "payment_rejected_consumer")
	payments := "http://" + addr + "/api/v1/payments"

	code, body := send(t, http.MethodPost, payments, john, fmt.Sprintf(`{"booking_id": %q}`, heldBookingID))
	require.Equal(t, http.StatusCreated, code, body)
	var paid struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &paid))

	publisher := messaging.NewPublisher(rabbitmqURL, "booking.events")
	t.Cleanup(func() { _ = publisher.Close() })
	require.NoError(t, publisher.Publish(t.Context(), []messaging.Message{{
		ID: "3a2f1e0d-9c8b-4a7f-a6e5-d4c3b2a1908f", Type: "payment.rejected", Timestamp: time.Now(),
		Body: []byte(fmt.Sprintf(`{
			"event_id": "3a2f1e0d-9c8b-4a7f-a6e5-d4c3b2a1908f",
			"type": "payment.rejected",
			"occurred_at": "2026-11-19T10:00:05Z",
			"data": {"payment_id": %q, "booking_id": %q, "reason": "booking_expired"}
		}`, paid.ID, heldBookingID)),
	}}))

	require.Eventually(t, func() bool {
		return paymentStatus(t.Context(), payments+"/"+paid.ID, john) == "REVERSED"
	}, 10*time.Second, 50*time.Millisecond, "the event reverses the payment")

	require.NoError(t, stop())
}

// paymentStatus returns the status of a payment, or "" when it cannot be read; safe inside require.Eventually.
func paymentStatus(ctx context.Context, url, token string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()

	var payment struct {
		Status string `json:"status"`
	}
	if json.NewDecoder(resp.Body).Decode(&payment) != nil {
		return ""
	}
	return payment.Status
}
