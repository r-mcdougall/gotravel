package payment

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
)

func paymentRejectedDelivery(paymentID string) messaging.Delivery {
	body := fmt.Sprintf(`{
		"event_id": "3a2f1e0d-9c8b-4a7f-a6e5-d4c3b2a1908f",
		"type": "payment.rejected",
		"occurred_at": "2026-11-19T10:00:05Z",
		"data": {"payment_id": %q, "booking_id": %q, "reason": "booking_expired"}
	}`, paymentID, testBookingID)

	return messaging.Delivery{ID: "3a2f1e0d-9c8b-4a7f-a6e5-d4c3b2a1908f", Type: eventPaymentRejected, Body: []byte(body)}
}

func TestEventHandlerHandlePaymentRejected_rejectsAMessageItCanNeverApply(t *testing.T) {
	handler := NewEventHandler(unusedStore{t})

	for name, d := range map[string]messaging.Delivery{
		"malformed body":     {ID: "1", Body: []byte(`{"data":`)},
		"invalid payment id": paymentRejectedDelivery("abc"),
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, handler.HandlePaymentRejected(t.Context(), d), messaging.ErrPermanent)
		})
	}
}

func TestEventHandlerHandlePaymentRejected_reversesTheSucceededPayment(t *testing.T) {
	pool := newTestPool(t)
	now := time.Now().UTC()
	svc := newService(NewPgStore(pool), heldBill(t, now.Add(15*time.Minute)), func() time.Time { return now })
	paid, err := svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: testBookingID})
	require.NoError(t, err)
	handler := NewEventHandler(NewPgStore(pool))

	require.NoError(t, handler.HandlePaymentRejected(t.Context(), paymentRejectedDelivery(paid.ID)))

	reversed, err := svc.GetPayment(t.Context(), johnID, paid.ID)
	require.NoError(t, err)
	require.Equal(t, "REVERSED", reversed.Status)
	require.True(t, reversed.UpdatedAt.After(paid.UpdatedAt))

	require.NoError(t, handler.HandlePaymentRejected(t.Context(), paymentRejectedDelivery(paid.ID)), "a repeated message changes nothing")
	again, err := svc.GetPayment(t.Context(), johnID, paid.ID)
	require.NoError(t, err)
	require.Equal(t, reversed, again)

	require.NoError(t, handler.HandlePaymentRejected(t.Context(), paymentRejectedDelivery("33333333-3333-4333-8333-333333333333")),
		"an unknown payment changes nothing")
}

func TestEventHandlerHandlePaymentRejected_leavesAFailedPaymentAlone(t *testing.T) {
	pool := newTestPool(t)
	now := time.Now().UTC()
	svc := newService(NewPgStore(pool), heldBill(t, now.Add(15*time.Minute)), func() time.Time { return now })
	failed, err := svc.Pay(t.Context(), johnID, johnToken, PayRequest{BookingID: testBookingID, Outcome: "failed"})
	require.NoError(t, err)

	require.NoError(t, NewEventHandler(NewPgStore(pool)).HandlePaymentRejected(t.Context(), paymentRejectedDelivery(failed.ID)))

	got, err := svc.GetPayment(t.Context(), johnID, failed.ID)
	require.NoError(t, err)
	require.Equal(t, "FAILED", got.Status)
}
