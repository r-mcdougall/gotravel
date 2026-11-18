package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/r-mcdougall/gotravel/internal/platform/cache"
	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

type searchCache interface {
	Get(ctx context.Context, key string) (value []byte, found bool, err error)
	Set(ctx context.Context, key string, value []byte, ttl time.Duration) error
	Delete(ctx context.Context, keys ...string) error
}

var _ searchCache = (*cache.Client)(nil)

const (
	lookupHit   = "hit"
	lookupMiss  = "miss"
	lookupError = "error"
)

type routeDay struct {
	Origin      string
	Destination string
	Date        string // YYYY-MM-DD
}

func (d routeDay) key() string {
	return fmt.Sprintf("catalog:flights:%s:%s:%s", d.Origin, d.Destination, d.Date)
}

type RouteDayCache struct {
	cache   searchCache
	ttl     time.Duration
	log     *slog.Logger
	lookups *prometheus.CounterVec
}

func NewRouteDayCache(c searchCache, ttl time.Duration, log *slog.Logger, registerer prometheus.Registerer) *RouteDayCache {
	lookups := promauto.With(registerer).NewCounterVec(prometheus.CounterOpts{
		Name: "catalog_search_cache_total",
		Help: "Search cache lookups by result: hit, miss, or error when Redis or the entry could not be used.",
	}, []string{"result"})
	for _, result := range []string{lookupHit, lookupMiss, lookupError} {
		lookups.WithLabelValues(result)
	}

	return &RouteDayCache{cache: c, ttl: ttl, log: log, lookups: lookups}
}

func (c *RouteDayCache) load(ctx context.Context, day routeDay, loadFlights func(context.Context) ([]store.Flight, error)) ([]store.Flight, error) {
	if c == nil {
		return loadFlights(ctx)
	}

	key := day.key()
	cached, found, err := c.cache.Get(ctx, key)
	switch {
	case err != nil:
		c.lookups.WithLabelValues(lookupError).Inc()
		c.log.WarnContext(ctx, "search cache unavailable, reading from the database", slog.String("key", key), slog.Any("error", err))
		return loadFlights(ctx)
	case found:
		var flights []store.Flight
		if err = json.Unmarshal(cached, &flights); err == nil {
			c.lookups.WithLabelValues(lookupHit).Inc()
			return flights, nil
		}
		c.lookups.WithLabelValues(lookupError).Inc()
		c.log.WarnContext(ctx, "discarding unreadable search cache entry", slog.String("key", key), slog.Any("error", err))
	default:
		c.lookups.WithLabelValues(lookupMiss).Inc()
	}

	flights, err := loadFlights(ctx)
	if err != nil {
		return nil, err
	}
	c.store(ctx, key, flights)

	return flights, nil
}

func (c *RouteDayCache) store(ctx context.Context, key string, flights []store.Flight) {
	payload, err := json.Marshal(flights)
	if err == nil {
		err = c.cache.Set(ctx, key, payload, c.ttl)
	}
	if err != nil {
		c.log.WarnContext(ctx, "could not store search cache entry", slog.String("key", key), slog.Any("error", err))
	}
}

func (c *RouteDayCache) invalidate(ctx context.Context, days ...routeDay) {
	if c == nil || len(days) == 0 {
		return
	}

	keys := make([]string, len(days))
	for i, day := range days {
		keys[i] = day.key()
	}

	if err := c.cache.Delete(context.WithoutCancel(ctx), keys...); err != nil {
		c.log.WarnContext(ctx, "could not invalidate search cache, entries expire after the TTL", slog.Any("keys", keys), slog.Any("error", err))
	}
}

// forget drops the cached route-days of flights. A flight's day is the local date at its origin airport.
func (c *RouteDayCache) forget(ctx context.Context, reference *Reference, flights ...store.Flight) {
	if c == nil {
		return
	}

	var days []routeDay
	for _, flight := range flights {
		origin, ok := reference.Airport(flight.OriginCode)
		if !ok {
			continue // a stored flight always names an airport from the reference data
		}

		day := routeDay{
			Origin:      flight.OriginCode,
			Destination: flight.DestinationCode,
			Date:        flight.DepartureAt.In(origin.Location).Format(time.DateOnly),
		}
		if !slices.Contains(days, day) {
			days = append(days, day)
		}
	}

	c.invalidate(ctx, days...)
}
