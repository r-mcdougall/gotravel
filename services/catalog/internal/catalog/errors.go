package catalog

import (
	"errors"
	"strings"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
)

var (
	ErrFlightNotFound     = errors.New("flight not found")
	ErrFlightExists       = errors.New("flight already exists for this departure time")
	ErrCapacityBelowSold  = errors.New("capacity is lower than the seats already held or sold")
	ErrBookingUnavailable = errors.New("booking service unavailable")
)

type ValidationError struct {
	Details []httpserver.ErrorDetail
}

func (e *ValidationError) Error() string {
	field := make([]string, len(e.Details))
	for i, detail := range e.Details {
		field[i] = detail.Field
	}

	return "invalid fields: " + strings.Join(field, ", ")
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
