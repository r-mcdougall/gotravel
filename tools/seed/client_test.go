package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var sampleSpec = flightSpec{
	FlightNumber:    "GA100",
	AirlineCode:     "GA",
	OriginCode:      "CGK",
	DestinationCode: "DPS",
	DepartureAt:     time.Date(2026, 11, 20, 0, 0, 0, 0, time.FixedZone("WIB", 7*3600)),
	ArrivalAt:       time.Date(2026, 11, 20, 2, 55, 0, 0, time.FixedZone("WITA", 8*3600)),
	Price:           1_115_000,
	Capacity:        180,
}

// newTestClient starts a server that answers every request with the handler.
func newTestClient(t *testing.T, handler http.HandlerFunc) *client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return newClient(server.URL, server.Client())
}

func writeJSON(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func loggedInClient(t *testing.T, handler http.HandlerFunc) *client {
	t.Helper()
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == loginPath {
			writeJSON(w, http.StatusOK, `{"access_token":"token-1"}`)
			return
		}
		handler(w, r)
	})
	require.NoError(t, c.login(context.Background(), "admin@example.test", "secret"))
	return c
}

func TestClient_loginSendsTheCredentialsAndKeepsTheToken(t *testing.T) {
	var got map[string]string
	var method, contentType string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		method, contentType = r.Method, r.Header.Get("Content-Type")
		require.Equal(t, loginPath, r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		writeJSON(w, http.StatusOK, `{"access_token":"token-1","token_type":"Bearer"}`)
	})

	require.NoError(t, c.login(context.Background(), "admin@example.test", "secret"))

	require.Equal(t, http.MethodPost, method)
	require.Equal(t, "application/json", contentType)
	require.Equal(t, map[string]string{"email": "admin@example.test", "password": "secret"}, got)
	require.Equal(t, "token-1", c.token)
}

func TestClient_loginExplainsARejectionWithoutLeakingThePassword(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusUnauthorized, `{"error":{"code":"INVALID_CREDENTIALS","message":"invalid email or password"}}`)
	})

	err := c.login(context.Background(), "admin@example.test", "secret")

	require.ErrorContains(t, err, "log in as admin@example.test")
	require.ErrorContains(t, err, "HTTP 401 INVALID_CREDENTIALS: invalid email or password")
	require.NotContains(t, err.Error(), "secret")
	require.Equal(t, "", c.token)
}

func TestClient_loginFailsWhenTheResponseHasNoToken(t *testing.T) {
	for name, body := range map[string]string{
		"empty token": `{"access_token":""}`,
		"no token":    `{}`,
		"not json":    `<html>`,
	} {
		c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusOK, body)
		})

		require.ErrorContains(t, c.login(context.Background(), "a@b.c", "x"), "no access token", name)
		require.Equal(t, "", c.token, name)
	}
}

func TestClient_createFlightSendsTheSpecWithTheAdminToken(t *testing.T) {
	var auth, path string
	var got map[string]any
	c := loggedInClient(t, func(w http.ResponseWriter, r *http.Request) {
		auth, path = r.Header.Get("Authorization"), r.URL.Path
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		writeJSON(w, http.StatusCreated, `{"id":"1"}`)
	})

	result, err := c.createFlight(context.Background(), sampleSpec)

	require.NoError(t, err)
	require.Equal(t, outcomeCreated, result)
	require.Equal(t, "Bearer token-1", auth)
	require.Equal(t, createFlightPath, path)
	require.Equal(t, map[string]any{
		"flight_number":    "GA100",
		"airline_code":     "GA",
		"origin_code":      "CGK",
		"destination_code": "DPS",
		"departure_at":     "2026-11-20T00:00:00+07:00",
		"arrival_at":       "2026-11-20T02:55:00+08:00",
		"price":            float64(1_115_000),
		"capacity":         float64(180),
	}, got)
}

func TestClient_aFlightThatAlreadyExistsIsNotAnError(t *testing.T) {
	c := loggedInClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusConflict, `{"error":{"code":"FLIGHT_EXISTS","message":"flight already exists for this departure time"}}`)
	})

	result, err := c.createFlight(context.Background(), sampleSpec)

	require.NoError(t, err)
	require.Equal(t, outcomeExisting, result)
}

func TestClient_otherConflictsAreErrors(t *testing.T) {
	c := loggedInClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusConflict, `{"error":{"code":"SOMETHING_ELSE","message":"no"}}`)
	})

	_, err := c.createFlight(context.Background(), sampleSpec)

	require.ErrorContains(t, err, "HTTP 409 SOMETHING_ELSE: no")
}

func TestClient_aValidationErrorNamesTheFlightAndTheFields(t *testing.T) {
	c := loggedInClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusBadRequest, `{"error":{"code":"VALIDATION_ERROR","message":"request is invalid","details":[`+
			`{"field":"price","message":"must be positive"},{"field":"capacity","message":"must be at least 1"}]}}`)
	})

	_, err := c.createFlight(context.Background(), sampleSpec)

	require.ErrorContains(t, err, "create flight GA100 departing 2026-11-20T00:00:00+07:00")
	require.ErrorContains(t, err, "HTTP 400 VALIDATION_ERROR: request is invalid; price: must be positive; capacity: must be at least 1")
}

func TestClient_unexpectedResponsesAreDescribedShortly(t *testing.T) {
	const prefix = "create flight GA100 departing 2026-11-20T00:00:00+07:00: "
	tests := map[string]struct {
		status int
		body   string
		want   string
	}{
		"plain text":                     {http.StatusBadGateway, "bad gateway\n", "HTTP 502: bad gateway"},
		"empty body":                     {http.StatusServiceUnavailable, "", "HTTP 503 with no body"},
		"unknown json":                   {http.StatusTeapot, `{"hello":"world"}`, `HTTP 418: {"hello":"world"}`},
		"long body is cut":               {http.StatusInternalServerError, strings.Repeat("x", 500), "HTTP 500: " + strings.Repeat("x", maxErrorTextBytes) + "..."},
		"success that is not a creation": {http.StatusOK, "{}", "HTTP 200: {}"},
	}
	for name, tt := range tests {
		c := loggedInClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tt.status)
			_, _ = io.WriteString(w, tt.body)
		})

		_, err := c.createFlight(context.Background(), sampleSpec)

		require.EqualError(t, err, prefix+tt.want, name)
	}
}

func TestClient_reportsAServerThatIsNotThere(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	c := newClient(server.URL, server.Client())
	server.Close()

	_, err := c.createFlight(context.Background(), sampleSpec)

	require.ErrorContains(t, err, "create flight GA100")
}

func TestClient_stopsWhenTheContextIsCancelled(t *testing.T) {
	c := loggedInClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusCreated, `{}`)
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := c.createFlight(ctx, sampleSpec)

	require.ErrorIs(t, err, context.Canceled)
}

func TestClient_ignoresATrailingSlashInTheBaseURL(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		writeJSON(w, http.StatusCreated, `{}`)
	}))
	t.Cleanup(server.Close)
	c := newClient(server.URL+"/", server.Client())

	_, err := c.createFlight(context.Background(), sampleSpec)

	require.NoError(t, err)
	require.Equal(t, createFlightPath, path)
}

func TestClient_sendsNoAuthorizationHeaderBeforeLogin(t *testing.T) {
	var header string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("Authorization")
		writeJSON(w, http.StatusOK, `{"access_token":"t"}`)
	})

	require.NoError(t, c.login(context.Background(), "a@b.c", "x"))

	require.Equal(t, "", header)
}

func TestClient_loginNeverPutsThePasswordInAConnectionError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	c := newClient(server.URL, server.Client())
	server.Close()

	err := c.login(context.Background(), "admin@example.test", "secret")

	require.ErrorContains(t, err, "log in as admin@example.test")
	require.NotContains(t, err.Error(), "secret")
}
