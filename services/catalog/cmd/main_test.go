package main

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/internal/platform/dbtest"
)

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

	for key, value := range map[string]string{"DATABASE_URL": url, "JWT_PUBLIC_KEY_FILE": keyFile} {
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
	addr = freeAddr(t)
	t.Setenv("HTTP_ADDR", addr)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, io.Discard) }()

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
		   AND table_name IN ('airlines', 'airports', 'flights', 'outbox')`)
	require.Equal(t, 4, tables)

	require.NoError(t, stop())
}

func TestRun_loadsReferenceDataOnStart(t *testing.T) {
	_, stop := startService(t)

	require.Equal(t, 8, queryCount(t, `SELECT count(*) FROM airlines`))
	require.Equal(t, 11, queryCount(t, `SELECT count(*) FROM airports`))

	require.NoError(t, stop())
}

func TestRun_failsWithoutDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "DATABASE_URL is required")
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
