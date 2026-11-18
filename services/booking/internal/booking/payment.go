package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

const (
	eventPaymentSucceeded = "payment.succeeded"
	eventPaymentRejected  = "payment.rejected"
)

type paymentSucceededEvent struct {
	Data paymentSucceededData `json:"data"`
}

type paymentSucceededData struct {
	PaymentID string `json:"payment_id"`
	BookingID string `json:"booking_id"`
	Amount    int64  `json:"amount"`
}

type paymentRejectedData struct {
	PaymentID string `json:"payment_id"`
	BookingID string `json:"booking_id"`
	Reason    string `json:"reason"`
}

// HandlePaymentSucceeded confirms a held booking and sells its seats.
func (h *EventHandler) HandlePaymentSucceeded(ctx context.Context, d messaging.Delivery) error {
	var event paymentSucceededEvent
	if err := json.Unmarshal(d.Body, &event); err != nil {
		return fmt.Errorf("decode %s message %s: %w: %w", eventPaymentSucceeded, d.ID, messaging.ErrPermanent, err)
	}

	data := event.Data
	if !validUUID(data.PaymentID) || !validUUID(data.BookingID) {
		return fmt.Errorf("%s message %s has payment_id %q and booking_id %q: %w",
			eventPaymentSucceeded, d.ID, data.PaymentID, data.BookingID, messaging.ErrPermanent)
	}

	return h.db.InTx(ctx, func(q queries) error {
		booking, err := q.ConfirmBooking(ctx, store.ConfirmBookingParams{PaymentID: data.PaymentID, ID: data.BookingID})
		if errors.Is(err, pgx.ErrNoRows) {
			return refusePayment(ctx, q, data)
		}
		if err != nil {
			return fmt.Errorf("confirm booking %s: %w", data.BookingID, err)
		}

		err = q.SellHeldSeats(ctx, store.SellHeldSeatsParams{Seats: booking.PassengerCount, FlightID: booking.FlightID})
		if err != nil {
			return fmt.Errorf("sell held seats of booking %s: %w", booking.ID, err)
		}
		return nil
	})
}

// refusePayment runs when ConfirmBooking changed no row: the booking is not held anymore.
func refusePayment(ctx context.Context, q queries, data paymentSucceededData) error {
	booking, err := q.GetBookingPayment(ctx, data.BookingID)
	var reason string
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		reason = "booking_not_found"
	case err != nil:
		return fmt.Errorf("get booking %s: %w", data.BookingID, err)
	case booking.Status == "CONFIRMED" && booking.PaymentID != nil && *booking.PaymentID == data.PaymentID:
		return nil // a repeated message for a booking this payment already confirmed
	case booking.Status == "CONFIRMED":
		reason = "booking_paid_by_another_payment"
	case booking.Status == "EXPIRED":
		reason = "booking_expired"
	case booking.Status == "CANCELLED":
		reason = "booking_cancelled"
	default:
		return fmt.Errorf("booking %s is %s but could not be confirmed", data.BookingID, booking.Status)
	}

	return recordPaymentRejected(ctx, q, paymentRejectedData{PaymentID: data.PaymentID, BookingID: data.BookingID, Reason: reason})
}

func recordPaymentRejected(ctx context.Context, q outboxWriter, data paymentRejectedData) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal %s event: %w", eventPaymentRejected, err)
	}

	if err := q.InsertOutboxEvent(ctx, store.InsertOutboxEventParams{EventType: eventPaymentRejected, Payload: payload}); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}
