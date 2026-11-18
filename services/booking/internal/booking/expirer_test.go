package booking

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

const otherFlightID = "5b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"

type fakeCounter struct {
	mu    sync.Mutex
	value float64
}

func (c *fakeCounter) Add(v float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.value += v
}

func (c *fakeCounter) get() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

func TestExpirerExpireBatch_expiresOverdueHoldsAndReleasesTheirSeats(t *testing.T) {
	pool, overdue, current := newExpiryServices(t)
	addFlight(t, pool, testFlightID, time.Now().Add(24*time.Hour), 180)
	addFlight(t, pool, otherFlightID, time.Now().Add(24*time.Hour), 180)

	stale := []string{
		hold(t, overdue, testFlightID, 2),
		hold(t, overdue, testFlightID, 1),
		hold(t, overdue, otherFlightID, 1),
	}
	fresh := hold(t, current, testFlightID, 1)

	expired := &fakeCounter{}
	expirer := NewExpirer(NewPgStore(pool), expired, slog.New(slog.DiscardHandler))

	n, err := expirer.expireBatch(t.Context())
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.InDelta(t, 3, expired.get(), 0)

	for _, id := range stale {
		require.Equal(t, "EXPIRED", bookingStatus(t, pool, id))
	}
	require.Equal(t, "HELD", bookingStatus(t, pool, fresh), "a hold that has not expired is left alone")

	inventory := getInventory(t, pool)
	require.Equal(t, int32(1), inventory.HeldSeats, "only the fresh hold keeps its seat")
	require.Equal(t, int64(5), inventory.AvailabilityVersion, "arrival, three holds and one release")
	require.Equal(t, 6, count(t, pool, "outbox"), "four holds and one release per flight")
	require.JSONEq(t,
		fmt.Sprintf(`{"flight_id":%q,"availability_version":5,"available_seats":179}`, testFlightID),
		latestEvent(t, pool, testFlightID))

	n, err = expirer.expireBatch(t.Context())
	require.NoError(t, err)
	require.Zero(t, n)
}

func TestExpirerExpireBatch_leavesCancelledHoldsAlone(t *testing.T) {
	pool, overdue, _ := newExpiryServices(t)
	addFlight(t, pool, testFlightID, time.Now().Add(24*time.Hour), 180)
	id := hold(t, overdue, testFlightID, 2)
	_, err := overdue.CancelBooking(t.Context(), johnID, id)
	require.NoError(t, err)

	n, err := NewExpirer(NewPgStore(pool), &fakeCounter{}, slog.New(slog.DiscardHandler)).expireBatch(t.Context())

	require.NoError(t, err)
	require.Zero(t, n)
	require.Equal(t, "CANCELLED", bookingStatus(t, pool, id))
	require.Zero(t, getInventory(t, pool).HeldSeats, "seats are not released twice")
}

func TestExpirerExpireBatch_expiresAtMostOneBatchPerCall(t *testing.T) {
	pool := newTestPool(t)
	addFlight(t, pool, testFlightID, time.Now().Add(24*time.Hour), 200)
	_, err := pool.Exec(t.Context(), `
		INSERT INTO bookings (user_id, flight_id, status, passenger_count, seat_price, expires_at)
		SELECT $1, $2, 'HELD', 1, 1250000, now() - interval '1 minute' FROM generate_series(1, $3)`,
		johnID, testFlightID, expireBatchSize+1)
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `UPDATE flight_inventories SET held_seats = $1`, expireBatchSize+1)
	require.NoError(t, err)
	expirer := NewExpirer(NewPgStore(pool), &fakeCounter{}, slog.New(slog.DiscardHandler))

	n, err := expirer.expireBatch(t.Context())
	require.NoError(t, err)
	require.Equal(t, expireBatchSize, n)

	n, err = expirer.expireBatch(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Zero(t, getInventory(t, pool).HeldSeats)
}

func TestExpirerExpireBatch_skipsHoldsLockedByAnotherTransaction(t *testing.T) {
	pool, overdue, _ := newExpiryServices(t)
	addFlight(t, pool, testFlightID, time.Now().Add(24*time.Hour), 180)
	id := hold(t, overdue, testFlightID, 1)
	expirer := NewExpirer(NewPgStore(pool), &fakeCounter{}, slog.New(slog.DiscardHandler))

	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) }) // before pool.Close, which waits for this connection
	_, err = tx.Exec(t.Context(), `SELECT id FROM bookings WHERE id = $1 FOR UPDATE`, id)
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

func TestExpirerRun_expiresHoldsUntilTheContextIsCanceled(t *testing.T) {
	pool, overdue, _ := newExpiryServices(t)
	addFlight(t, pool, testFlightID, time.Now().Add(24*time.Hour), 180)
	id := hold(t, overdue, testFlightID, 1)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		NewExpirer(NewPgStore(pool), &fakeCounter{}, slog.New(slog.DiscardHandler)).Run(ctx)
	}()

	require.Eventually(t, func() bool {
		var status string
		err := pool.QueryRow(ctx, `SELECT status FROM bookings WHERE id = $1`, id).Scan(&status)
		return err == nil && status == "EXPIRED"
	}, 5*time.Second, 50*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was canceled")
	}
}

// newExpiryServices returns a service whose holds are already overdue and one whose holds are current.
func newExpiryServices(t *testing.T) (pool *pgxpool.Pool, overdue, current *service) {
	t.Helper()

	pool = newTestPool(t)
	anHourAgo := time.Now().Add(-time.Hour)
	overdue = newService(NewPgStore(pool), 15*time.Minute, func() time.Time { return anHourAgo })
	current = newService(NewPgStore(pool), 15*time.Minute, time.Now)
	return pool, overdue, current
}

func hold(t *testing.T, svc *service, flightID string, passengers int) string {
	t.Helper()

	request := HoldRequest{FlightID: flightID}
	for range passengers {
		request.Passengers = append(request.Passengers, PassengerRequest{FullName: "John Doe", BirthDate: "1990-05-17"})
	}
	held, err := svc.Hold(t.Context(), johnID, request)
	require.NoError(t, err)
	return held.ID
}

func bookingStatus(t *testing.T, pool *pgxpool.Pool, id string) string {
	t.Helper()

	var status string
	require.NoError(t, pool.QueryRow(t.Context(), `SELECT status FROM bookings WHERE id = $1`, id).Scan(&status))
	return status
}

func latestEvent(t *testing.T, pool *pgxpool.Pool, flightID string) string {
	t.Helper()

	var payload []byte
	err := pool.QueryRow(t.Context(),
		`SELECT payload FROM outbox WHERE payload->>'flight_id' = $1 ORDER BY created_at DESC, (payload->>'availability_version')::bigint DESC LIMIT 1`,
		flightID).Scan(&payload)
	require.NoError(t, err)
	return string(payload)
}
