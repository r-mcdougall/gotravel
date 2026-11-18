package booking

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

const (
	expireInterval   = 5 * time.Second
	expireBatchSize  = 100
	maxExpireBackoff = 30 * time.Second
)

type counter interface {
	Add(float64)
}

type Expirer struct {
	db      bookingStore
	expired counter
	log     *slog.Logger
}

func NewExpirer(db bookingStore, expired counter, log *slog.Logger) *Expirer {
	return &Expirer{db: db, expired: expired, log: log}
}

func (e *Expirer) Run(ctx context.Context) {
	wait := expireInterval

	for {
		n, err := e.expireBatch(ctx)
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			e.log.WarnContext(ctx, "expiring holds failed", slog.Any("error", err), slog.Duration("retry_in", wait))
			if !sleep(ctx, wait) {
				return
			}
			wait = min(wait*2, maxExpireBackoff)
			continue
		}

		wait = expireInterval
		if n > 0 {
			e.log.InfoContext(ctx, "expired holds", slog.Int("count", n))
		}
		if n == expireBatchSize {
			continue
		}
		if !sleep(ctx, expireInterval) {
			return
		}
	}
}

func (e *Expirer) expireBatch(ctx context.Context) (int, error) {
	var expired []store.ExpireHoldsRow

	fn := func(q queries) error {
		var err error
		expired, err = q.ExpireHolds(ctx, expireBatchSize)
		if err != nil {
			return fmt.Errorf("expire holds: %w", err)
		}

		seats := make(map[string]int32)
		for _, b := range expired {
			seats[b.FlightID] += b.PassengerCount
		}

		for _, flightID := range slices.Sorted(maps.Keys(seats)) {
			flight, err := q.ReleaseHeldSeats(ctx, store.ReleaseHeldSeatsParams{Seats: seats[flightID], FlightID: flightID})
			if err != nil {
				return fmt.Errorf("release held seats: %w", err)
			}
			if err := recordAvailabilityChanged(ctx, q, flight); err != nil {
				return err
			}
		}
		return nil
	}

	if err := e.db.InTx(ctx, fn); err != nil {
		return 0, err
	}

	e.expired.Add(float64(len(expired)))
	return len(expired), nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
