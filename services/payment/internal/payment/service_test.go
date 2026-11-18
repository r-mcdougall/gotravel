package payment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/database"
	"github.com/r-mcdougall/gotravel/internal/platform/dbtest"
	"github.com/r-mcdougall/gotravel/services/payment/internal/store"
	"github.com/r-mcdougall/gotravel/services/payment/migrations"
)

const (
	johnID    = "11111111-1111-4111-8111-111111111111"
	janeID    = "22222222-2222-4222-8222-222222222222"
	johnToken = "Bearer john-token"
)

var (
	databaseOnce      sync.Once
	databaseURLValue  string
	databaseErr       error
	terminateDatabase func()
)

func TestMain(m *testing.M) {
	code := m.Run()
	if terminateDatabase != nil {
		terminateDatabase()
	}
	os.Exit(code)
}

// unusedStore fails the test on any call, for requests that must be refused before reaching the database.
type unusedStore struct{ t *testing.T }

func (s unusedStore) fail(name string) { s.t.Fatalf("unexpected call to %s", name) }

func (s unusedStore) CreatePayment(context.Context, store.CreatePaymentParams) (store.Payment, error) {
	s.fail("CreatePayment")
	return store.Payment{}, nil
}

func (s unusedStore) GetPayment(context.Context, store.GetPaymentParams) (store.Payment, error) {
	s.fail("GetPayment")
	return store.Payment{}, nil
}

func (s unusedStore) ReversePayment(context.Context, string) (store.Payment, error) {
	s.fail("ReversePayment")
	return store.Payment{}, nil
}

func (s unusedStore) InsertOutboxEvent(context.Context, store.InsertOutboxEventParams) error {
	s.fail("InsertOutboxEvent")
	return nil
}

func (s unusedStore) InTx(context.Context, func(queries) error) error {
	s.fail("InTx")
	return nil
}

// fakeBookings answers with get; a nil get fails the test.
type fakeBookings struct {
	t   *testing.T
	get func(authorization, id string) (Booking, error)
}

func (f fakeBookings) GetBooking(_ context.Context, authorization, id string) (Booking, error) {
	if f.get == nil {
		f.t.Fatal("unexpected call to GetBooking")
	}
	return f.get(authorization, id)
}

// heldBooking returns a booking that can be paid until expiresAt.
func heldBooking(t *testing.T, expiresAt time.Time) fakeBookings {
	return fakeBookings{t: t, get: func(authorization, id string) (Booking, error) {
		require.Equal(t, johnToken, authorization)
		return Booking{ID: id, Status: "HELD", TotalPrice: 2_500_000, ExpiresAt: expiresAt}, nil
	}}
}

func TestServicePay_rejectsAnInvalidRequestBeforeAskingBooking(t *testing.T) {
	svc := newService(unusedStore{t}, fakeBookings{t: t}, time.Now)

	_, err := svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: "not-a-uuid"})

	var validationErr *ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "booking_id", validationErr.Details[0].Field)
}

func TestServicePay_refusesABookingThatCannotBePaid(t *testing.T) {
	now := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		booking Booking
	}{
		{name: "confirmed", booking: Booking{Status: "CONFIRMED", ExpiresAt: now.Add(time.Minute)}},
		{name: "expired", booking: Booking{Status: "EXPIRED", ExpiresAt: now.Add(-time.Minute)}},
		{name: "cancelled", booking: Booking{Status: "CANCELLED", ExpiresAt: now.Add(time.Minute)}},
		{name: "held but past its expiry", booking: Booking{Status: "HELD", ExpiresAt: now}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bookings := fakeBookings{t: t, get: func(string, string) (Booking, error) { return tt.booking, nil }}
			svc := newService(unusedStore{t}, bookings, func() time.Time { return now })

			_, err := svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: testBookingID})

			require.ErrorIs(t, err, ErrBookingNotPayable)
		})
	}
}

func TestServicePay_passesOnWhatBookingAnswered(t *testing.T) {
	for _, want := range []error{ErrBookingNotFound, ErrBookingUnavailable} {
		bookings := fakeBookings{t: t, get: func(string, string) (Booking, error) { return Booking{}, want }}
		svc := newService(unusedStore{t}, bookings, time.Now)

		_, err := svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: testBookingID})

		require.ErrorIs(t, err, want)
	}
}

func TestServiceGetPayment_treatsAMalformedIDAsNotFound(t *testing.T) {
	svc := newService(unusedStore{t}, fakeBookings{t: t}, time.Now)

	_, err := svc.GetPayment(t.Context(), johnID, "not-a-uuid")

	require.ErrorIs(t, err, ErrNotFound)
}

func TestServicePay_chargesTheBookingAndAnnouncesTheSuccess(t *testing.T) {
	pool := newTestPool(t)
	now := time.Now().UTC()
	svc := newService(NewPgStore(pool), heldBooking(t, now.Add(15*time.Minute)), func() time.Time { return now })

	paid, err := svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: testBookingID})
	require.NoError(t, err)

	require.Equal(t, testBookingID, paid.BookingID)
	require.Equal(t, int64(2_500_000), paid.Amount)
	require.Equal(t, "SUCCEEDED", paid.Status)
	requireOutbox(t, pool, fmt.Sprintf(`{"payment_id":%q,"booking_id":%q,"amount":2500000}`, paid.ID, testBookingID))

	got, err := svc.GetPayment(t.Context(), johnID, paid.ID)
	require.NoError(t, err)
	require.Equal(t, paid, got)
}

func TestServicePay_recordsAFailedPaymentWithoutAnnouncingIt(t *testing.T) {
	pool := newTestPool(t)
	now := time.Now().UTC()
	svc := newService(NewPgStore(pool), heldBooking(t, now.Add(15*time.Minute)), func() time.Time { return now })

	failed, err := svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: testBookingID, Outcome: "failed"})
	require.NoError(t, err)
	require.Equal(t, "FAILED", failed.Status)
	requireOutbox(t, pool)

	paid, err := svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: testBookingID})
	require.NoError(t, err, "a failed payment can be retried")
	require.Equal(t, "SUCCEEDED", paid.Status)
	requireOutbox(t, pool, fmt.Sprintf(`{"payment_id":%q,"booking_id":%q,"amount":2500000}`, paid.ID, testBookingID))
}

func TestServicePay_refusesASecondSuccessfulPayment(t *testing.T) {
	pool := newTestPool(t)
	now := time.Now().UTC()
	svc := newService(NewPgStore(pool), heldBooking(t, now.Add(15*time.Minute)), func() time.Time { return now })

	_, err := svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: testBookingID})
	require.NoError(t, err)

	_, err = svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: testBookingID})
	require.ErrorIs(t, err, ErrAlreadyPaid)
	require.Equal(t, 1, count(t, pool, "payments"))
	require.Equal(t, 1, count(t, pool, "outbox"))
}

func TestServicePay_neverChargesABookingTwiceAtTheSameTime(t *testing.T) {
	pool := newTestPool(t)
	now := time.Now().UTC()
	svc := newService(NewPgStore(pool), heldBooking(t, now.Add(15*time.Minute)), func() time.Time { return now })

	const attempts = 10
	errs := make(chan error, attempts)
	var wg sync.WaitGroup
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Pay(context.Background(), johnID, johnToken, PayRequest{BookingID: testBookingID})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	succeeded := 0
	for err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, ErrAlreadyPaid)
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, count(t, pool, "payments"))
	require.Equal(t, 1, count(t, pool, "outbox"))
}

func TestServiceGetPayment_hidesPaymentsOfOtherUsers(t *testing.T) {
	pool := newTestPool(t)
	now := time.Now().UTC()
	svc := newService(NewPgStore(pool), heldBooking(t, now.Add(15*time.Minute)), func() time.Time { return now })

	paid, err := svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: testBookingID})
	require.NoError(t, err)

	_, err = svc.GetPayment(t.Context(), janeID, paid.ID)
	require.ErrorIs(t, err, ErrNotFound)

	_, err = svc.GetPayment(t.Context(), johnID, "33333333-3333-4333-8333-333333333333")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestServicePay_doesNotCommitAPaymentWhoseEventCannotBeRecorded(t *testing.T) {
	pool := newTestPool(t)
	now := time.Now().UTC()
	db := failingOutboxStore{NewPgStore(pool)}
	svc := newService(db, heldBooking(t, now.Add(15*time.Minute)), func() time.Time { return now })

	_, err := svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: testBookingID})

	require.ErrorIs(t, err, errOutboxDown)
	require.Zero(t, count(t, pool, "payments"))
}

var errOutboxDown = errors.New("outbox down")

// failingOutboxStore runs real transactions whose outbox insert fails.
type failingOutboxStore struct{ *PgStore }

func (s failingOutboxStore) InTx(ctx context.Context, fn func(queries) error) error {
	return s.PgStore.InTx(ctx, func(q queries) error {
		return fn(failingOutboxQueries{q})
	})
}

type failingOutboxQueries struct{ queries }

func (failingOutboxQueries) InsertOutboxEvent(context.Context, store.InsertOutboxEventParams) error {
	return errOutboxDown
}

func databaseURL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	databaseOnce.Do(func() {
		databaseURLValue, terminateDatabase, databaseErr = dbtest.Start(context.Background())
		if databaseErr == nil {
			databaseErr = database.Migrate(context.Background(), slog.New(slog.DiscardHandler), databaseURLValue, migrations.FS)
		}
	})
	require.NoError(t, databaseErr)
	return databaseURLValue
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := database.Connect(context.Background(), databaseURL(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, err = pool.Exec(t.Context(), "TRUNCATE payments, outbox")
	require.NoError(t, err)
	return pool
}

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()

	var n int
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

// requireOutbox checks the payment events in the outbox, oldest first.
func requireOutbox(t *testing.T, pool *pgxpool.Pool, wantPayloads ...string) {
	t.Helper()

	rows, err := pool.Query(t.Context(), `SELECT event_type, payload FROM outbox ORDER BY created_at, id`)
	require.NoError(t, err)
	defer rows.Close()

	var got []string
	for rows.Next() {
		var eventType string
		var payload []byte
		require.NoError(t, rows.Scan(&eventType, &payload))
		require.Equal(t, "payment.succeeded", eventType)
		got = append(got, string(payload))
	}
	require.NoError(t, rows.Err())

	require.Len(t, got, len(wantPayloads))
	for i, want := range wantPayloads {
		require.JSONEq(t, want, got[i])
	}
}
