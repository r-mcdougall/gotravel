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
	"github.com/r-mcdougall/gotravel/internal/platform/cache"
	"github.com/r-mcdougall/gotravel/internal/platform/cachetest"
	"github.com/r-mcdougall/gotravel/internal/platform/dbtest"
	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/internal/platform/messagingtest"
)

// testSigningKey signs the tokens the tests send; its public half is what the
// service under test loads from JWT_PUBLIC_KEY_FILE.
var testSigningKey ed25519.PrivateKey

// redisURL is a throwaway Redis. Tests opt in to the search cache by setting
// REDIS_URL to it; without that the service runs with the cache off.
var redisURL string

// rabbitmqURL is a throwaway RabbitMQ the outbox relay publishes to.
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

	redis, terminateRedis, err := cachetest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer terminateRedis()
	redisURL = redis

	rabbitmq, terminateRabbitMQ, err := messagingtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer terminateRabbitMQ()
	rabbitmqURL = rabbitmq

	keyDir, err := os.MkdirTemp("", "catalog-test-keys")
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

	// By default booking knows no flight, so lowering a capacity is allowed; tests that
	// need held or sold seats point BOOKING_URL at their own fake.
	booking := httptest.NewServer(http.NotFoundHandler())
	defer booking.Close()

	for key, value := range map[string]string{
		"DATABASE_URL": url, "JWT_PUBLIC_KEY_FILE": keyFile, "REDIS_URL": "", "RABBITMQ_URL": rabbitmqURL,
		"BOOKING_URL": booking.URL,
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

func TestRun_appliesMigrationsOnStart(t *testing.T) {
	_, stop := startService(t)

	tables := queryCount(t,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public'
		   AND table_name IN ('airlines', 'airports', 'flights', 'outbox', 'cities', 'hotels', 'room_types')`)
	require.Equal(t, 7, tables)

	require.NoError(t, stop())
}

func TestRun_loadsReferenceDataOnStart(t *testing.T) {
	_, stop := startService(t)

	require.Equal(t, 8, queryCount(t, `SELECT count(*) FROM airlines`))
	require.Equal(t, 11, queryCount(t, `SELECT count(*) FROM airports`))
	require.Equal(t, 11, queryCount(t, `SELECT count(*) FROM cities`))

	require.NoError(t, stop())
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

func TestRun_failsWithoutBookingURL(t *testing.T) {
	t.Setenv("BOOKING_URL", "")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "BOOKING_URL is required")
}

func TestRun_failsOnInvalidBookingURL(t *testing.T) {
	for _, value := range []string{"booking:8080", "/internal/v1", "http://"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("BOOKING_URL", value)

			err := run(t.Context(), nil, io.Discard)

			require.ErrorContains(t, err, "BOOKING_URL must be an absolute URL")
		})
	}
}

func TestRun_failsWhenDatabaseIsUnreachable(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://catalog_svc:x@127.0.0.1:1/catalog")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "apply migrations")
}

func TestRun_failsOnInvalidLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "loud")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "invalid log level")
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

func TestRun_failsWhenJWTPublicKeyFileIsNotAKey(t *testing.T) {
	notAKey := filepath.Join(t.TempDir(), "garbage.pem")
	require.NoError(t, os.WriteFile(notAKey, []byte("not a pem file"), 0o600))
	t.Setenv("JWT_PUBLIC_KEY_FILE", notAKey)

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "load jwt public key")
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

func signToken(t *testing.T, key ed25519.PrivateKey, role string, expiresAt time.Time) string {
	t.Helper()

	claims := authn.AccessClaims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    authn.IssuerName,
			Subject:   "00000000-0000-4000-8000-000000000001",
			ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(key)
	require.NoError(t, err)
	return signed
}

func truncateFlights(t *testing.T) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer func() { _ = conn.Close(t.Context()) }()

	_, err = conn.Exec(t.Context(), "TRUNCATE flights, outbox")
	require.NoError(t, err)
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

func TestRun_adminEndpointsRequireAnAdminToken(t *testing.T) {
	addr, stop := startService(t)

	otherPrivatePEM, _, err := authn.GenerateKeyPair()
	require.NoError(t, err)
	otherKey, err := authn.ParsePrivateKey(otherPrivatePEM)
	require.NoError(t, err)

	inAnHour := time.Now().Add(time.Hour)
	credentials := []struct {
		name       string
		token      string
		wantStatus int
		wantCode   string
	}{
		{"no token", "", http.StatusUnauthorized, "UNAUTHORIZED"},
		{"malformed token", "not-a-jwt", http.StatusUnauthorized, "UNAUTHORIZED"},
		{"expired admin token", signToken(t, testSigningKey, authn.RoleAdmin, time.Now().Add(-time.Hour)), http.StatusUnauthorized, "UNAUTHORIZED"},
		{"admin token signed by another key", signToken(t, otherKey, authn.RoleAdmin, inAnHour), http.StatusUnauthorized, "UNAUTHORIZED"},
		{"user token", signToken(t, testSigningKey, authn.RoleUser, inAnHour), http.StatusForbidden, "FORBIDDEN"},
	}

	const id = "0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/admin/flights"},
		{http.MethodPost, "/api/v1/admin/flights"},
		{http.MethodGet, "/api/v1/admin/flights/" + id},
		{http.MethodPatch, "/api/v1/admin/flights/" + id},
		{http.MethodPost, "/api/v1/admin/hotels"},
	}

	for _, route := range routes {
		for _, credential := range credentials {
			t.Run(route.method+" "+route.path+" with "+credential.name, func(t *testing.T) {
				code, body := send(t, route.method, "http://"+addr+route.path, credential.token, `{}`)

				require.Equal(t, credential.wantStatus, code, body)
				require.Equal(t, credential.wantCode, errorCode(t, body))
			})
		}
	}

	require.NoError(t, stop())
}

func TestRun_adminManagesAFlightEndToEnd(t *testing.T) {
	truncateFlights(t)
	addr, stop := startService(t)
	admin := signToken(t, testSigningKey, authn.RoleAdmin, time.Now().Add(time.Hour))
	flights := "http://" + addr + "/api/v1/admin/flights"

	type flight struct {
		ID           string `json:"id"`
		FlightNumber string `json:"flight_number"`
		Airline      struct {
			Code string `json:"code"`
			Name string `json:"name"`
		} `json:"airline"`
		Origin struct {
			Code string `json:"code"`
			City string `json:"city"`
		} `json:"origin"`
		DepartureAt     string `json:"departure_at"`
		ArrivalAt       string `json:"arrival_at"`
		DurationMinutes int    `json:"duration_minutes"`
		Price           int64  `json:"price"`
		Capacity        int32  `json:"capacity"`
		AvailableSeats  int32  `json:"available_seats"`
	}
	type flightList struct {
		Data []flight `json:"data"`
		Meta struct {
			Page     int32 `json:"page"`
			PageSize int32 `json:"page_size"`
			Total    int64 `json:"total"`
		} `json:"meta"`
	}

	const createBody = `{
		"flight_number": "GA410", "airline_code": "GA",
		"origin_code": "CGK", "destination_code": "DPS",
		"departure_at": "2027-03-15T06:00:00+07:00",
		"arrival_at": "2027-03-15T08:55:00+08:00",
		"price": 1250000, "capacity": 180
	}`

	code, body := send(t, http.MethodPost, flights, admin, createBody)
	require.Equal(t, http.StatusCreated, code, body)
	var created flight
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	require.NotEmpty(t, created.ID)
	require.Equal(t, "Garuda Indonesia", created.Airline.Name, "reference data is loaded")
	require.Equal(t, "Jakarta", created.Origin.City)
	require.Equal(t, "2027-03-15T06:00:00+07:00", created.DepartureAt, "times use the airport's own offset")
	require.Equal(t, "2027-03-15T08:55:00+08:00", created.ArrivalAt)
	require.Equal(t, 115, created.DurationMinutes)
	require.Equal(t, int32(180), created.AvailableSeats)

	code, body = send(t, http.MethodPost, flights, admin, createBody)
	require.Equal(t, http.StatusConflict, code, body)
	require.Equal(t, "FLIGHT_EXISTS", errorCode(t, body))

	code, fetched := send(t, http.MethodGet, flights+"/"+created.ID, admin, "")
	require.Equal(t, http.StatusOK, code, fetched)
	var got flight
	require.NoError(t, json.Unmarshal([]byte(fetched), &got))
	require.Equal(t, created, got)

	code, body = send(t, http.MethodPatch, flights+"/"+created.ID, admin, `{"price": 900000}`)
	require.Equal(t, http.StatusOK, code, body)
	var updated flight
	require.NoError(t, json.Unmarshal([]byte(body), &updated))
	require.Equal(t, int64(900_000), updated.Price)
	require.Equal(t, created.ID, updated.ID)

	code, body = send(t, http.MethodGet, flights+"?origin=CGK&destination=DPS&date=2027-03-15", admin, "")
	require.Equal(t, http.StatusOK, code, body)
	var list flightList
	require.NoError(t, json.Unmarshal([]byte(body), &list))
	require.Equal(t, int64(1), list.Meta.Total)
	require.Equal(t, int32(20), list.Meta.PageSize, "default page size")
	require.Len(t, list.Data, 1)
	require.Equal(t, updated, list.Data[0])

	code, body = send(t, http.MethodGet, flights+"?date=2027-03-16", admin, "")
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"data":[]`)

	code, body = send(t, http.MethodGet, flights+"?date=15-03-2027", admin, "")
	require.Equal(t, http.StatusBadRequest, code, body)
	require.Equal(t, "VALIDATION_ERROR", errorCode(t, body))

	for _, id := range []string{"6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e", "not-a-uuid"} {
		code, body = send(t, http.MethodGet, flights+"/"+id, admin, "")
		require.Equal(t, http.StatusNotFound, code, body)
		require.Equal(t, "NOT_FOUND", errorCode(t, body))
	}

	require.Equal(t, 2, queryCount(t, `SELECT count(*) FROM outbox WHERE event_type = 'flight.changed'`), "one event for the create, one for the update")

	require.NoError(t, stop())
}

func TestRun_anyoneCanSearchAndViewFlightsWithoutAToken(t *testing.T) {
	truncateFlights(t)
	addr, stop := startService(t)
	admin := signToken(t, testSigningKey, authn.RoleAdmin, time.Now().Add(time.Hour))
	adminFlights := "http://" + addr + "/api/v1/admin/flights"
	publicFlights := "http://" + addr + "/api/v1/flights"

	wib := time.FixedZone("WIB", 7*60*60)
	wita := time.FixedZone("WITA", 8*60*60)
	// Always a week ahead, so the flights are upcoming whenever the test runs.
	day := time.Now().In(wib).AddDate(0, 0, 7)
	at := func(location *time.Location, hour int) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), hour, 0, 0, 0, location)
	}
	date := day.Format(time.DateOnly)

	type flight struct {
		ID             string `json:"id"`
		FlightNumber   string `json:"flight_number"`
		Price          int64  `json:"price"`
		AvailableSeats int32  `json:"available_seats"`
		DepartureAt    string `json:"departure_at"`
	}
	type flightList struct {
		Data []flight `json:"data"`
		Meta struct {
			Page     int32 `json:"page"`
			PageSize int32 `json:"page_size"`
			Total    int64 `json:"total"`
		} `json:"meta"`
	}

	create := func(number, airline, origin, destination string, departure time.Time, price int64) flight {
		body := fmt.Sprintf(`{
			"flight_number": %q, "airline_code": %q,
			"origin_code": %q, "destination_code": %q,
			"departure_at": %q, "arrival_at": %q,
			"price": %d, "capacity": 180
		}`, number, airline, origin, destination,
			departure.Format(time.RFC3339), departure.Add(2*time.Hour).Format(time.RFC3339), price)
		code, response := send(t, http.MethodPost, adminFlights, admin, body)
		require.Equal(t, http.StatusCreated, code, response)
		var created flight
		require.NoError(t, json.Unmarshal([]byte(response), &created))
		return created
	}
	search := func(query string) flightList {
		code, response := send(t, http.MethodGet, publicFlights+"?origin=CGK&destination=DPS&date="+date+query, "", "")
		require.Equal(t, http.StatusOK, code, response)
		var list flightList
		require.NoError(t, json.Unmarshal([]byte(response), &list))
		return list
	}
	numbers := func(list flightList) []string {
		out := make([]string, len(list.Data))
		for i, f := range list.Data {
			out[i] = f.FlightNumber
		}
		return out
	}

	morning := create("GA410", "GA", "CGK", "DPS", at(wib, 6), 1_250_000)
	create("JT100", "JT", "CGK", "DPS", at(wib, 10), 700_000)
	create("GA412", "GA", "CGK", "DPS", at(wib, 22), 900_000)
	create("GA411", "GA", "DPS", "CGK", at(wita, 10), 1_100_000) // the other direction

	t.Run("lists the route's flights by departure time", func(t *testing.T) {
		list := search("")

		require.Equal(t, []string{"GA410", "JT100", "GA412"}, numbers(list))
		require.Equal(t, int64(3), list.Meta.Total)
		require.Equal(t, int32(20), list.Meta.PageSize)
	})
	t.Run("shows the public view without admin fields", func(t *testing.T) {
		code, body := send(t, http.MethodGet, publicFlights+"?origin=CGK&destination=DPS&date="+date, "", "")

		require.Equal(t, http.StatusOK, code, body)
		require.NotContains(t, body, `"capacity"`)
		require.NotContains(t, body, `"created_at"`)
		require.Contains(t, body, `"departure_at":"`+at(wib, 6).Format(time.RFC3339)+`"`, "times use the origin's offset")
	})
	t.Run("filters by airline, price and local departure time", func(t *testing.T) {
		require.Equal(t, []string{"JT100"}, numbers(search("&airlines=JT")))
		require.Equal(t, []string{"JT100", "GA412"}, numbers(search("&max_price=1000000")))
		require.Equal(t, []string{"JT100"}, numbers(search("&departure_from=09:00&departure_to=12:00")))
	})
	t.Run("sorts by price", func(t *testing.T) {
		require.Equal(t, []string{"JT100", "GA412", "GA410"}, numbers(search("&sort=price_asc")))
		require.Equal(t, []string{"GA410", "GA412", "JT100"}, numbers(search("&sort=price_desc")))
	})
	t.Run("pages through the result", func(t *testing.T) {
		list := search("&page_size=2&page=2")

		require.Equal(t, []string{"GA412"}, numbers(list))
		require.Equal(t, int64(3), list.Meta.Total)
	})
	t.Run("hides flights without enough seats for the party", func(t *testing.T) {
		code, body := send(t, http.MethodPatch, adminFlights+"/"+morning.ID, admin, `{"capacity": 2}`)
		require.Equal(t, http.StatusOK, code, body)

		require.Equal(t, []string{"JT100", "GA412"}, numbers(search("&passengers=3")))
		require.Equal(t, []string{"GA410", "JT100", "GA412"}, numbers(search("&passengers=2")))
	})
	t.Run("returns an empty list for a day without flights or an unknown airport", func(t *testing.T) {
		code, body := send(t, http.MethodGet, publicFlights+"?origin=CGK&destination=DPS&date="+day.AddDate(0, 0, 1).Format(time.DateOnly), "", "")
		require.Equal(t, http.StatusOK, code, body)
		require.Contains(t, body, `"data":[]`)

		code, body = send(t, http.MethodGet, publicFlights+"?origin=ZZZ&destination=DPS&date="+date, "", "")
		require.Equal(t, http.StatusOK, code, body)
		require.Contains(t, body, `"data":[]`)
	})
	t.Run("rejects a malformed search", func(t *testing.T) {
		code, body := send(t, http.MethodGet, publicFlights+"?origin=CGK&destination=DPS", "", "")

		require.Equal(t, http.StatusBadRequest, code, body)
		require.Equal(t, "VALIDATION_ERROR", errorCode(t, body))
	})
	t.Run("shows one flight by id", func(t *testing.T) {
		code, body := send(t, http.MethodGet, publicFlights+"/"+morning.ID, "", "")

		require.Equal(t, http.StatusOK, code, body)
		var got flight
		require.NoError(t, json.Unmarshal([]byte(body), &got))
		require.Equal(t, "GA410", got.FlightNumber)
		require.Equal(t, int32(2), got.AvailableSeats, "reflects the new capacity")
		require.NotContains(t, body, `"capacity"`)
	})
	t.Run("reports an unknown flight as not found", func(t *testing.T) {
		for _, id := range []string{"6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e", "not-a-uuid"} {
			code, body := send(t, http.MethodGet, publicFlights+"/"+id, "", "")

			require.Equal(t, http.StatusNotFound, code, body)
			require.Equal(t, "NOT_FOUND", errorCode(t, body))
		}
	})

	require.NoError(t, stop())
}

func TestRun_failsOnInvalidSearchCacheConfig(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		wantErr string
	}{
		{"redis url with the wrong scheme", "REDIS_URL", "http://localhost:6379", "create search cache"},
		{"ttl that is not a duration", "SEARCH_CACHE_TTL", "soon", `SEARCH_CACHE_TTL: invalid duration "soon"`},
		{"ttl of zero", "SEARCH_CACHE_TTL", "0s", "SEARCH_CACHE_TTL must be positive"},
		{"negative ttl", "SEARCH_CACHE_TTL", "-5s", "SEARCH_CACHE_TTL must be positive"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)

			err := run(t.Context(), nil, io.Discard)

			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestRun_exposesNoSearchCacheMetricsWhenRedisIsNotConfigured(t *testing.T) {
	addr, stop := startService(t)

	code, body := fetch(t, "http://"+addr+"/metrics")

	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, body, "catalog_search_cache_total")
	require.NoError(t, stop())
}

// createFlightOnRoute creates a CGK to DPS flight through the admin API and returns its id.
func createFlightOnRoute(t *testing.T, addr, admin, number string, departure time.Time, price int64) string {
	t.Helper()

	body := fmt.Sprintf(`{
		"flight_number": %q, "airline_code": "GA",
		"origin_code": "CGK", "destination_code": "DPS",
		"departure_at": %q, "arrival_at": %q,
		"price": %d, "capacity": 180
	}`, number, departure.Format(time.RFC3339), departure.Add(2*time.Hour).Format(time.RFC3339), price)
	code, response := send(t, http.MethodPost, "http://"+addr+"/api/v1/admin/flights", admin, body)
	require.Equal(t, http.StatusCreated, code, response)

	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal([]byte(response), &created))
	return created.ID
}

// nextWeek returns 06:00 Jakarta time a week ahead and its local date, so the flight is always upcoming.
func nextWeek() (departure time.Time, date string) {
	wib := time.FixedZone("WIB", 7*60*60)
	day := time.Now().In(wib).AddDate(0, 0, 7)
	return time.Date(day.Year(), day.Month(), day.Day(), 6, 0, 0, 0, wib), day.Format(time.DateOnly)
}

func requireMetric(t *testing.T, addr, line string) {
	t.Helper()
	code, body := fetch(t, "http://"+addr+"/metrics")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, line)
}

func TestRun_servesSearchFromRedisAndForgetsTheDayAfterAnAdminWrite(t *testing.T) {
	truncateFlights(t)
	t.Setenv("REDIS_URL", redisURL)
	addr, stop := startService(t)
	admin := signToken(t, testSigningKey, authn.RoleAdmin, time.Now().Add(time.Hour))
	departure, date := nextWeek()
	searchURL := "http://" + addr + "/api/v1/flights?origin=CGK&destination=DPS&date=" + date
	key := "catalog:flights:CGK:DPS:" + date

	redisClient, err := cache.New(redisURL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = redisClient.Close() })
	require.NoError(t, redisClient.Delete(t.Context(), key), "an earlier run may have left the day behind")
	cached := func() bool {
		_, found, err := redisClient.Get(t.Context(), key)
		require.NoError(t, err)
		return found
	}

	id := createFlightOnRoute(t, addr, admin, "GA410", departure, 1_250_000)
	require.False(t, cached(), "creating a flight must not leave a stale day behind")

	code, body := send(t, http.MethodGet, searchURL, "", "")
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"price":1250000`)
	require.True(t, cached(), "the first search stores the day in Redis")
	requireMetric(t, addr, `catalog_search_cache_total{result="miss"} 1`)

	code, body = send(t, http.MethodGet, searchURL, "", "")
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"price":1250000`)
	requireMetric(t, addr, `catalog_search_cache_total{result="hit"} 1`)

	code, body = send(t, http.MethodPatch, "http://"+addr+"/api/v1/admin/flights/"+id, admin, `{"price": 900000}`)
	require.Equal(t, http.StatusOK, code, body)
	require.False(t, cached(), "the admin write forgets the day")

	code, body = send(t, http.MethodGet, searchURL, "", "")
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"price":900000`, "search must not serve the old price")
	require.NotContains(t, body, `"price":1250000`)
	requireMetric(t, addr, `catalog_search_cache_total{result="miss"} 2`)
	requireMetric(t, addr, `catalog_search_cache_total{result="hit"} 1`)

	require.NoError(t, stop())
}

func TestRun_searchStillWorksWhenRedisIsDown(t *testing.T) {
	truncateFlights(t)
	t.Setenv("REDIS_URL", "redis://127.0.0.1:1") // nothing listens there
	addr, stop := startService(t)                // the service starts without Redis
	admin := signToken(t, testSigningKey, authn.RoleAdmin, time.Now().Add(time.Hour))
	departure, date := nextWeek()
	createFlightOnRoute(t, addr, admin, "GA410", departure, 1_250_000)

	code, body := send(t, http.MethodGet, "http://"+addr+"/api/v1/flights?origin=CGK&destination=DPS&date="+date, "", "")

	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"flight_number":"GA410"`)
	requireMetric(t, addr, `catalog_search_cache_total{result="error"} 1`)
	requireMetric(t, addr, `catalog_search_cache_total{result="hit"} 0`)

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

func TestRun_publishesFlightChangesToRabbitMQ(t *testing.T) {
	truncateFlights(t)

	// Messages published before the queue exists are dropped, so wait until the consumer has declared it.
	started := &logSignal{match: "rabbitmq consumer started", ready: make(chan struct{})}
	received := make(chan messaging.Delivery, 10)
	ctx, cancel := context.WithCancel(t.Context())
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		messaging.Consume(ctx, rabbitmqURL, messaging.Subscription{
			Exchange: "catalog.events", RoutingKey: "flight.changed", Queue: "test.catalog-flight-changed",
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
	admin := signToken(t, testSigningKey, authn.RoleAdmin, time.Now().Add(time.Hour))
	departure, _ := nextWeek()
	id := createFlightOnRoute(t, addr, admin, "GA410", departure, 1_250_000)

	var delivery messaging.Delivery
	select {
	case delivery = <-received:
	case <-time.After(10 * time.Second):
		t.Fatal("no flight.changed event reached RabbitMQ")
	}

	require.Equal(t, "flight.changed", delivery.Type)
	var event struct {
		EventID    string    `json:"event_id"`
		Type       string    `json:"type"`
		OccurredAt time.Time `json:"occurred_at"`
		Data       struct {
			FlightID string `json:"flight_id"`
			Price    int64  `json:"price"`
			Capacity int32  `json:"capacity"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(delivery.Body, &event))
	require.Equal(t, delivery.ID, event.EventID)
	require.Equal(t, "flight.changed", event.Type)
	require.False(t, event.OccurredAt.IsZero())
	require.Equal(t, id, event.Data.FlightID)
	require.Equal(t, int64(1_250_000), event.Data.Price)
	require.Equal(t, int32(180), event.Data.Capacity)

	require.Eventually(t, func() bool {
		conn, err := pgx.Connect(t.Context(), os.Getenv("DATABASE_URL"))
		if err != nil {
			return false
		}
		defer func() { _ = conn.Close(t.Context()) }()

		var pending int
		err = conn.QueryRow(t.Context(), `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&pending)
		return err == nil && pending == 0
	}, 5*time.Second, 50*time.Millisecond, "the relay marks the event published")

	require.NoError(t, stop())
}

func TestRun_appliesSeatAvailabilityFromBookingAndForgetsTheCachedDay(t *testing.T) {
	truncateFlights(t)
	t.Setenv("REDIS_URL", redisURL)

	// Messages published before the queue exists are dropped, so wait until the consumer has declared it.
	started := &logSignal{match: "rabbitmq consumer started", ready: make(chan struct{})}
	addr, stop := startServiceWithLogs(t, started)
	select {
	case <-started.ready:
	case <-time.After(10 * time.Second):
		t.Fatal("availability consumer did not start")
	}

	admin := signToken(t, testSigningKey, authn.RoleAdmin, time.Now().Add(time.Hour))
	departure, date := nextWeek()
	searchURL := "http://" + addr + "/api/v1/flights?origin=CGK&destination=DPS&date=" + date
	id := createFlightOnRoute(t, addr, admin, "GA410", departure, 1_250_000)
	require.Equal(t, 180, searchSeats(t.Context(), searchURL), "search caches the day with every seat free")

	publisher := messaging.NewPublisher(rabbitmqURL, "booking.events")
	t.Cleanup(func() { _ = publisher.Close() })
	report := func(version int64, seats int) {
		t.Helper()
		body := fmt.Sprintf(`{"event_id": "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e", "type": "flight.availability_changed",
			"occurred_at": "2026-11-18T10:00:00Z",
			"data": {"flight_id": %q, "availability_version": %d, "available_seats": %d}}`, id, version, seats)
		require.NoError(t, publisher.Publish(t.Context(), []messaging.Message{{
			ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", version), Type: "flight.availability_changed",
			Timestamp: time.Now(), Body: []byte(body),
		}}))
	}

	report(2, 42)
	require.Eventually(t, func() bool {
		return searchSeats(t.Context(), searchURL) == 42
	}, 10*time.Second, 50*time.Millisecond, "the cached day is forgotten, so search shows the new count")

	report(1, 7)
	report(3, 41)
	require.Eventually(t, func() bool {
		return searchSeats(t.Context(), searchURL) == 41
	}, 10*time.Second, 50*time.Millisecond)
	require.Equal(t, 41, queryCount(t, `SELECT available_seats FROM flights`), "the older report was ignored")

	require.NoError(t, stop())
}

// searchSeats returns the free seats of the first flight a search finds, or -1; safe inside require.Eventually.
func searchSeats(ctx context.Context, url string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return -1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1
	}
	defer func() { _ = resp.Body.Close() }()

	var list struct {
		Data []struct {
			AvailableSeats int `json:"available_seats"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&list) != nil || len(list.Data) == 0 {
		return -1
	}
	return list.Data[0].AvailableSeats
}

func TestRun_refusesToLowerCapacityBelowTheSeatsBookingHolds(t *testing.T) {
	truncateFlights(t)
	admin := signToken(t, testSigningKey, authn.RoleAdmin, time.Now().Add(time.Hour))
	booking := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+admin {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"capacity": 180, "held_seats": 5, "sold_seats": 140}`))
	}))
	t.Cleanup(booking.Close)
	t.Setenv("BOOKING_URL", booking.URL)
	addr, stop := startService(t)
	departure, _ := nextWeek()
	id := createFlightOnRoute(t, addr, admin, "GA410", departure, 1_250_000)
	flight := "http://" + addr + "/api/v1/admin/flights/" + id

	code, body := send(t, http.MethodPatch, flight, admin, `{"capacity": 144}`)
	require.Equal(t, http.StatusConflict, code, body)
	require.Equal(t, "CAPACITY_BELOW_SOLD", errorCode(t, body))

	code, body = send(t, http.MethodPatch, flight, admin, `{"capacity": 145}`)
	require.Equal(t, http.StatusOK, code, body)
	require.Contains(t, body, `"capacity":145`)

	require.NoError(t, stop())
}

func TestRun_refusesToLowerCapacityWhileBookingIsDown(t *testing.T) {
	truncateFlights(t)
	t.Setenv("BOOKING_URL", "http://127.0.0.1:1") // nothing listens there
	addr, stop := startService(t)
	admin := signToken(t, testSigningKey, authn.RoleAdmin, time.Now().Add(time.Hour))
	departure, _ := nextWeek()
	flight := "http://" + addr + "/api/v1/admin/flights/" + createFlightOnRoute(t, addr, admin, "GA410", departure, 1_250_000)

	code, body := send(t, http.MethodPatch, flight, admin, `{"capacity": 100}`)
	require.Equal(t, http.StatusServiceUnavailable, code, body)
	require.Equal(t, "BOOKING_UNAVAILABLE", errorCode(t, body))

	code, body = send(t, http.MethodPatch, flight, admin, `{"capacity": 200, "price": 900000}`)
	require.Equal(t, http.StatusOK, code, body, "raising the capacity or changing the price does not need booking")

	require.NoError(t, stop())
}

func truncateHotels(t *testing.T) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer func() { _ = conn.Close(t.Context()) }()

	_, err = conn.Exec(t.Context(), "TRUNCATE room_types, hotels, outbox")
	require.NoError(t, err)
}

const createHotelBody = `{
	"name": "Seminyak Garden Hotel", "city_code": "DPS",
	"address": "Jl. Raya Seminyak 12, Badung", "star_rating": 4,
	"room_types": [
		{"name": "Deluxe Double", "max_guests": 2, "total_rooms": 20, "price_per_night": 850000},
		{"name": "Family Suite", "max_guests": 4, "total_rooms": 5, "price_per_night": 1600000}
	]
}`

type hotelRoom struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	PricePerNight int64  `json:"price_per_night"`
	TotalPrice    int64  `json:"total_price"`
}

type hotel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	City struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"city"`
	LowestPricePerNight int64       `json:"lowest_price_per_night"`
	RoomTypes           []hotelRoom `json:"room_types"`
}

func createHotel(t *testing.T, addr, admin string) hotel {
	t.Helper()
	code, body := send(t, http.MethodPost, "http://"+addr+"/api/v1/admin/hotels", admin, createHotelBody)
	require.Equal(t, http.StatusCreated, code, body)

	var created hotel
	require.NoError(t, json.Unmarshal([]byte(body), &created))
	return created
}

func roomNames(h hotel) []string {
	names := make([]string, len(h.RoomTypes))
	for i, room := range h.RoomTypes {
		names[i] = room.Name
	}
	return names
}

func TestRun_adminCreatesAHotelAndAnyoneCanSearchIt(t *testing.T) {
	truncateHotels(t)
	addr, stop := startService(t)
	admin := signToken(t, testSigningKey, authn.RoleAdmin, time.Now().Add(time.Hour))
	adminHotels := "http://" + addr + "/api/v1/admin/hotels"

	created := createHotel(t, addr, admin)
	require.NotEmpty(t, created.ID)
	require.Equal(t, "Denpasar", created.City.Name, "the city comes from the migrated reference data")
	require.Equal(t, []string{"Deluxe Double", "Family Suite"}, roomNames(created))
	require.NotEmpty(t, created.RoomTypes[0].ID)

	code, body := send(t, http.MethodPost, adminHotels, admin, createHotelBody)
	require.Equal(t, http.StatusConflict, code, body)
	require.Equal(t, "HOTEL_EXISTS", errorCode(t, body))

	code, body = send(t, http.MethodPost, adminHotels, admin, strings.Replace(createHotelBody, `"DPS"`, `"ZZZ"`, 1))
	require.Equal(t, http.StatusBadRequest, code, body)
	require.Equal(t, "VALIDATION_ERROR", errorCode(t, body))
	require.Contains(t, body, `"field":"city_code"`)

	require.Equal(t, 2, queryCount(t, `SELECT count(*) FROM outbox WHERE event_type = 'room_type.created'`), "one event per room type")

	// Always a week ahead in UTC, so the check-in is never in the past.
	checkIn := time.Now().UTC().AddDate(0, 0, 7)
	stay := fmt.Sprintf("check_in=%s&check_out=%s", checkIn.Format(time.DateOnly), checkIn.AddDate(0, 0, 2).Format(time.DateOnly))
	search := func(query string) (int, string) {
		t.Helper()
		return send(t, http.MethodGet, "http://"+addr+"/api/v1/hotels?"+query, "", "")
	}
	found := func(query string) []hotel {
		t.Helper()
		code, body := search(query)
		require.Equal(t, http.StatusOK, code, body)
		var list struct {
			Data []hotel `json:"data"`
		}
		require.NoError(t, json.Unmarshal([]byte(body), &list))
		return list.Data
	}

	t.Run("finds the hotel without a token, priced for the whole stay", func(t *testing.T) {
		code, body := search("city=DPS&" + stay)
		require.Equal(t, http.StatusOK, code, body)
		require.NotContains(t, body, `"total_rooms"`, "the public view hides the inventory")

		hotels := found("city=DPS&" + stay)
		require.Len(t, hotels, 1)
		require.Equal(t, created.ID, hotels[0].ID)
		require.Equal(t, int64(850_000), hotels[0].LowestPricePerNight)
		require.Equal(t, int64(1_700_000), hotels[0].RoomTypes[0].TotalPrice)
	})
	t.Run("leaves out room types too small for the guests", func(t *testing.T) {
		hotels := found("city=DPS&guests=3&" + stay)

		require.Len(t, hotels, 1)
		require.Equal(t, []string{"Family Suite"}, roomNames(hotels[0]))
	})
	t.Run("returns an empty list when no room fits or the city has no hotels", func(t *testing.T) {
		for _, query := range []string{"city=DPS&guests=5&" + stay, "city=JKT&" + stay, "city=ZZZ&" + stay} {
			code, body := search(query)
			require.Equal(t, http.StatusOK, code, body)
			require.Contains(t, body, `"data":[]`, query)
		}
	})
	t.Run("rejects a check-in in the past", func(t *testing.T) {
		yesterday := time.Now().UTC().AddDate(0, 0, -1)
		code, body := search(fmt.Sprintf("city=DPS&check_in=%s&check_out=%s",
			yesterday.Format(time.DateOnly), yesterday.AddDate(0, 0, 2).Format(time.DateOnly)))

		require.Equal(t, http.StatusBadRequest, code, body)
		require.Contains(t, body, `"field":"check_in"`)
	})
	t.Run("rejects a malformed search", func(t *testing.T) {
		code, body := search("city=DPS")

		require.Equal(t, http.StatusBadRequest, code, body)
		require.Equal(t, "VALIDATION_ERROR", errorCode(t, body))
	})

	require.NoError(t, stop())
}

func TestRun_publishesRoomTypeCreatedToRabbitMQ(t *testing.T) {
	truncateHotels(t)

	// Messages published before the queue exists are dropped, so wait until the consumer has declared it.
	started := &logSignal{match: "rabbitmq consumer started", ready: make(chan struct{})}
	received := make(chan messaging.Delivery, 10)
	ctx, cancel := context.WithCancel(t.Context())
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		messaging.Consume(ctx, rabbitmqURL, messaging.Subscription{
			Exchange: "catalog.events", RoutingKey: "room_type.created", Queue: "test.catalog-room-type-created",
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
	admin := signToken(t, testSigningKey, authn.RoleAdmin, time.Now().Add(time.Hour))
	created := createHotel(t, addr, admin)

	type roomTypeCreated struct {
		Type string `json:"type"`
		Data struct {
			RoomTypeID    string `json:"room_type_id"`
			HotelID       string `json:"hotel_id"`
			HotelName     string `json:"hotel_name"`
			CityCode      string `json:"city_code"`
			TotalRooms    int32  `json:"total_rooms"`
			PricePerNight int64  `json:"price_per_night"`
		} `json:"data"`
	}
	rooms := map[string]roomTypeCreated{}
	for range created.RoomTypes {
		select {
		case delivery := <-received:
			require.Equal(t, "room_type.created", delivery.Type)
			var event roomTypeCreated
			require.NoError(t, json.Unmarshal(delivery.Body, &event))
			rooms[event.Data.RoomTypeID] = event
		case <-time.After(10 * time.Second):
			t.Fatal("not every room_type.created event reached RabbitMQ")
		}
	}

	for _, room := range created.RoomTypes {
		event, ok := rooms[room.ID]
		require.True(t, ok, "no event for room type %s", room.ID)
		require.Equal(t, "room_type.created", event.Type)
		require.Equal(t, created.ID, event.Data.HotelID)
		require.Equal(t, "Seminyak Garden Hotel", event.Data.HotelName)
		require.Equal(t, "DPS", event.Data.CityCode)
		require.Equal(t, room.PricePerNight, event.Data.PricePerNight)
	}
	require.Equal(t, int32(20), rooms[created.RoomTypes[0].ID].Data.TotalRooms)

	require.NoError(t, stop())
}
