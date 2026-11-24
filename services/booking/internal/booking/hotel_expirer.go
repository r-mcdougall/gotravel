package booking

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
)

// HotelExpirer turns overdue hotel holds into EXPIRED and gives their nights back. Each hold gets its own
// transaction, because a transaction that locked the nights of two stays could deadlock with a hold.
type HotelExpirer struct {
	db      bookingStore
	expired counter
	log     *slog.Logger
}

func NewHotelExpirer(db bookingStore, expired counter, log *slog.Logger) *HotelExpirer {
	return &HotelExpirer{db: db, expired: expired, log: log}
}

func (e *HotelExpirer) Run(ctx context.Context) {
	runExpiry(ctx, e.log, e.expireBatch)
}

// expireBatch expires up to expireBatchSize holds and counts the ones committed, even when a later one fails.
func (e *HotelExpirer) expireBatch(ctx context.Context) (int, error) {
	n := 0
	defer func() { e.expired.Add(float64(n)) }()

	for n < expireBatchSize {
		expired, err := e.expireOne(ctx)
		if err != nil || !expired {
			return n, err
		}
		n++
	}
	return n, nil
}

func (e *HotelExpirer) expireOne(ctx context.Context) (bool, error) {
	expired := false

	err := e.db.InTx(ctx, func(q queries) error {
		hold, err := q.ExpireHotelHold(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("expire hotel hold: %w", err)
		}

		expired = true
		return releaseRoomNights(ctx, q, hold.RoomTypeID, hold.CheckIn, hold.CheckOut)
	})
	if err != nil {
		return false, err
	}
	return expired, nil
}
