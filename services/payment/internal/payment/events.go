package payment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/services/payment/internal/store"
)

const eventPaymentSucceeded = "payment.succeeded"

type paymentSucceededData struct {
	PaymentID string `json:"payment_id"`
	BookingID string `json:"booking_id"`
	Amount    int64  `json:"amount"`
}

type outboxWriter interface {
	InsertOutboxEvent(ctx context.Context, arg store.InsertOutboxEventParams) error
}

func recordPaymentSucceeded(ctx context.Context, q outboxWriter, payment store.Payment) error {
	payload, err := json.Marshal(paymentSucceededData{
		PaymentID: payment.ID,
		BookingID: payment.BookingID,
		Amount:    payment.Amount,
	})
	if err != nil {
		return fmt.Errorf("marshal %s event: %w", eventPaymentSucceeded, err)
	}

	if err := q.InsertOutboxEvent(ctx, store.InsertOutboxEventParams{EventType: eventPaymentSucceeded, Payload: payload}); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

const eventPaymentRejected = "payment.rejected"

type paymentRejectedEvent struct {
	Data paymentRejectedData `json:"data"`
}

type paymentRejectedData struct {
	PaymentID string `json:"payment_id"`
	BookingID string `json:"booking_id"`
	Reason    string `json:"reason"`
}

type EventHandler struct {
	db queries
}

func NewEventHandler(db paymentStore) *EventHandler {
	return &EventHandler{db: db}
}

// HandlePaymentRejected reverses a payment booking could not use, for example because the hold expired first.
func (h *EventHandler) HandlePaymentRejected(ctx context.Context, d messaging.Delivery) error {
	var event paymentRejectedEvent
	if err := json.Unmarshal(d.Body, &event); err != nil {
		return fmt.Errorf("decode %s message %s: %w: %w", eventPaymentRejected, d.ID, messaging.ErrPermanent, err)
	}

	data := event.Data
	if !validUUID(data.PaymentID) {
		return fmt.Errorf("%s message %s has payment_id %q: %w", eventPaymentRejected, d.ID, data.PaymentID, messaging.ErrPermanent)
	}

	_, err := h.db.ReversePayment(ctx, data.PaymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reverse payment %s: %w", data.PaymentID, err)
	}
	return nil
}
