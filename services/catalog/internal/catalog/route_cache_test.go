package catalog

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

const testCacheTTL = time.Minute

var testRouteDay = routeDay{Origin: "CGK", Destination: "DPS", Date: "2026-11-20"}

const testRouteDayKey = "catalog:flights:CGK:DPS:2026-11-20"

type fakeSearchCache struct {
	t      *testing.T
	get    func(ctx context.Context, key string) ([]byte, bool, error)
	set    func(ctx context.Context, key string, value []byte, ttl time.Duration) error
	delete func(ctx context.Context, keys ...string) error
}

func (f *fakeSearchCache) Get(ctx context.Context, key string) ([]byte, bool, error) {
	f.t.Helper()
	if f.get == nil {
		f.t.Fatal("unexpected call to Get")
	}
	return f.get(ctx, key)
}

func (f *fakeSearchCache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	f.t.Helper()
	if f.set == nil {
		f.t.Fatal("unexpected call to Set")
	}
	return f.set(ctx, key, value, ttl)
}

func (f *fakeSearchCache) Delete(ctx context.Context, keys ...string) error {
	f.t.Helper()
	if f.delete == nil {
		f.t.Fatal("unexpected call to Delete")
	}
	return f.delete(ctx, keys...)
}

// memoryCache is a working searchCache for tests that follow a key over several calls.
type memoryCache struct {
	entries map[string][]byte
}

func newMemoryCache(t *testing.T) (*memoryCache, *fakeSearchCache) {
	t.Helper()
	memory := &memoryCache{entries: map[string][]byte{}}
	return memory, &fakeSearchCache{
		t: t,
		get: func(_ context.Context, key string) ([]byte, bool, error) {
			value, ok := memory.entries[key]
			return value, ok, nil
		},
		set: func(_ context.Context, key string, value []byte, _ time.Duration) error {
			memory.entries[key] = value
			return nil
		},
		delete: func(_ context.Context, keys ...string) error {
			for _, key := range keys {
				delete(memory.entries, key)
			}
			return nil
		},
	}
}

func newTestRouteDayCache(c searchCache) (*RouteDayCache, *bytes.Buffer) {
	logs := &bytes.Buffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	return NewRouteDayCache(c, testCacheTTL, log, prometheus.NewRegistry()), logs
}

func cacheLookups(c *RouteDayCache, result string) float64 {
	return testutil.ToFloat64(c.lookups.WithLabelValues(result))
}

func sampleRouteDayFlights() []store.Flight {
	morning := sampleFlight()
	evening := sampleFlight()
	evening.ID = "9a4f7d1e-2b6c-4e83-8f15-6c0d2a7b3e42"
	evening.FlightNumber = "GA412"
	evening.DepartureAt = time.Date(2026, 11, 20, 19, 0, 0, 0, wib).UTC()
	evening.ArrivalAt = time.Date(2026, 11, 20, 21, 55, 0, 0, wita).UTC()
	evening.Price = 990_000
	return []store.Flight{morning, evening}
}

func requireSameFlights(t *testing.T, want, got []store.Flight) {
	t.Helper()
	require.Len(t, got, len(want))
	for i := range want {
		require.Equal(t, want[i].ID, got[i].ID)
		require.Equal(t, want[i].FlightNumber, got[i].FlightNumber)
		require.Equal(t, want[i].AirlineCode, got[i].AirlineCode)
		require.Equal(t, want[i].OriginCode, got[i].OriginCode)
		require.Equal(t, want[i].DestinationCode, got[i].DestinationCode)
		require.True(t, want[i].DepartureAt.Equal(got[i].DepartureAt), "departure %s != %s", want[i].DepartureAt, got[i].DepartureAt)
		require.True(t, want[i].ArrivalAt.Equal(got[i].ArrivalAt), "arrival %s != %s", want[i].ArrivalAt, got[i].ArrivalAt)
		require.Equal(t, want[i].Price, got[i].Price)
		require.Equal(t, want[i].Capacity, got[i].Capacity)
		require.Equal(t, want[i].AvailableSeats, got[i].AvailableSeats)
		require.Equal(t, want[i].Version, got[i].Version)
	}
}

// source returns a loader that counts its calls.
func source(flights []store.Flight, calls *int) func(context.Context) ([]store.Flight, error) {
	return func(context.Context) ([]store.Flight, error) {
		*calls++
		return flights, nil
	}
}

func TestRouteDayKey_namesTheRouteAndTheLocalDate(t *testing.T) {
	require.Equal(t, testRouteDayKey, testRouteDay.key())
	require.NotEqual(t, testRouteDay.key(), routeDay{Origin: "DPS", Destination: "CGK", Date: "2026-11-20"}.key())
}

func TestRouteDayCacheLoad_readsTheSourceOnAMissAndStoresTheRowsUnderTheRouteDayKey(t *testing.T) {
	var (
		storedKey string
		stored    []byte
		storedTTL time.Duration
	)
	fake := &fakeSearchCache{
		t:   t,
		get: func(context.Context, string) ([]byte, bool, error) { return nil, false, nil },
		set: func(_ context.Context, key string, value []byte, ttl time.Duration) error {
			storedKey, stored, storedTTL = key, value, ttl
			return nil
		},
	}
	routeCache, _ := newTestRouteDayCache(fake)
	want := sampleRouteDayFlights()
	calls := 0

	got, err := routeCache.load(t.Context(), testRouteDay, source(want, &calls))

	require.NoError(t, err)
	requireSameFlights(t, want, got)
	require.Equal(t, 1, calls)
	require.Equal(t, testRouteDayKey, storedKey)
	require.Equal(t, testCacheTTL, storedTTL)
	var decoded []store.Flight
	require.NoError(t, json.Unmarshal(stored, &decoded))
	requireSameFlights(t, want, decoded)
	require.Equal(t, 1.0, cacheLookups(routeCache, lookupMiss))
}

func TestRouteDayCacheLoad_servesTheSecondCallFromTheCacheWithoutTheSource(t *testing.T) {
	_, fake := newMemoryCache(t)
	routeCache, _ := newTestRouteDayCache(fake)
	want := sampleRouteDayFlights()
	calls := 0

	_, err := routeCache.load(t.Context(), testRouteDay, source(want, &calls))
	require.NoError(t, err)
	got, err := routeCache.load(t.Context(), testRouteDay, source(nil, &calls))

	require.NoError(t, err)
	requireSameFlights(t, want, got)
	require.Equal(t, 1, calls, "the second call must not reach the source")
	require.Equal(t, 1.0, cacheLookups(routeCache, lookupMiss))
	require.Equal(t, 1.0, cacheLookups(routeCache, lookupHit))
}

func TestRouteDayCacheLoad_remembersADayWithoutFlights(t *testing.T) {
	_, fake := newMemoryCache(t)
	routeCache, _ := newTestRouteDayCache(fake)
	calls := 0

	_, err := routeCache.load(t.Context(), testRouteDay, source(nil, &calls))
	require.NoError(t, err)
	got, err := routeCache.load(t.Context(), testRouteDay, source(sampleRouteDayFlights(), &calls))

	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, 1, calls, "an empty day is cached too, so repeated searches do not hit the database")
}

func TestRouteDayCacheLoad_keepsRouteDaysApart(t *testing.T) {
	_, fake := newMemoryCache(t)
	routeCache, _ := newTestRouteDayCache(fake)
	calls := 0
	nextDay := routeDay{Origin: "CGK", Destination: "DPS", Date: "2026-11-21"}

	_, err := routeCache.load(t.Context(), testRouteDay, source(sampleRouteDayFlights(), &calls))
	require.NoError(t, err)
	got, err := routeCache.load(t.Context(), nextDay, source(nil, &calls))

	require.NoError(t, err)
	require.Empty(t, got)
	require.Equal(t, 2, calls)
}

func TestRouteDayCacheLoad_readsTheSourceWithoutStoringWhenRedisFails(t *testing.T) {
	fake := &fakeSearchCache{
		t:   t,
		get: func(context.Context, string) ([]byte, bool, error) { return nil, false, errBoom },
		// set is nil: a store attempt would wait on the failing Redis again, and fails the test.
	}
	routeCache, logs := newTestRouteDayCache(fake)
	want := sampleRouteDayFlights()
	calls := 0

	got, err := routeCache.load(t.Context(), testRouteDay, source(want, &calls))

	require.NoError(t, err)
	requireSameFlights(t, want, got)
	require.Equal(t, 1, calls)
	require.Equal(t, 1.0, cacheLookups(routeCache, lookupError))
	require.Contains(t, logs.String(), `"level":"WARN"`)
	require.Contains(t, logs.String(), "search cache unavailable")
}

func TestRouteDayCacheLoad_replacesAnUnreadableEntry(t *testing.T) {
	memory, fake := newMemoryCache(t)
	memory.entries[testRouteDayKey] = []byte("not json")
	routeCache, logs := newTestRouteDayCache(fake)
	want := sampleRouteDayFlights()
	calls := 0

	got, err := routeCache.load(t.Context(), testRouteDay, source(want, &calls))

	require.NoError(t, err)
	requireSameFlights(t, want, got)
	require.Equal(t, 1, calls)
	require.Equal(t, 1.0, cacheLookups(routeCache, lookupError))
	require.Contains(t, logs.String(), "discarding unreadable search cache entry")

	var healed []store.Flight
	require.NoError(t, json.Unmarshal(memory.entries[testRouteDayKey], &healed))
	requireSameFlights(t, want, healed)
}

func TestRouteDayCacheLoad_returnsTheSourceErrorAndStoresNothing(t *testing.T) {
	fake := &fakeSearchCache{
		t:   t,
		get: func(context.Context, string) ([]byte, bool, error) { return nil, false, nil },
		// set is nil: nothing may be stored when the source failed.
	}
	routeCache, _ := newTestRouteDayCache(fake)

	_, err := routeCache.load(t.Context(), testRouteDay, func(context.Context) ([]store.Flight, error) {
		return nil, errBoom
	})

	require.ErrorIs(t, err, errBoom)
}

func TestRouteDayCacheLoad_stillReturnsTheFlightsWhenStoringFails(t *testing.T) {
	fake := &fakeSearchCache{
		t:   t,
		get: func(context.Context, string) ([]byte, bool, error) { return nil, false, nil },
		set: func(context.Context, string, []byte, time.Duration) error { return errBoom },
	}
	routeCache, logs := newTestRouteDayCache(fake)
	want := sampleRouteDayFlights()
	calls := 0

	got, err := routeCache.load(t.Context(), testRouteDay, source(want, &calls))

	require.NoError(t, err)
	requireSameFlights(t, want, got)
	require.Contains(t, logs.String(), "could not store search cache entry")
}

func TestRouteDayCacheLoad_aNilCacheAlwaysReadsTheSource(t *testing.T) {
	var routeCache *RouteDayCache
	want := sampleRouteDayFlights()
	calls := 0

	for range 2 {
		got, err := routeCache.load(t.Context(), testRouteDay, source(want, &calls))
		require.NoError(t, err)
		requireSameFlights(t, want, got)
	}

	require.Equal(t, 2, calls)
}

func TestRouteDayCacheInvalidate_deletesTheKeysOfTheRouteDays(t *testing.T) {
	var deleted []string
	fake := &fakeSearchCache{
		t: t,
		delete: func(_ context.Context, keys ...string) error {
			deleted = keys
			return nil
		},
	}
	routeCache, _ := newTestRouteDayCache(fake)
	nextDay := routeDay{Origin: "CGK", Destination: "DPS", Date: "2026-11-21"}

	routeCache.invalidate(t.Context(), testRouteDay, nextDay)

	require.Equal(t, []string{testRouteDayKey, "catalog:flights:CGK:DPS:2026-11-21"}, deleted)
}

func TestRouteDayCacheInvalidate_makesTheNextLoadReadTheSource(t *testing.T) {
	_, fake := newMemoryCache(t)
	routeCache, _ := newTestRouteDayCache(fake)
	calls := 0
	_, err := routeCache.load(t.Context(), testRouteDay, source(sampleRouteDayFlights(), &calls))
	require.NoError(t, err)

	routeCache.invalidate(t.Context(), testRouteDay)
	_, err = routeCache.load(t.Context(), testRouteDay, source(sampleRouteDayFlights(), &calls))

	require.NoError(t, err)
	require.Equal(t, 2, calls)
}

func TestRouteDayCacheInvalidate_stillDeletesWhenTheRequestWasCancelled(t *testing.T) {
	var deleteContextErr error
	fake := &fakeSearchCache{
		t: t,
		delete: func(ctx context.Context, _ ...string) error {
			deleteContextErr = ctx.Err()
			return nil
		},
	}
	routeCache, _ := newTestRouteDayCache(fake)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	routeCache.invalidate(ctx, testRouteDay)

	require.NoError(t, deleteContextErr, "the committed write must still invalidate")
}

func TestRouteDayCacheInvalidate_warnsAndCarriesOnWhenRedisFails(t *testing.T) {
	fake := &fakeSearchCache{
		t:      t,
		delete: func(context.Context, ...string) error { return errBoom },
	}
	routeCache, logs := newTestRouteDayCache(fake)

	routeCache.invalidate(t.Context(), testRouteDay)

	require.Contains(t, logs.String(), `"level":"WARN"`)
	require.Contains(t, logs.String(), "could not invalidate search cache")
}

func TestRouteDayCacheInvalidate_withoutRouteDaysOrCacheDoesNothing(t *testing.T) {
	routeCache, _ := newTestRouteDayCache(&fakeSearchCache{t: t}) // any call would fail the test
	routeCache.invalidate(t.Context())

	var disabled *RouteDayCache
	disabled.invalidate(t.Context(), testRouteDay)
}

func TestNewRouteDayCache_reportsEveryResultFromTheStart(t *testing.T) {
	routeCache, _ := newTestRouteDayCache(&fakeSearchCache{t: t})

	require.Equal(t, 3, testutil.CollectAndCount(routeCache.lookups, "catalog_search_cache_total"))
	require.Equal(t, 0.0, cacheLookups(routeCache, lookupHit))
	require.Equal(t, 0.0, cacheLookups(routeCache, lookupMiss))
	require.Equal(t, 0.0, cacheLookups(routeCache, lookupError))
}
