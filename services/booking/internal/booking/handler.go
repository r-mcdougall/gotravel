package booking

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
)

type Service interface {
	Hold(ctx context.Context, userID string, request HoldRequest) (Response, error)
	GetBooking(ctx context.Context, userID, id string) (Response, error)
	ListBookings(ctx context.Context, userID string, request ListRequest) (ListResponse, error)
	CancelBooking(ctx context.Context, userID, id string) (Response, error)
	Analytics(ctx context.Context, request AnalyticsRequest) (Analytics, error)
	FlightSeats(ctx context.Context, flightID string) (FlightSeats, error)

	HoldRoom(ctx context.Context, userID string, request HotelHoldRequest) (HotelBookingResponse, error)
	GetHotelBooking(ctx context.Context, userID, id string) (HotelBookingResponse, error)
	ListHotelBookings(ctx context.Context, userID string, request ListRequest) (HotelBookingListResponse, error)
	CancelHotelBooking(ctx context.Context, userID, id string) (HotelBookingResponse, error)
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

		r.Post("/api/v1/bookings", h.Hold)
		r.Get("/api/v1/bookings", h.ListBookings)
		r.Get("/api/v1/bookings/{id}", h.GetBooking)
		r.Post("/api/v1/bookings/{id}/cancel", h.CancelBooking)

		r.Post("/api/v1/hotel-bookings", h.HoldRoom)
		r.Get("/api/v1/hotel-bookings", h.ListHotelBookings)
		r.Get("/api/v1/hotel-bookings/{id}", h.GetHotelBooking)
		r.Post("/api/v1/hotel-bookings/{id}/cancel", h.CancelHotelBooking)

		r.Group(func(r chi.Router) {
			r.Use(authn.RequireRole(authn.RoleAdmin))

			r.Get("/api/v1/admin/analytics/bookings", h.Analytics)
			// Internal: catalog calls it with the admin's token; the gateway does not route /internal.
			r.Get("/internal/v1/flights/{id}/seats", h.FlightSeats)
		})
	})
}

func (h *Handler) Hold(w http.ResponseWriter, r *http.Request) {
	var request HoldRequest
	if err := httpserver.DecodeJSON(w, r, &request); err != nil {
		invalidBody(w)
		return
	}

	response, err := h.service.Hold(r.Context(), userID(r), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, response)
}

func (h *Handler) ListBookings(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	request := ListRequest{
		Page:     int32Param(query.Get("page"), defaultPage, maxPage),
		PageSize: int32Param(query.Get("page_size"), defaultPageSize, maxPageSize),
	}

	response, err := h.service.ListBookings(r.Context(), userID(r), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) Analytics(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	request := AnalyticsRequest{From: query.Get("from"), To: query.Get("to")}

	response, err := h.service.Analytics(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) FlightSeats(w http.ResponseWriter, r *http.Request) {
	response, err := h.service.FlightSeats(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) GetBooking(w http.ResponseWriter, r *http.Request) {
	response, err := h.service.GetBooking(r.Context(), userID(r), chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) CancelBooking(w http.ResponseWriter, r *http.Request) {
	response, err := h.service.CancelBooking(r.Context(), userID(r), chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) HoldRoom(w http.ResponseWriter, r *http.Request) {
	var request HotelHoldRequest
	if err := httpserver.DecodeJSON(w, r, &request); err != nil {
		invalidBody(w)
		return
	}

	response, err := h.service.HoldRoom(r.Context(), userID(r), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, response)
}

func (h *Handler) ListHotelBookings(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	request := ListRequest{
		Page:     int32Param(query.Get("page"), defaultPage, maxPage),
		PageSize: int32Param(query.Get("page_size"), defaultPageSize, maxPageSize),
	}

	response, err := h.service.ListHotelBookings(r.Context(), userID(r), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) GetHotelBooking(w http.ResponseWriter, r *http.Request) {
	response, err := h.service.GetHotelBooking(r.Context(), userID(r), chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) CancelHotelBooking(w http.ResponseWriter, r *http.Request) {
	response, err := h.service.CancelHotelBooking(r.Context(), userID(r), chi.URLParam(r, "id"))
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
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "booking not found")
	case errors.Is(err, ErrFlightNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "flight not found")
	case errors.Is(err, ErrFlightDeparted):
		httpserver.WriteError(w, http.StatusConflict, "FLIGHT_DEPARTED", "the flight has already departed")
	case errors.Is(err, ErrNotEnoughSeats):
		httpserver.WriteError(w, http.StatusConflict, "NOT_ENOUGH_SEATS", "not enough seats are available for every passenger")
	case errors.Is(err, ErrRoomTypeNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "room type not found")
	case errors.Is(err, ErrRoomsUnavailable):
		httpserver.WriteError(w, http.StatusConflict, "ROOMS_UNAVAILABLE", "no room of this type is free on every night of the stay")
	case errors.Is(err, ErrNotHeld):
		httpserver.WriteError(w, http.StatusConflict, "BOOKING_NOT_HELD", "only a held booking can be cancelled")
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.log.ErrorContext(r.Context(), "request failed",
		slog.String("request_id", httpserver.RequestIDFrom(r.Context())),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("error", err),
	)
	httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
}

func userID(r *http.Request) string {
	identity, _ := authn.IdentityFrom(r.Context())
	return identity.UserID
}

func invalidBody(w http.ResponseWriter) {
	httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
}

func int32Param(raw string, fallback, upper int32) int32 {
	if raw == "" {
		return fallback
	}

	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || n < 1 || n > int64(upper) {
		return 0
	}

	return int32(n)
}
