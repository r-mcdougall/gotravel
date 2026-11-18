package payment

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
)

type Service interface {
	Pay(ctx context.Context, userID, authorization string, request PayRequest) (Response, error)
	GetPayment(ctx context.Context, userID, id string) (Response, error)
}

type Handler struct {
	service  Service
	log      *slog.Logger
	verifier *authn.Verifier
}

func NewHandler(service Service, log *slog.Logger, verifier *authn.Verifier) *Handler {
	return &Handler{
		service:  service,
		log:      log,
		verifier: verifier,
	}
}

func (h *Handler) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(authn.Authenticate(h.verifier))

		r.Post("/api/v1/payments", h.Pay)
		r.Get("/api/v1/payments/{id}", h.GetPayment)
	})
}

func (h *Handler) Pay(w http.ResponseWriter, r *http.Request) {
	var request PayRequest
	if err := httpserver.DecodeJSON(w, r, &request); err != nil {
		httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
		return
	}

	response, err := h.service.Pay(r.Context(), userID(r), r.Header.Get("Authorization"), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, response)
}

func (h *Handler) GetPayment(w http.ResponseWriter, r *http.Request) {
	response, err := h.service.GetPayment(r.Context(), userID(r), chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var validationErr *ValidationError

	switch {
	case errors.As(err, &validationErr):
		httpserver.WriteValidationError(w, validationErr.Details)
	case errors.Is(err, ErrNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "payment not found")
	case errors.Is(err, ErrBookingNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "booking not found")
	case errors.Is(err, ErrBookingNotPayable):
		httpserver.WriteError(w, http.StatusConflict, "BOOKING_NOT_PAYABLE", "only a held booking that has not expired can be paid")
	case errors.Is(err, ErrAlreadyPaid):
		httpserver.WriteError(w, http.StatusConflict, "ALREADY_PAID", "the booking is already paid")
	case errors.Is(err, ErrBookingUnavailable):
		h.logError(r, "booking service unavailable", err)
		httpserver.WriteError(w, http.StatusServiceUnavailable, "BOOKING_UNAVAILABLE", "the booking service is unavailable, try again later")
	default:
		h.logError(r, "request failed", err)
		httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
	}
}

func (h *Handler) logError(r *http.Request, message string, err error) {
	h.log.ErrorContext(r.Context(), message,
		slog.String("request_id", httpserver.RequestIDFrom(r.Context())),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("error", err),
	)
}

func userID(r *http.Request) string {
	identity, _ := authn.IdentityFrom(r.Context())
	return identity.UserID
}
