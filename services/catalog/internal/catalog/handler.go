package catalog

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
)

type Service interface {
	CreateFlight(ctx context.Context, request CreateFlightRequest) (AdminFlightResponse, error)
	UpdateFlight(ctx context.Context, id string, request UpdateFlightRequest) (AdminFlightResponse, error)
	GetFlight(ctx context.Context, id string) (AdminFlightResponse, error)
	ListFlights(ctx context.Context, request ListFlightsRequest) (AdminFlightListResponse, error)
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
		r.Use(authn.RequireRole(authn.RoleAdmin))

		r.Get("/api/v1/admin/flights", h.ListFlights)
		r.Post("/api/v1/admin/flights", h.CreateFlight)
		r.Get("/api/v1/admin/flights/{id}", h.GetFlight)
		r.Patch("/api/v1/admin/flights/{id}", h.UpdateFlight)
	})
}

func (h *Handler) ListFlights(w http.ResponseWriter, r *http.Request) {
	request, err := parseListFlightsQuery(r.URL.Query())
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	response, err := h.service.ListFlights(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) CreateFlight(w http.ResponseWriter, r *http.Request) {
	var request CreateFlightRequest
	if err := httpserver.DecodeJSON(w, r, &request); err != nil {
		invalidBody(w)
		return
	}

	response, err := h.service.CreateFlight(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, response)
}

func (h *Handler) GetFlight(w http.ResponseWriter, r *http.Request) {
	response, err := h.service.GetFlight(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) UpdateFlight(w http.ResponseWriter, r *http.Request) {
	var request UpdateFlightRequest
	if err := httpserver.DecodeJSON(w, r, &request); err != nil {
		invalidBody(w)
		return
	}

	response, err := h.service.UpdateFlight(r.Context(), chi.URLParam(r, "id"), request)
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
	case errors.Is(err, ErrFlightNotFound):
		httpserver.WriteError(w, http.StatusNotFound, "NOT_FOUND", "flight not found")
	case errors.Is(err, ErrFlightExists):
		httpserver.WriteError(w, http.StatusConflict, "FLIGHT_EXISTS", "flight already exists for this departure time")
	case errors.Is(err, ErrCapacityBelowSold):
		httpserver.WriteError(w, http.StatusConflict, "CAPACITY_BELOW_SOLD", "capacity is lower than the number of seats already sold")
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

func invalidBody(w http.ResponseWriter) {
	httpserver.WriteError(w, http.StatusBadRequest, "VALIDATION_ERROR", "invalid request body")
}

func parseListFlightsQuery(query url.Values) (ListFlightsRequest, error) {
	request := ListFlightsRequest{
		Origin:      query.Get("origin"),
		Destination: query.Get("destination"),
		Page:        int32Param(query.Get("page"), defaultPage, maxPage),
		PageSize:    int32Param(query.Get("page_size"), defaultPageSize, maxPageSize),
	}

	var invalid ValidationError

	if raw := query.Get("date"); raw != "" {
		date, err := time.Parse(time.DateOnly, raw)
		invalid.check(err == nil, "date", "must be a date in YYYY-MM-DD format")
		if err == nil {
			request.Date = &date
		}
	}

	if err := request.Validate(); err != nil {
		var validationErr *ValidationError
		if errors.As(err, &validationErr) {
			invalid.Details = append(invalid.Details, validationErr.Details...)
		}
	}

	return request, invalid.err()
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
