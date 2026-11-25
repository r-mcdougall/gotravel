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

// HandlePaymentSucceeded confirms a held flight or hotel booking and sells its seats or nights. The event does
// not say which kind the booking is, so flight bookings are tried first.
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
		confirmed, err := confirmFlightBooking(ctx, q, data)
		if err != nil || confirmed {
			return err
		}

		confirmed, err = confirmHotelBooking(ctx, q, data)
		if err != nil || confirmed {
			return err
		}

		return refusePayment(ctx, q, data)
	})
}

func confirmFlightBooking(ctx context.Context, q queries, data paymentSucceededData) (bool, error) {
	booking, err := q.ConfirmBooking(ctx, store.ConfirmBookingParams{PaymentID: data.PaymentID, ID: data.BookingID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("confirm booking %s: %w", data.BookingID, err)
	}

	err = q.SellHeldSeats(ctx, store.SellHeldSeatsParams{Seats: booking.PassengerCount, FlightID: booking.FlightID})
	if err != nil {
		return false, fmt.Errorf("sell held seats of booking %s: %w", booking.ID, err)
	}
	return true, nil
}

// confirmHotelBooking locks the booking row, then its nights in date order, like every change to room nights.
func confirmHotelBooking(ctx context.Context, q queries, data paymentSucceededData) (bool, error) {
	booking, err := q.ConfirmHotelBooking(ctx, store.ConfirmHotelBookingParams{PaymentID: data.PaymentID, ID: data.BookingID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("confirm hotel booking %s: %w", data.BookingID, err)
	}

	stay := store.LockRoomNightsParams{RoomTypeID: booking.RoomTypeID, CheckIn: booking.CheckIn, CheckOut: booking.CheckOut}
	if _, err := q.LockRoomNights(ctx, stay); err != nil {
		return false, fmt.Errorf("lock room nights of hotel booking %s: %w", booking.ID, err)
	}

	err = q.SellRoomNights(ctx, store.SellRoomNightsParams{RoomTypeID: booking.RoomTypeID, CheckIn: booking.CheckIn, CheckOut: booking.CheckOut})
	if err != nil {
		return false, fmt.Errorf("sell room nights of hotel booking %s: %w", booking.ID, err)
	}
	return true, nil
}

// refusePayment runs when no held booking took the payment: the booking is not held anymore, or does not exist.
func refusePayment(ctx context.Context, q queries, data paymentSucceededData) error {
	status, paymentID, err := bookingPayment(ctx, q, data.BookingID)
	var reason string
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		reason = "booking_not_found"
	case err != nil:
		return fmt.Errorf("get booking %s: %w", data.BookingID, err)
	case status == "CONFIRMED" && paymentID != nil && *paymentID == data.PaymentID:
		return nil // a repeated message for a booking this payment already confirmed
	case status == "CONFIRMED":
		reason = "booking_paid_by_another_payment"
	case status == "EXPIRED":
		reason = "booking_expired"
	case status == "CANCELLED":
		reason = "booking_cancelled"
	default:
		return fmt.Errorf("booking %s is %s but could not be confirmed", data.BookingID, status)
	}

	return recordPaymentRejected(ctx, q, paymentRejectedData{PaymentID: data.PaymentID, BookingID: data.BookingID, Reason: reason})
}

// bookingPayment returns the status and payment of a flight or hotel booking, or pgx.ErrNoRows.
func bookingPayment(ctx context.Context, q queries, id string) (status string, paymentID *string, err error) {
	flight, err := q.GetBookingPayment(ctx, id)
	if err == nil {
		return flight.Status, flight.PaymentID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", nil, err
	}

	hotel, err := q.GetHotelBookingPayment(ctx, id)
	if err != nil {
		return "", nil, err
	}
	return hotel.Status, hotel.PaymentID, nil
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
