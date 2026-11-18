package booking

import (
	"fmt"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
)

const (
	testPaymentID  = "9d8c7b6a-5f4e-4d3c-8b2a-1f0e9d8c7b6a"
	otherPaymentID = "8c7b6a5f-4e3d-4c2b-9a1f-0e9d8c7b6a5f"
)

func paymentSucceededDelivery(paymentID, bookingID string) messaging.Delivery {
	body := fmt.Sprintf(`{
		"event_id": "4b3a2f1e-0d9c-4b8a-a7f6-e5d4c3b2a190",
		"type": "payment.succeeded",
		"occurred_at": "2026-11-19T10:00:00Z",
		"data": {"payment_id": %q, "booking_id": %q, "amount": 2500000}
	}`, paymentID, bookingID)

	return messaging.Delivery{ID: "4b3a2f1e-0d9c-4b8a-a7f6-e5d4c3b2a190", Type: eventPaymentSucceeded, Body: []byte(body)}
}

func TestEventHandlerHandlePaymentSucceeded_rejectsAMessageItCanNeverApply(t *testing.T) {
	handler := NewEventHandler(unusedStore{t})

	for name, d := range map[string]messaging.Delivery{
		"malformed body":     {ID: "1", Body: []byte(`{"data":`)},
		"invalid payment id": paymentSucceededDelivery("abc", testFlightID),
		"invalid booking id": paymentSucceededDelivery(testPaymentID, "abc"),
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, handler.HandlePaymentSucceeded(t.Context(), d), messaging.ErrPermanent)
		})
	}
}

func TestEventHandlerHandlePaymentSucceeded_confirmsAHeldBookingAndSellsItsSeats(t *testing.T) {
	svc, pool, now := newTestService(t)
	addFlight(t, pool, testFlightID, now.Add(7*24*time.Hour), 180)
	held, err := svc.Hold(t.Context(), johnID, HoldRequest{FlightID: testFlightID, Passengers: twoPassengers()})
	require.NoError(t, err)
	before := getInventory(t, pool)
	handler := NewEventHandler(NewPgStore(pool))

	require.NoError(t, handler.HandlePaymentSucceeded(t.Context(), paymentSucceededDelivery(testPaymentID, held.ID)))

	confirmed, err := svc.GetBooking(t.Context(), johnID, held.ID)
	require.NoError(t, err)
	require.Equal(t, "CONFIRMED", confirmed.Status)
	require.Equal(t, testPaymentID, *confirmed.PaymentID)

	after := getInventory(t, pool)
	require.Zero(t, after.HeldSeats)
	require.Equal(t, int32(2), after.SoldSeats)
	require.Equal(t, before.AvailabilityVersion, after.AvailabilityVersion, "free seats did not change")
	require.Equal(t, 1, count(t, pool, "outbox"), "only the hold reported availability")

	require.NoError(t, handler.HandlePaymentSucceeded(t.Context(), paymentSucceededDelivery(testPaymentID, held.ID)))
	require.Equal(t, after, getInventory(t, pool), "a repeated message changes nothing")
	require.Equal(t, 1, count(t, pool, "outbox"))
}

func TestEventHandlerHandlePaymentSucceeded_rejectsAPaymentForABookingThatIsNotHeld(t *testing.T) {
	tests := []struct {
		name       string
		prepare    func(t *testing.T, svc *service, pool *pgxpool.Pool, bookingID string)
		bookingID  string
		wantReason string
	}{
		{
			name: "expired",
			prepare: func(t *testing.T, _ *service, pool *pgxpool.Pool, bookingID string) {
				_, err := pool.Exec(t.Context(), `UPDATE bookings SET status = 'EXPIRED' WHERE id = $1`, bookingID)
				require.NoError(t, err)
			},
			wantReason: "booking_expired",
		},
		{
			name: "cancelled",
			prepare: func(t *testing.T, svc *service, _ *pgxpool.Pool, bookingID string) {
				_, err := svc.CancelBooking(t.Context(), johnID, bookingID)
				require.NoError(t, err)
			},
			wantReason: "booking_cancelled",
		},
		{
			name: "confirmed by another payment",
			prepare: func(t *testing.T, _ *service, pool *pgxpool.Pool, bookingID string) {
				handler := NewEventHandler(NewPgStore(pool))
				require.NoError(t, handler.HandlePaymentSucceeded(t.Context(), paymentSucceededDelivery(otherPaymentID, bookingID)))
			},
			wantReason: "booking_paid_by_another_payment",
		},
		{
			name:       "unknown",
			prepare:    func(*testing.T, *service, *pgxpool.Pool, string) {},
			bookingID:  "33333333-3333-4333-8333-333333333333",
			wantReason: "booking_not_found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, pool, now := newTestService(t)
			addFlight(t, pool, testFlightID, now.Add(7*24*time.Hour), 180)
			held, err := svc.Hold(t.Context(), johnID, HoldRequest{FlightID: testFlightID, Passengers: twoPassengers()})
			require.NoError(t, err)
			tt.prepare(t, svc, pool, held.ID)
			bookingID := held.ID
			if tt.bookingID != "" {
				bookingID = tt.bookingID
			}
			before := getInventory(t, pool)
			handler := NewEventHandler(NewPgStore(pool))

			require.NoError(t, handler.HandlePaymentSucceeded(t.Context(), paymentSucceededDelivery(testPaymentID, bookingID)))

			require.Equal(t, before, getInventory(t, pool), "no seats move")
			require.JSONEq(t,
				fmt.Sprintf(`{"payment_id":%q,"booking_id":%q,"reason":%q}`, testPaymentID, bookingID, tt.wantReason),
				latestRejection(t, pool))
		})
	}
}

// latestRejection returns the payload of the newest payment.rejected event in the outbox.
func latestRejection(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()

	var payload []byte
	err := pool.QueryRow(t.Context(),
		`SELECT payload FROM outbox WHERE event_type = 'payment.rejected' ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&payload)
	require.NoError(t, err)
	return string(payload)
}

func TestEventHandlerHandlePaymentSucceeded_eitherConfirmsOrLosesToTheExpirer(t *testing.T) {
	for range 20 {
		svc, pool, now := newTestService(t)
		addFlight(t, pool, testFlightID, now.Add(7*24*time.Hour), 180)
		held, err := svc.Hold(t.Context(), johnID, HoldRequest{FlightID: testFlightID, Passengers: twoPassengers()})
		require.NoError(t, err)
		_, err = pool.Exec(t.Context(), `UPDATE bookings SET expires_at = now() - interval '1 second' WHERE id = $1`, held.ID)
		require.NoError(t, err)

		handler := NewEventHandler(NewPgStore(pool))
		expirer := NewExpirer(NewPgStore(pool), &fakeCounter{}, slog.New(slog.DiscardHandler))
		errs := make(chan error, 2)
		go func() {
			errs <- handler.HandlePaymentSucceeded(t.Context(), paymentSucceededDelivery(testPaymentID, held.ID))
		}()
		go func() {
			_, err := expirer.expireBatch(t.Context())
			errs <- err
		}()
		require.NoError(t, <-errs)
		require.NoError(t, <-errs)

		booking, err := svc.GetBooking(t.Context(), johnID, held.ID)
		require.NoError(t, err)
		inventory := getInventory(t, pool)
		require.Zero(t, inventory.HeldSeats)
		switch booking.Status {
		case "CONFIRMED":
			require.Equal(t, int32(2), inventory.SoldSeats)
			require.Equal(t, 1, count(t, pool, "outbox"), "only the hold reported availability")
		case "EXPIRED":
			require.Zero(t, inventory.SoldSeats)
			require.Contains(t, latestRejection(t, pool), `"reason": "booking_expired"`)
		default:
			t.Fatalf("booking ended %s", booking.Status)
		}
	}
}
