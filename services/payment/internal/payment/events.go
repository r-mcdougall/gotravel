package payment

import (
	"context"
	"encoding/json"
	"fmt"

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
