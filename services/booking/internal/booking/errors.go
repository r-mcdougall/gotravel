package booking

import (
	"errors"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
)

var (
	ErrNotFound       = errors.New("booking not found")
	ErrNotHeld        = errors.New("booking is not held")
	ErrFlightNotFound = errors.New("flight not found")
	ErrFlightDeparted = errors.New("flight has already departed")
	ErrNotEnoughSeats = errors.New("not enough seats available")
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
