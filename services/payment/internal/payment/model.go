package payment

import "time"

const (
	statusSucceeded  = "SUCCEEDED"
	statusFailed     = "FAILED"
	outcomeSucceeded = "succeeded"
	outcomeFailed    = "failed"
	bookingHeld      = "HELD"
)

type PayRequest struct {
	BookingID string `json:"booking_id"`
	Outcome   string `json:"outcome"`
}

type Response struct {
	ID        string    `json:"id"`
	BookingID string    `json:"booking_id"`
	Amount    int64     `json:"amount"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Booking is what payment reads from the booking service before charging.
type Booking struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	TotalPrice int64     `json:"total_price"`
	ExpiresAt  time.Time `json:"expires_at"`
}
