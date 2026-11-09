package auth

import (
	"errors"
	"strings"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
)

var (
	ErrEmailTaken          = errors.New("email is already registered")
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrInvalidRefreshToken = errors.New("refresh token is invalid or expired")
	ErrUserNotFound        = errors.New("user not found")
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
