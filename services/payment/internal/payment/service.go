package payment

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/r-mcdougall/gotravel/services/payment/internal/store"
)

const (
	uniqueViolation      = "23505"
	oneSuccessPerBooking = "payments_one_success_per_booking"
)

type queries interface {
	CreatePayment(ctx context.Context, arg store.CreatePaymentParams) (store.Payment, error)
	GetPayment(ctx context.Context, arg store.GetPaymentParams) (store.Payment, error)
	ReversePayment(ctx context.Context, id string) (store.Payment, error)
	InsertOutboxEvent(ctx context.Context, arg store.InsertOutboxEventParams) error
}

var _ queries = (*store.Queries)(nil)

type paymentStore interface {
	queries
	InTx(ctx context.Context, fn func(queries) error) error
}

type billReader interface {
	GetBill(ctx context.Context, authorization, id string) (Bill, error)
}

type service struct {
	db    paymentStore
	bills billReader
	now   func() time.Time
}

func NewService(db paymentStore, bills billReader) Service {
	return newService(db, bills, time.Now)
}

func newService(db paymentStore, bills billReader, now func() time.Time) *service {
	return &service{db: db, bills: bills, now: now}
}

func (s *service) Pay(ctx context.Context, userID, authorization string, request PayRequest) (Response, error) {
	request.normalize()
	if err := request.Validate(); err != nil {
		return Response{}, fmt.Errorf("validate request: %w", err)
	}

	bill, err := s.bills.GetBill(ctx, authorization, request.BookingID)
	if err != nil {
		return Response{}, fmt.Errorf("get bill: %w", err)
	}
	if bill.Status != bookingHeld || !bill.ExpiresAt.After(s.now()) {
		return Response{}, ErrBookingNotPayable
	}

	status := statusSucceeded
	if request.Outcome == outcomeFailed {
		status = statusFailed
	}

	var payment store.Payment
	err = s.db.InTx(ctx, func(q queries) error {
		var err error
		payment, err = q.CreatePayment(ctx, store.CreatePaymentParams{
			BookingID: bill.ID,
			UserID:    userID,
			Amount:    bill.TotalPrice,
			Status:    status,
		})
		if isSecondSuccess(err) {
			return ErrAlreadyPaid
		}
		if err != nil {
			return fmt.Errorf("create payment: %w", err)
		}

		if payment.Status != statusSucceeded {
			return nil
		}
		return recordPaymentSucceeded(ctx, q, payment)
	})
	if err != nil {
		return Response{}, err
	}

	return toResponse(payment), nil
}

func (s *service) GetPayment(ctx context.Context, userID, id string) (Response, error) {
	if !validUUID(id) {
		return Response{}, ErrNotFound
	}

	payment, err := s.db.GetPayment(ctx, store.GetPaymentParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Response{}, ErrNotFound
	}
	if err != nil {
		return Response{}, fmt.Errorf("get payment: %w", err)
	}

	return toResponse(payment), nil
}

func isSecondSuccess(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation && pgErr.ConstraintName == oneSuccessPerBooking
}

func toResponse(payment store.Payment) Response {
	return Response{
		ID:        payment.ID,
		BookingID: payment.BookingID,
		Amount:    payment.Amount,
		Status:    payment.Status,
		CreatedAt: payment.CreatedAt,
		UpdatedAt: payment.UpdatedAt,
	}
}
