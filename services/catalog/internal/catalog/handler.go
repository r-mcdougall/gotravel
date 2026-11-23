package catalog

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
)

type Service interface {
	CreateFlight(ctx context.Context, request CreateFlightRequest) (AdminFlightResponse, error)
	UpdateFlight(ctx context.Context, id, authorization string, request UpdateFlightRequest) (AdminFlightResponse, error)
	GetFlight(ctx context.Context, id string) (AdminFlightResponse, error)
	ListFlights(ctx context.Context, request ListFlightsRequest) (AdminFlightListResponse, error)
	SearchFlights(ctx context.Context, request SearchFlightsRequest) (FlightListResponse, error)
	GetFlightDetail(ctx context.Context, id string) (FlightResponse, error)
	CreateHotel(ctx context.Context, request CreateHotelRequest) (AdminHotelResponse, error)
	SearchHotels(ctx context.Context, request SearchHotelsRequest) (HotelListResponse, error)
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
	r.Get("/api/v1/flights", h.SearchFlights)
	r.Get("/api/v1/flights/{id}", h.GetFlightDetail)
	r.Get("/api/v1/hotels", h.SearchHotels)

	r.Group(func(r chi.Router) {
		r.Use(authn.Authenticate(h.verifier))
		r.Use(authn.RequireRole(authn.RoleAdmin))

		r.Get("/api/v1/admin/flights", h.ListFlights)
		r.Post("/api/v1/admin/flights", h.CreateFlight)
		r.Get("/api/v1/admin/flights/{id}", h.GetFlight)
		r.Patch("/api/v1/admin/flights/{id}", h.UpdateFlight)
		r.Post("/api/v1/admin/hotels", h.CreateHotel)
	})
}

func (h *Handler) SearchFlights(w http.ResponseWriter, r *http.Request) {
	request, err := parseSearchFlightsQuery(r.URL.Query())
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	response, err := h.service.SearchFlights(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) GetFlightDetail(w http.ResponseWriter, r *http.Request) {
	response, err := h.service.GetFlightDetail(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
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

	response, err := h.service.UpdateFlight(r.Context(), chi.URLParam(r, "id"), r.Header.Get("Authorization"), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusOK, response)
}

func (h *Handler) CreateHotel(w http.ResponseWriter, r *http.Request) {
	var request CreateHotelRequest
	if err := httpserver.DecodeJSON(w, r, &request); err != nil {
		invalidBody(w)
		return
	}

	response, err := h.service.CreateHotel(r.Context(), request)
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	httpserver.WriteJSON(w, http.StatusCreated, response)
}

func (h *Handler) SearchHotels(w http.ResponseWriter, r *http.Request) {
	request, err := parseSearchHotelsQuery(r.URL.Query())
	if err != nil {
		h.writeError(w, r, err)
		return
	}

	response, err := h.service.SearchHotels(r.Context(), request)
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
	case errors.Is(err, ErrHotelExists):
		httpserver.WriteError(w, http.StatusConflict, "HOTEL_EXISTS", "hotel already exists in this city")
	case errors.Is(err, ErrCapacityBelowSold):
		httpserver.WriteError(w, http.StatusConflict, "CAPACITY_BELOW_SOLD", "capacity is lower than the seats already held or sold")
	case errors.Is(err, ErrBookingUnavailable):
		h.logError(r, "booking service unavailable", err)
		httpserver.WriteError(w, http.StatusServiceUnavailable, "BOOKING_UNAVAILABLE", "the booking service is unavailable, try again later")
	default:
		h.internalError(w, r, err)
	}
}

func (h *Handler) internalError(w http.ResponseWriter, r *http.Request, err error) {
	h.logError(r, "request failed", err)
	httpserver.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "internal server error")
}

func (h *Handler) logError(r *http.Request, message string, err error) {
	h.log.ErrorContext(r.Context(), message,
		slog.String("request_id", httpserver.RequestIDFrom(r.Context())),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Any("error", err),
	)
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

// parseSearchFlightsQuery turns the query string into a search request,
// applying the defaults for passengers, sort and paging. Every invalid
// parameter is reported together.
func parseSearchFlightsQuery(query url.Values) (SearchFlightsRequest, error) {
	request := SearchFlightsRequest{
		Origin:      query.Get("origin"),
		Destination: query.Get("destination"),
		Passengers:  int32Param(query.Get("passengers"), defaultPassengers, maxPassengers),
		Sort:        query.Get("sort"),
		Page:        int32Param(query.Get("page"), defaultPage, maxPage),
		PageSize:    int32Param(query.Get("page_size"), defaultPageSize, maxPageSize),
	}
	if request.Sort == "" {
		request.Sort = sortDepartureAsc
	}
	if raw := query.Get("airlines"); raw != "" {
		request.Airlines = strings.Split(raw, ",")
	}

	var invalid ValidationError

	rawDate := query.Get("date")
	if rawDate != "" {
		date, err := time.Parse(time.DateOnly, rawDate)
		invalid.check(err == nil, "date", "must be a date in YYYY-MM-DD format")
		if err == nil {
			request.Date = date
		}
	}

	request.MinPrice = priceParam(&invalid, query, "min_price")
	request.MaxPrice = priceParam(&invalid, query, "max_price")
	request.DepartureFrom = timeOfDayParam(&invalid, query, "departure_from")
	request.DepartureTo = timeOfDayParam(&invalid, query, "departure_to")

	if err := request.Validate(); err != nil {
		var validationErr *ValidationError
		if errors.As(err, &validationErr) {
			for _, detail := range validationErr.Details {
				if detail.Field == "date" && rawDate != "" {
					continue // a malformed date is already reported above
				}
				invalid.Details = append(invalid.Details, detail)
			}
		}
	}

	return request, invalid.err()
}

// parseSearchHotelsQuery turns the query string into a hotel search request,
// applying the defaults for guests, sort and paging. Every invalid parameter
// is reported together.
func parseSearchHotelsQuery(query url.Values) (SearchHotelsRequest, error) {
	request := SearchHotelsRequest{
		City:     query.Get("city"),
		Guests:   int32Param(query.Get("guests"), defaultGuests, maxGuests),
		Sort:     query.Get("sort"),
		Page:     int32Param(query.Get("page"), defaultPage, maxPage),
		PageSize: int32Param(query.Get("page_size"), defaultPageSize, maxPageSize),
	}
	if request.Sort == "" {
		request.Sort = sortPriceAsc
	}

	var invalid ValidationError
	malformed := make(map[string]bool)
	date := func(name string) time.Time {
		raw := query.Get(name)
		if raw == "" {
			return time.Time{}
		}
		parsed, err := time.Parse(time.DateOnly, raw)
		invalid.check(err == nil, name, "must be a date in YYYY-MM-DD format")
		malformed[name] = err != nil
		return parsed
	}
	request.CheckIn = date("check_in")
	request.CheckOut = date("check_out")
	request.MinPrice = priceParam(&invalid, query, "min_price")
	request.MaxPrice = priceParam(&invalid, query, "max_price")

	if err := request.Validate(); err != nil {
		var validationErr *ValidationError
		if errors.As(err, &validationErr) {
			for _, detail := range validationErr.Details {
				if malformed[detail.Field] {
					continue // a malformed date is already reported above
				}
				invalid.Details = append(invalid.Details, detail)
			}
		}
	}

	return request, invalid.err()
}

// priceParam reads an optional whole-number price. A value that is not a number is reported, and returns nil so Validate does not report it twice.
func priceParam(invalid *ValidationError, query url.Values, name string) *int64 {
	raw := query.Get(name)
	if raw == "" {
		return nil
	}

	price, err := strconv.ParseInt(raw, 10, 64)
	invalid.check(err == nil, name, "must be a whole number")
	if err != nil {
		return nil
	}

	return &price
}

// timeOfDayParam reads an optional HH:MM time as an offset from midnight.
func timeOfDayParam(invalid *ValidationError, query url.Values, name string) *time.Duration {
	raw := query.Get(name)
	if raw == "" {
		return nil
	}

	parsed, err := time.Parse("15:04", raw)
	valid := err == nil && len(raw) == len("15:04") // time.Parse also accepts "7:30"
	invalid.check(valid, name, timeOfDayMessage)
	if !valid {
		return nil
	}

	offset := time.Duration(parsed.Hour())*time.Hour + time.Duration(parsed.Minute())*time.Minute
	return &offset
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
