package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeCreator answers each flight with the function it was given and records
// every call.
type fakeCreator struct {
	answer func(call int, spec flightSpec) (outcome, error)

	// answerWithContext replaces answer for tests that need the run's context.
	answerWithContext func(ctx context.Context, call int, spec flightSpec) (outcome, error)

	mu    sync.Mutex
	calls []flightSpec
}

func (f *fakeCreator) createFlight(ctx context.Context, spec flightSpec) (outcome, error) {
	f.mu.Lock()
	f.calls = append(f.calls, spec)
	call := len(f.calls)
	f.mu.Unlock()
	if f.answerWithContext != nil {
		return f.answerWithContext(ctx, call, spec)
	}
	return f.answer(call, spec)
}

func (f *fakeCreator) called() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func numberedFlights(n int) []flightSpec {
	flights := make([]flightSpec, n)
	for i := range flights {
		flights[i] = flightSpec{FlightNumber: "GA" + strconv.Itoa(i)}
	}
	return flights
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func alwaysCreated(int, flightSpec) (outcome, error) { return outcomeCreated, nil }

func TestRun_countsCreatedExistingAndFailedFlights(t *testing.T) {
	creator := &fakeCreator{answer: func(_ int, spec flightSpec) (outcome, error) {
		switch spec.FlightNumber {
		case "GA1", "GA4":
			return outcomeExisting, nil
		case "GA7":
			return 0, errors.New("boom")
		default:
			return outcomeCreated, nil
		}
	}}

	got := run(context.Background(), creator, numberedFlights(10), 3, quietLogger())

	require.Equal(t, summary{created: 7, existing: 2, failed: 1}, got)
}

func TestRun_triesEveryFlightExactlyOnce(t *testing.T) {
	creator := &fakeCreator{answer: alwaysCreated}

	got := run(context.Background(), creator, numberedFlights(500), 8, quietLogger())

	require.Equal(t, 500, got.created)
	seen := make(map[string]int)
	for _, spec := range creator.calls {
		seen[spec.FlightNumber]++
	}
	require.Len(t, seen, 500)
	for number, times := range seen {
		require.Equal(t, 1, times, number)
	}
}

func TestRun_doesNothingForAnEmptyPlan(t *testing.T) {
	got := run(context.Background(), &fakeCreator{answer: alwaysCreated}, nil, 4, quietLogger())

	require.Equal(t, summary{}, got)
}

func TestRun_stillWorksWithFewerThanOneWorker(t *testing.T) {
	creator := &fakeCreator{answer: alwaysCreated}

	got := run(context.Background(), creator, numberedFlights(5), 0, quietLogger())

	require.Equal(t, 5, got.created)
}

func TestRun_givesUpAfterTooManyFailures(t *testing.T) {
	const workers = 4
	creator := &fakeCreator{answer: func(int, flightSpec) (outcome, error) { return 0, errors.New("boom") }}

	got := run(context.Background(), creator, numberedFlights(1000), workers, quietLogger())

	require.True(t, got.aborted)
	require.GreaterOrEqual(t, got.failed, maxFailures)
	require.LessOrEqual(t, creator.called(), maxFailures+workers, "no new flight starts once the limit is reached")
	require.Equal(t, creator.called(), got.failed)
}

func TestRun_failuresBelowTheLimitDoNotStopTheRun(t *testing.T) {
	creator := &fakeCreator{answer: func(call int, _ flightSpec) (outcome, error) {
		if call%2 == 0 {
			return 0, errors.New("boom")
		}
		return outcomeCreated, nil
	}}

	got := run(context.Background(), creator, numberedFlights(2*(maxFailures-1)), 1, quietLogger())

	require.Equal(t, summary{created: maxFailures - 1, failed: maxFailures - 1}, got)
}

func TestRun_aRunThatEndsOnItsLastFailureIsNotAborted(t *testing.T) {
	creator := &fakeCreator{answer: func(int, flightSpec) (outcome, error) { return 0, errors.New("boom") }}

	got := run(context.Background(), creator, numberedFlights(maxFailures), 1, quietLogger())

	require.Equal(t, maxFailures, got.failed)
	require.False(t, got.aborted, "every flight was tried")
}

func TestRun_stopsQuietlyWhenTheContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	creator := &fakeCreator{answer: func(call int, _ flightSpec) (outcome, error) {
		if call == 3 {
			cancel()
			return 0, ctx.Err()
		}
		return outcomeCreated, nil
	}}

	got := run(ctx, creator, numberedFlights(100), 1, quietLogger())

	require.Equal(t, summary{created: 2}, got, "the cut-short flight is not a failure and the run is not aborted")
	require.Equal(t, 3, creator.called())
}

func TestRun_aCancelledErrorFromTheServerIsStillAFailure(t *testing.T) {
	creator := &fakeCreator{answer: func(call int, _ flightSpec) (outcome, error) {
		if call == 2 {
			return 0, context.Canceled // not our doing: the context is alive
		}
		return outcomeCreated, nil
	}}

	got := run(context.Background(), creator, numberedFlights(3), 1, quietLogger())

	require.Equal(t, summary{created: 2, failed: 1}, got)
}

func TestRun_logsEachFailureAndEveryThousandthFlight(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	creator := &fakeCreator{answer: func(_ int, spec flightSpec) (outcome, error) {
		if spec.FlightNumber == "GA5" {
			return 0, errors.New("flight five broke")
		}
		return outcomeCreated, nil
	}}

	run(context.Background(), creator, numberedFlights(2500), 4, log)

	require.Equal(t, 1, strings.Count(logs.String(), "create flight failed"))
	require.Contains(t, logs.String(), "flight five broke")
	require.Equal(t, 2, strings.Count(logs.String(), "seeding progress"))
	require.Contains(t, logs.String(), "done=1000 total=2500")
	require.Contains(t, logs.String(), "done=2000 total=2500")
}

func TestRun_worksWithTheRealClientAgainstAFakeAPI(t *testing.T) {
	var mu sync.Mutex
	stored := make(map[string]bool)
	c := loggedInClient(t, func(w http.ResponseWriter, r *http.Request) {
		var spec flightSpec
		require.NoError(t, json.NewDecoder(r.Body).Decode(&spec))
		key := spec.FlightNumber + spec.DepartureAt.UTC().String()

		mu.Lock()
		exists := stored[key]
		stored[key] = true
		mu.Unlock()

		if exists {
			writeJSON(w, http.StatusConflict, `{"error":{"code":"FLIGHT_EXISTS","message":"exists"}}`)
			return
		}
		writeJSON(w, http.StatusCreated, `{}`)
	})
	flights, err := plan(planStart, 1)
	require.NoError(t, err)

	first := run(context.Background(), c, flights, 8, quietLogger())
	second := run(context.Background(), c, flights, 8, quietLogger())

	require.Equal(t, summary{created: flightsPerDay}, first)
	require.Equal(t, summary{existing: flightsPerDay}, second, "running the seed again is harmless")
}

func TestRun_stopsAtExactlyTheFailureLimit(t *testing.T) {
	// A call past the limit only returns once the run is cancelled. A run that
	// cancels too late waits for the timeout instead, and that error counts as
	// one failure too many.
	parent, cancelTimeout := context.WithTimeout(context.Background(), time.Second)
	defer cancelTimeout()
	creator := &fakeCreator{answerWithContext: func(ctx context.Context, call int, _ flightSpec) (outcome, error) {
		if call > maxFailures {
			<-ctx.Done()
			return 0, ctx.Err()
		}
		return 0, errors.New("boom")
	}}

	got := run(parent, creator, numberedFlights(100), 1, quietLogger())

	require.Equal(t, maxFailures, got.failed)
	require.True(t, got.aborted)
}
