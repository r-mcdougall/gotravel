package booking

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

func TestHotelExpirerExpireBatch_expiresOverdueHoldsAndReleasesTheirNights(t *testing.T) {
	pool, overdue, current := newExpiryServices(t)
	addRoomType(t, pool, 5, 2)
	now := time.Now()

	stale := []string{holdRoom(t, overdue, johnID, now, 7, 9), holdRoom(t, overdue, janeID, now, 8, 10)}
	fresh := holdRoom(t, current, johnID, now, 8, 9)

	expired := &fakeCounter{}
	expirer := NewHotelExpirer(NewPgStore(pool), expired, slog.New(slog.DiscardHandler))

	n, err := expirer.expireBatch(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.InDelta(t, 2, expired.get(), 0)

	for _, id := range stale {
		require.Equal(t, "EXPIRED", hotelBookingStatus(t, pool, id))
	}
	require.Equal(t, "HELD", hotelBookingStatus(t, pool, fresh), "a hold that has not expired is left alone")
	require.Equal(t, map[string]int32{day(now, 7): 0, day(now, 8): 1, day(now, 9): 0}, heldRooms(t, pool),
		"only the fresh hold keeps its night")

	n, err = expirer.expireBatch(t.Context())
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestHotelExpirerExpireBatch_leavesCancelledHoldsAlone(t *testing.T) {
	pool, overdue, _ := newExpiryServices(t)
	addRoomType(t, pool, 5, 2)
	id := holdRoom(t, overdue, johnID, time.Now(), 7, 9)
	_, err := overdue.CancelHotelBooking(t.Context(), johnID, id)
	require.NoError(t, err)

	n, err := NewHotelExpirer(NewPgStore(pool), &fakeCounter{}, slog.New(slog.DiscardHandler)).expireBatch(t.Context())

	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, "CANCELLED", hotelBookingStatus(t, pool, id))
	require.Equal(t, map[string]int32{day(time.Now(), 7): 0, day(time.Now(), 8): 0}, heldRooms(t, pool),
		"nights are not released twice")
}

func TestHotelExpirerExpireBatch_expiresAtMostOneBatchPerCall(t *testing.T) {
	pool := newTestPool(t)
	addRoomType(t, pool, 200, 2)
	checkIn := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, 7)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO hotel_bookings (user_id, room_type_id, status, check_in, check_out, guests, guest_name, price_per_night, expires_at)
		SELECT $1, $2, 'HELD', $3, $3::date + 1, 1, 'John Doe', 1500000, now() - interval '1 minute' FROM generate_series(1, $4)`,
		johnID, testRoomTypeID, checkIn, expireBatchSize+1)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `INSERT INTO room_nights (room_type_id, night, total_rooms, held_rooms) VALUES ($1, $2, 200, $3)`,
		testRoomTypeID, checkIn, expireBatchSize+1)
	require.NoError(t, err)
	expirer := NewHotelExpirer(NewPgStore(pool), &fakeCounter{}, slog.New(slog.DiscardHandler))

	n, err := expirer.expireBatch(t.Context())
	require.NoError(t, err)
	require.Equal(t, expireBatchSize, n)

	n, err = expirer.expireBatch(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, map[string]int32{checkIn.Format(time.DateOnly): 0}, heldRooms(t, pool))
}

func TestHotelExpirerExpireBatch_skipsHoldsLockedByAnotherTransaction(t *testing.T) {
	pool, overdue, _ := newExpiryServices(t)
	addRoomType(t, pool, 5, 2)
	id := holdRoom(t, overdue, johnID, time.Now(), 7, 9)
	expirer := NewHotelExpirer(NewPgStore(pool), &fakeCounter{}, slog.New(slog.DiscardHandler))

	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) }) // before pool.Close, which waits for this connection
	_, err = tx.Exec(t.Context(), `SELECT id FROM hotel_bookings WHERE id = $1 FOR UPDATE`, id)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	n, err := expirer.expireBatch(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "a hold another transaction is changing is skipped, not waited for")

	require.NoError(t, tx.Rollback(t.Context()))
	n, err = expirer.expireBatch(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestHotelExpirerExpireBatch_countsTheHoldsExpiredBeforeAFailure(t *testing.T) {
	q := &failingExpiryQueries{unusedStore: unusedStore{t}, holds: 2}
	expired := &fakeCounter{}
	expirer := NewHotelExpirer(failingExpiryStore{unusedStore: unusedStore{t}, q: q}, expired, slog.New(slog.DiscardHandler))

	n, err := expirer.expireBatch(t.Context())

	require.ErrorContains(t, err, "database unavailable")
	require.Equal(t, 2, n)
	require.InDelta(t, 2, expired.get(), 0)
}

func TestHotelExpirerExpireBatch_keepsTheNightsInStepWithConcurrentHolds(t *testing.T) {
	const rooms = 25

	pool, overdue, current := newExpiryServices(t)
	addRoomType(t, pool, rooms, 2)
	now := time.Now()
	for i := range 20 {
		holdRoom(t, overdue, johnID, now, 7+i%3, 10+i%2)
	}
	expirer := NewHotelExpirer(NewPgStore(pool), &fakeCounter{}, slog.New(slog.DiscardHandler))

	var wg sync.WaitGroup
	errs := make(chan error, 22)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := expirer.expireBatch(context.Background())
			errs <- err
		}()
	}
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := current.HoldRoom(context.Background(), janeID, hotelHold(now, 8+i%3, 11+i%2))
			if errors.Is(err, ErrRoomsUnavailable) {
				err = nil
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}
	require.Zero(t, count(t, pool, "hotel_bookings WHERE status = 'HELD' AND expires_at <= now()"))
	requireNightsMatchHolds(t, pool)
}

func TestHotelExpirerRun_expiresHoldsUntilTheContextIsCanceled(t *testing.T) {
	pool, overdue, _ := newExpiryServices(t)
	addRoomType(t, pool, 5, 2)
	id := holdRoom(t, overdue, johnID, time.Now(), 7, 9)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewHotelExpirer(NewPgStore(pool), &fakeCounter{}, slog.New(slog.DiscardHandler)).Run(ctx)
	}()

	require.Eventually(t, func() bool {
		var status string
		err := pool.QueryRow(ctx, `SELECT status FROM hotel_bookings WHERE id = $1`, id).Scan(&status)
		return err == nil && status == "EXPIRED"
	}, 5*time.Second, 50*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was canceled")
	}
}

func holdRoom(t *testing.T, svc *service, userID string, now time.Time, checkIn, checkOut int) string {
	t.Helper()

	held, err := svc.HoldRoom(t.Context(), userID, hotelHold(now, checkIn, checkOut))
	require.NoError(t, err)
	return held.ID
}

func hotelBookingStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()

	var status string
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT status FROM hotel_bookings WHERE id = $1`, id).Scan(&status))
	return status
}

// failingExpiryStore runs each expiry against failingExpiryQueries.
type failingExpiryStore struct {
	unusedStore
	q *failingExpiryQueries
}

func (s failingExpiryStore) InTx(_ context.Context, fn func(queries) error) error {
	return fn(s.q)
}

// failingExpiryQueries finds one overdue hold per call until holds runs out, then fails.
type failingExpiryQueries struct {
	unusedStore
	holds int
}

func (q *failingExpiryQueries) ExpireHotelHold(context.Context) (store.ExpireHotelHoldRow, error) {
	if q.holds == 0 {
		return store.ExpireHotelHoldRow{}, errors.New("database unavailable")
	}
	q.holds--
	return store.ExpireHotelHoldRow{RoomTypeID: testRoomTypeID}, nil
}

func (q *failingExpiryQueries) LockRoomNights(context.Context, store.LockRoomNightsParams) ([]store.RoomNight, error) {
	return nil, nil
}

func (q *failingExpiryQueries) ReleaseRoomNights(context.Context, store.ReleaseRoomNightsParams) error {
	return nil
}
