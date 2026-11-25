package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeAPI is the part of the platform the seed talks to: a login and admin
// endpoints that create flights and hotels and refuse duplicates.
type fakeAPI struct {
	server *httptest.Server

	mu          sync.Mutex
	logins      []map[string]string
	requests    int
	flights     map[string]flightSpec
	failCreates bool // flights only
	rejectLogin bool
	onCreate    func(created int) // called after each stored flight, under no lock

	// createDelay slows every create down, so that requests pile up to the
	// number of workers and a test can count how many ran at once.
	createDelay time.Duration
	inFlight    int
	maxInFlight int

	hotels        map[string]hotelSpec
	hotelRequests int
	failHotels    bool
}

func newFakeAPI(t *testing.T) *fakeAPI {
	t.Helper()
	api := &fakeAPI{flights: make(map[string]flightSpec), hotels: make(map[string]hotelSpec)}
	api.server = httptest.NewServer(http.HandlerFunc(api.serve))
	t.Cleanup(api.server.Close)
	return api
}

func (a *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.requests++
	a.mu.Unlock()

	switch r.URL.Path {
	case loginPath:
		var credentials map[string]string
		_ = json.NewDecoder(r.Body).Decode(&credentials)
		a.mu.Lock()
		a.logins = append(a.logins, credentials)
		rejected := a.rejectLogin
		a.mu.Unlock()
		if rejected {
			writeJSON(w, http.StatusUnauthorized, `{"error":{"code":"INVALID_CREDENTIALS","message":"invalid email or password"}}`)
			return
		}
		writeJSON(w, http.StatusOK, `{"access_token":"admin-token"}`)
	case createFlightPath:
		a.create(w, r)
	case createHotelPath:
		a.createHotel(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (a *fakeAPI) create(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer admin-token" {
		writeJSON(w, http.StatusUnauthorized, `{"error":{"code":"UNAUTHORIZED","message":"missing token"}}`)
		return
	}
	if a.failCreates {
		writeJSON(w, http.StatusInternalServerError, `{"error":{"code":"INTERNAL_ERROR","message":"internal server error"}}`)
		return
	}

	a.enter()
	defer a.leave()

	var spec flightSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeJSON(w, http.StatusBadRequest, `{"error":{"code":"VALIDATION_ERROR","message":"invalid request body"}}`)
		return
	}
	key := spec.FlightNumber + "/" + spec.AirlineCode + "/" + spec.DepartureAt.UTC().Format(time.RFC3339)

	a.mu.Lock()
	_, exists := a.flights[key]
	if !exists {
		a.flights[key] = spec
	}
	stored := len(a.flights)
	a.mu.Unlock()

	if exists {
		writeJSON(w, http.StatusConflict, `{"error":{"code":"FLIGHT_EXISTS","message":"flight already exists for this departure time"}}`)
		return
	}
	if a.onCreate != nil {
		a.onCreate(stored)
	}
	writeJSON(w, http.StatusCreated, `{}`)
}

func (a *fakeAPI) createHotel(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer admin-token" {
		writeJSON(w, http.StatusUnauthorized, `{"error":{"code":"UNAUTHORIZED","message":"missing token"}}`)
		return
	}

	a.mu.Lock()
	a.hotelRequests++
	failing := a.failHotels
	a.mu.Unlock()
	if failing {
		writeJSON(w, http.StatusInternalServerError, `{"error":{"code":"INTERNAL_ERROR","message":"internal server error"}}`)
		return
	}

	var spec hotelSpec
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeJSON(w, http.StatusBadRequest, `{"error":{"code":"VALIDATION_ERROR","message":"invalid request body"}}`)
		return
	}
	key := spec.CityCode + "/" + spec.Name

	a.mu.Lock()
	_, exists := a.hotels[key]
	if !exists {
		a.hotels[key] = spec
	}
	a.mu.Unlock()

	if exists {
		writeJSON(w, http.StatusConflict, `{"error":{"code":"HOTEL_EXISTS","message":"hotel already exists in this city"}}`)
		return
	}
	writeJSON(w, http.StatusCreated, `{}`)
}

func (a *fakeAPI) storedHotels() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.hotels)
}

func (a *fakeAPI) hotelRequestCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.hotelRequests
}

// enter counts a create as in flight and holds it for createDelay.
func (a *fakeAPI) enter() {
	a.mu.Lock()
	a.inFlight++
	a.maxInFlight = max(a.maxInFlight, a.inFlight)
	a.mu.Unlock()

	time.Sleep(a.createDelay)
}

func (a *fakeAPI) leave() {
	a.mu.Lock()
	a.inFlight--
	a.mu.Unlock()
}

func (a *fakeAPI) peakInFlight() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.maxInFlight
}

func (a *fakeAPI) storedFlights() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.flights)
}

func (a *fakeAPI) requestCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.requests
}

func adminEnv(key string) string {
	return map[string]string{"ADMIN_EMAIL": "admin@example.test", "ADMIN_PASSWORD": "s3cret"}[key]
}

// An evening in Jakarta, so "tomorrow" is the 15th there.
var fixedNow = time.Date(2026, 11, 14, 15, 55, 0, 0, time.UTC)

func TestExecute_seedsTheRequestedDaysStartingTomorrow(t *testing.T) {
	api := newFakeAPI(t)
	var out bytes.Buffer

	err := execute(context.Background(), []string{"-base-url", api.server.URL, "-days", "2", "-workers", "3"}, adminEnv, fixedNow, &out)

	require.NoError(t, err)
	require.Equal(t, 2*flightsPerDay, api.storedFlights())
	days := make(map[string]bool)
	for _, spec := range api.flights {
		days[localDate(t, spec.DepartureAt, spec.OriginCode)] = true
	}
	require.Equal(t, map[string]bool{"2026-11-15": true, "2026-11-16": true}, days)
	require.Equal(t, []map[string]string{{"email": "admin@example.test", "password": "s3cret"}}, api.logins)
	require.Contains(t, out.String(), "flights=1212")
	require.Contains(t, out.String(), "created=1212 existing=0 failed=0")
	require.Equal(t, hotelsPerSeed, api.storedHotels())
	require.Contains(t, out.String(), "hotels=33")
}

func TestExecute_tomorrowIsCountedInJakartaTime(t *testing.T) {
	api := newFakeAPI(t)
	// 01:00 on the 15th in Jakarta, still the 14th in UTC.
	now := time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC)

	err := execute(context.Background(), []string{"-base-url", api.server.URL, "-days", "1"}, adminEnv, now, &bytes.Buffer{})

	require.NoError(t, err)
	for _, spec := range api.flights {
		require.Equal(t, "2026-11-16", localDate(t, spec.DepartureAt, spec.OriginCode))
	}
}

func TestExecute_runningTwiceCreatesNothingNewAndSucceeds(t *testing.T) {
	api := newFakeAPI(t)
	args := []string{"-base-url", api.server.URL, "-days", "1"}
	require.NoError(t, execute(context.Background(), args, adminEnv, fixedNow, &bytes.Buffer{}))
	var out bytes.Buffer

	err := execute(context.Background(), args, adminEnv, fixedNow, &out)

	require.NoError(t, err)
	require.Equal(t, flightsPerDay, api.storedFlights())
	require.Contains(t, out.String(), "created=0 existing=606 failed=0")
	require.Equal(t, hotelsPerSeed, api.storedHotels())
	require.Contains(t, out.String(), `msg="hotels seeded" created=0 existing=33 failed=0`)
}

func TestExecute_aLaterRunAddsOnlyTheNewDays(t *testing.T) {
	api := newFakeAPI(t)
	args := []string{"-base-url", api.server.URL, "-days", "2"}
	require.NoError(t, execute(context.Background(), args, adminEnv, fixedNow, &bytes.Buffer{}))
	var out bytes.Buffer

	err := execute(context.Background(), args, adminEnv, fixedNow.Add(24*time.Hour), &out)

	require.NoError(t, err)
	require.Equal(t, 3*flightsPerDay, api.storedFlights())
	require.Contains(t, out.String(), "created=606 existing=606 failed=0")
}

func TestExecute_checksTheInputBeforeTouchingTheNetwork(t *testing.T) {
	tests := map[string]struct {
		args []string
		env  func(string) string
		want string
	}{
		"no days":           {[]string{"-days", "0"}, adminEnv, "-days must be at least 1"},
		"no workers":        {[]string{"-workers", "0"}, adminEnv, "-workers must be at least 1"},
		"no scheme":         {[]string{"-base-url", "localhost:8080"}, adminEnv, "-base-url must look like http://host:port"},
		"ftp":               {[]string{"-base-url", "ftp://localhost"}, adminEnv, "-base-url must look like http://host:port"},
		"no host":           {[]string{"-base-url", "http://"}, adminEnv, "-base-url must look like http://host:port"},
		"unknown flag":      {[]string{"-nope"}, adminEnv, "flag provided but not defined"},
		"no admin email":    {nil, func(k string) string { return map[string]string{"ADMIN_PASSWORD": "x"}[k] }, "ADMIN_EMAIL must be set"},
		"no admin password": {nil, func(k string) string { return map[string]string{"ADMIN_EMAIL": "a@b.c"}[k] }, "ADMIN_PASSWORD must be set"},
		"no admin at all":   {nil, func(string) string { return "" }, "ADMIN_EMAIL and ADMIN_PASSWORD must be set"},
	}
	for name, tt := range tests {
		api := newFakeAPI(t)
		args := append([]string{"-base-url", api.server.URL}, tt.args...) // a later -base-url wins
		err := execute(context.Background(), args, tt.env, fixedNow, &bytes.Buffer{})

		require.ErrorContains(t, err, tt.want, name)
		require.Equal(t, 0, api.requestCount(), name)
	}
}

func TestExecute_helpIsNotAnError(t *testing.T) {
	var out bytes.Buffer

	err := execute(context.Background(), []string{"-h"}, adminEnv, fixedNow, &out)

	require.NoError(t, err)
	require.Contains(t, out.String(), "-base-url")
	require.Contains(t, out.String(), "-days")
	require.Contains(t, out.String(), "-workers")
}

func TestParseConfig_hasUsefulDefaults(t *testing.T) {
	cfg, err := parseConfig(nil, &bytes.Buffer{})

	require.NoError(t, err)
	require.Equal(t, config{baseURL: "http://localhost:8080", days: 14, workers: 8}, cfg)
}

func TestParseConfig_reportsHelpAsFlagErrHelp(t *testing.T) {
	_, err := parseConfig([]string{"-h"}, &bytes.Buffer{})

	require.ErrorIs(t, err, flag.ErrHelp)
}

func TestExecute_stopsWhenTheAdminCannotLogIn(t *testing.T) {
	api := newFakeAPI(t)
	api.rejectLogin = true

	err := execute(context.Background(), []string{"-base-url", api.server.URL}, adminEnv, fixedNow, &bytes.Buffer{})

	require.ErrorContains(t, err, "log in as admin@example.test")
	require.ErrorContains(t, err, "INVALID_CREDENTIALS")
	require.Equal(t, 1, api.requestCount(), "no flight is sent without a token")
}

func TestExecute_failsWhenTheGatewayIsNotThere(t *testing.T) {
	api := newFakeAPI(t)
	url := api.server.URL
	api.server.Close()

	err := execute(context.Background(), []string{"-base-url", url}, adminEnv, fixedNow, &bytes.Buffer{})

	require.ErrorContains(t, err, "log in as admin@example.test")
}

func TestExecute_givesUpWhenEveryCreateFails(t *testing.T) {
	api := newFakeAPI(t)
	api.failCreates = true
	var out bytes.Buffer

	err := execute(context.Background(), []string{"-base-url", api.server.URL, "-workers", "2"}, adminEnv, fixedNow, &out)

	require.ErrorContains(t, err, "stopped after")
	require.ErrorContains(t, err, "of 8484 flights were tried")
	require.Less(t, api.requestCount(), 50, "the run stops instead of sending all 8484")
	require.Contains(t, out.String(), "INTERNAL_ERROR")
	require.Zero(t, api.hotelRequestCount(), "a broken run does not go on to the hotels")
}

func TestExecute_reportsAnInterruptionWithTheProgressMade(t *testing.T) {
	api := newFakeAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.onCreate = func(created int) {
		if created == 100 {
			cancel()
		}
	}
	var out bytes.Buffer

	err := execute(ctx, []string{"-base-url", api.server.URL, "-workers", "1"}, adminEnv, fixedNow, &out)

	require.ErrorContains(t, err, "interrupted after")
	require.ErrorContains(t, err, "of 8484 flights", "the progress is the flights', not the hotels' that never started")
	require.Less(t, api.storedFlights(), 200)
	require.Contains(t, out.String(), "failed=0", "the interruption is not counted as a failure")
	require.Zero(t, api.hotelRequestCount())
}

func TestExecute_failuresBelowTheLimitStillFailTheCommand(t *testing.T) {
	api := newFakeAPI(t)
	var once sync.Once
	api.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == createFlightPath {
			failed := false
			once.Do(func() { failed = true })
			if failed {
				writeJSON(w, http.StatusBadRequest, `{"error":{"code":"VALIDATION_ERROR","message":"request is invalid","details":[{"field":"price","message":"must be positive"}]}}`)
				return
			}
		}
		api.serve(w, r)
	})

	err := execute(context.Background(), []string{"-base-url", api.server.URL, "-days", "1"}, adminEnv, fixedNow, &bytes.Buffer{})

	require.EqualError(t, err, "1 of 606 flights failed")
	require.Equal(t, flightsPerDay-1, api.storedFlights())
	require.Equal(t, hotelsPerSeed, api.storedHotels(), "a few failed flights do not keep the hotels out")
}

func TestExecute_sendsRequestsFromTheRequestedNumberOfWorkers(t *testing.T) {
	api := newFakeAPI(t)
	api.createDelay = 5 * time.Millisecond

	err := execute(context.Background(), []string{"-base-url", api.server.URL, "-days", "1", "-workers", "4"}, adminEnv, fixedNow, &bytes.Buffer{})

	require.NoError(t, err)
	require.Equal(t, 4, api.peakInFlight())
}

func TestExecute_seedsTheHotelsAfterTheFlights(t *testing.T) {
	api := newFakeAPI(t)
	var out bytes.Buffer

	err := execute(context.Background(), []string{"-base-url", api.server.URL, "-days", "1"}, adminEnv, fixedNow, &out)

	require.NoError(t, err)
	require.Equal(t, hotelsPerSeed, api.storedHotels())
	require.Contains(t, out.String(), `msg="flights seeded" created=606 existing=0 failed=0`)
	require.Contains(t, out.String(), `msg="hotels seeded" created=33 existing=0 failed=0`)
	require.Less(t, bytes.Index(out.Bytes(), []byte("flights seeded")), bytes.Index(out.Bytes(), []byte("hotels seeded")))
}

func TestExecute_failedHotelsFailTheCommand(t *testing.T) {
	api := newFakeAPI(t)
	api.failHotels = true
	var out bytes.Buffer

	err := execute(context.Background(), []string{"-base-url", api.server.URL, "-days", "1"}, adminEnv, fixedNow, &out)

	require.EqualError(t, err, "33 of 33 hotels failed")
	require.Equal(t, flightsPerDay, api.storedFlights(), "the flights are already in")
	require.Contains(t, out.String(), "create hotel failed")
}

func TestExecute_reportsAnInterruptionDuringTheHotels(t *testing.T) {
	api := newFakeAPI(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	api.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == createHotelPath && api.storedHotels() == 5 {
			cancel()
		}
		api.serve(w, r)
	})

	err := execute(ctx, []string{"-base-url", api.server.URL, "-days", "1"}, adminEnv, fixedNow, &bytes.Buffer{})

	require.ErrorContains(t, err, "interrupted after")
	require.ErrorContains(t, err, "of 33 hotels")
	require.Less(t, api.storedHotels(), hotelsPerSeed)
}
