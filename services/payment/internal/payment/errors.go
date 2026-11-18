package payment

import (
	"errors"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
)

var (
	ErrNotFound           = errors.New("payment not found")
	ErrAlreadyPaid        = errors.New("booking is already paid")
	ErrBookingNotFound    = errors.New("booking not found")
	ErrBookingNotPayable  = errors.New("booking cannot be paid")
	ErrBookingUnavailable = errors.New("booking service unavailable")
)

type ValidationError struct {
	Details []httpserver.ErrorDetail
}

func (e *ValidationError) Error() string {
	return "validation failed"
}

func (e *ValidationError) check(ok bool, field, message string) {
	if !ok {
		e.Details = append(e.Details, httpserver.ErrorDetail{Field: field, Message: message})
	}
}

func (e *ValidationError) err() error {
	if len(e.Details) == 0 {
		return nil
	}
	return e
}
