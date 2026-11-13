package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"
)

const createFlightBody = `{
	"flight_number": "GA410",
	"airline_code": "GA",
	"origin_code": "CGK",
	"destination_code": "DPS",
	"departure_at": "2026-11-20T06:00:00+07:00",
	"arrival_at": "2026-11-20T08:55:00+08:00",
	"price": 1250000,
	"capacity": 180
}`

const flightID = "0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"

type fakeService struct {
	t               *testing.T
	createFlight    func(ctx context.Context, request CreateFlightRequest) (AdminFlightResponse, error)
	updateFlight    func(ctx context.Context, id string, request UpdateFlightRequest) (AdminFlightResponse, error)
	getFlight       func(ctx context.Context, id string) (AdminFlightResponse, error)
	listFlights     func(ctx context.Context, request ListFlightsRequest) (AdminFlightListResponse, error)
	searchFlights   func(ctx context.Context, request SearchFlightsRequest) (FlightListResponse, error)
	getFlightDetail func(ctx context.Context, id string) (FlightResponse, error)
}

func (f *fakeService) CreateFlight(ctx context.Context, request CreateFlightRequest) (AdminFlightResponse, error) {
	if f.createFlight == nil {
		f.t.Fatal("unexpected call to CreateFlight")
	}
	return f.createFlight(ctx, request)
}

func (f *fakeService) UpdateFlight(ctx context.Context, id string, request UpdateFlightRequest) (AdminFlightResponse, error) {
	if f.updateFlight == nil {
		f.t.Fatal("unexpected call to UpdateFlight")
	}
	return f.updateFlight(ctx, id, request)
}

func (f *fakeService) GetFlight(ctx context.Context, id string) (AdminFlightResponse, error) {
	if f.getFlight == nil {
		f.t.Fatal("unexpected call to GetFlight")
	}
	return f.getFlight(ctx, id)
}

func (f *fakeService) ListFlights(ctx context.Context, request ListFlightsRequest) (AdminFlightListResponse, error) {
	if f.listFlights == nil {
		f.t.Fatal("unexpected call to ListFlights")
	}
	return f.listFlights(ctx, request)
}

func (f *fakeService) SearchFlights(ctx context.Context, request SearchFlightsRequest) (FlightListResponse, error) {
	if f.searchFlights == nil {
		f.t.Fatal("unexpected call to SearchFlights")
	}
	return f.searchFlights(ctx, request)
}

func (f *fakeService) GetFlightDetail(ctx context.Context, id string) (FlightResponse, error) {
	if f.getFlightDetail == nil {
		f.t.Fatal("unexpected call to GetFlightDetail")
	}
	return f.getFlightDetail(ctx, id)
}

func validationError(field, message string) *ValidationError {
	var v ValidationError
	v.check(false, field, message)
	return &v
}

type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		} `json:"details"`
	} `json:"error"`
}

func (e errorBody) fields() []string {
	var fields []string // nil when the error has no details
	for _, detail := range e.Error.Details {
		fields = append(fields, detail.Field)
	}
	return fields
}

func sampleAdminFlight() AdminFlightResponse {
	return AdminFlightResponse{
		ID:             flightID,
		FlightNumber:   "GA410",
		Airline:        AirlineResponse{Code: "GA", Name: "Garuda Indonesia"},
		Origin:         AirportResponse{Code: "CGK", Name: "Soekarno-Hatta International Airport", City: "Jakarta"},
		Destination:    AirportResponse{Code: "DPS", Name: "I Gusti Ngurah Rai International Airport", City: "Denpasar"},
		DepartureAt:    time.Date(2026, 11, 20, 6, 0, 0, 0, wib),
		ArrivalAt:      time.Date(2026, 11, 20, 8, 55, 0, 0, wita),
		Price:          1_250_000,
		Capacity:       180,
		AvailableSeats: 180,
	}
}

// callHandler runs one handler method directly. A non-empty id is set as the
// {id} route parameter, the way the router would.
func callHandler(t *testing.T, call http.HandlerFunc, method, target, body, id string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	if id != "" {
		routeContext := chi.NewRouteContext()
		routeContext.URLParams.Add("id", id)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext))
	}

	rec := httptest.NewRecorder()
	call(rec, req)
	return rec
}

func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()

	var v T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &v), rec.Body.String())
	return v
}

func newTestHandler(svc *fakeService) *Handler {
	return NewHandler(svc, slog.New(slog.DiscardHandler), nil)
}

func TestCreateFlight_returns201WithTheCreatedFlight(t *testing.T) {
	var got CreateFlightRequest
	svc := &fakeService{t: t, createFlight: func(_ context.Context, request CreateFlightRequest) (AdminFlightResponse, error) {
		got = request
		return sampleAdminFlight(), nil
	}}

	rec := callHandler(t, newTestHandler(svc).CreateFlight, http.MethodPost, "/api/v1/admin/flights", createFlightBody, "")

	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, toJSON(t, sampleAdminFlight()), rec.Body.String())
	require.Equal(t, "GA410", got.FlightNumber)
	require.Equal(t, "DPS", got.DestinationCode)
	require.True(t, validCreateRequest().DepartureAt.Equal(got.DepartureAt))
	require.True(t, validCreateRequest().ArrivalAt.Equal(got.ArrivalAt))
	require.Equal(t, int64(1_250_000), got.Price)
	require.Equal(t, int32(180), got.Capacity)
}

func TestCreateFlight_rejectsABodyItCannotDecodeWithoutCallingTheService(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"malformed json", `{"flight_number":`},
		{"unknown field", `{"flight_number":"GA410","seat_map":true}`},
		{"wrong type", `{"price":"cheap"}`},
		{"bad timestamp", `{"departure_at":"tomorrow morning"}`},
		{"two json values", createFlightBody + createFlightBody},
		{"empty body", ``},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := callHandler(t, newTestHandler(&fakeService{t: t}).CreateFlight, http.MethodPost, "/api/v1/admin/flights", tc.body, "")

			require.Equal(t, http.StatusBadRequest, rec.Code)
			body := decodeBody[errorBody](t, rec)
			require.Equal(t, "VALIDATION_ERROR", body.Error.Code)
			require.Equal(t, "invalid request body", body.Error.Message)
		})
	}
}

func TestCreateFlight_mapsServiceErrorsToHTTPErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantFields []string
	}{
		{"validation error", validationError("airline_code", "unknown airline"),
			http.StatusBadRequest, "VALIDATION_ERROR", []string{"airline_code"}},
		{"duplicate flight", ErrFlightExists, http.StatusConflict, "FLIGHT_EXISTS", nil},
		{"wrapped duplicate flight", fmt.Errorf("create flight: %w", ErrFlightExists), http.StatusConflict, "FLIGHT_EXISTS", nil},
		{"unexpected error", errBoom, http.StatusInternalServerError, "INTERNAL_ERROR", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{t: t, createFlight: func(context.Context, CreateFlightRequest) (AdminFlightResponse, error) {
				return AdminFlightResponse{}, tc.err
			}}

			rec := callHandler(t, newTestHandler(svc).CreateFlight, http.MethodPost, "/api/v1/admin/flights", createFlightBody, "")

			require.Equal(t, tc.wantStatus, rec.Code)
			body := decodeBody[errorBody](t, rec)
			require.Equal(t, tc.wantCode, body.Error.Code)
			require.Equal(t, tc.wantFields, body.fields())
		})
	}
}

func TestCreateFlight_doesNotLeakAnUnexpectedErrorToTheClient(t *testing.T) {
	svc := &fakeService{t: t, createFlight: func(context.Context, CreateFlightRequest) (AdminFlightResponse, error) {
		return AdminFlightResponse{}, errors.New("pq: password authentication failed for user catalog")
	}}

	rec := callHandler(t, newTestHandler(svc).CreateFlight, http.MethodPost, "/api/v1/admin/flights", createFlightBody, "")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.NotContains(t, rec.Body.String(), "password")
	require.Equal(t, "internal server error", decodeBody[errorBody](t, rec).Error.Message)
}

func TestGetFlight_returnsTheFlightForTheIDInThePath(t *testing.T) {
	var gotID string
	svc := &fakeService{t: t, getFlight: func(_ context.Context, id string) (AdminFlightResponse, error) {
		gotID = id
		return sampleAdminFlight(), nil
	}}

	rec := callHandler(t, newTestHandler(svc).GetFlight, http.MethodGet, "/api/v1/admin/flights/"+flightID, "", flightID)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, flightID, gotID)
	require.JSONEq(t, toJSON(t, sampleAdminFlight()), rec.Body.String())
}

func TestGetFlight_returns404WhenTheFlightDoesNotExist(t *testing.T) {
	svc := &fakeService{t: t, getFlight: func(context.Context, string) (AdminFlightResponse, error) {
		return AdminFlightResponse{}, ErrFlightNotFound
	}}

	rec := callHandler(t, newTestHandler(svc).GetFlight, http.MethodGet, "/api/v1/admin/flights/"+flightID, "", flightID)

	require.Equal(t, http.StatusNotFound, rec.Code)
	body := decodeBody[errorBody](t, rec)
	require.Equal(t, "NOT_FOUND", body.Error.Code)
	require.Equal(t, "flight not found", body.Error.Message)
}

func TestUpdateFlight_passesTheIDAndOnlyTheSentFieldsToTheService(t *testing.T) {
	var (
		gotID      string
		gotRequest UpdateFlightRequest
	)
	svc := &fakeService{t: t, updateFlight: func(_ context.Context, id string, request UpdateFlightRequest) (AdminFlightResponse, error) {
		gotID, gotRequest = id, request
		return sampleAdminFlight(), nil
	}}

	rec := callHandler(t, newTestHandler(svc).UpdateFlight, http.MethodPatch, "/api/v1/admin/flights/"+flightID, `{"price": 900000}`, flightID)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, flightID, gotID)
	require.Equal(t, UpdateFlightRequest{Price: ptr(int64(900_000))}, gotRequest)
	require.JSONEq(t, toJSON(t, sampleAdminFlight()), rec.Body.String())
}

func TestUpdateFlight_rejectsFieldsThatCannotChange(t *testing.T) {
	for _, field := range []string{"flight_number", "airline_code", "origin_code", "destination_code", "available_seats"} {
		t.Run(field, func(t *testing.T) {
			body := fmt.Sprintf(`{"%s": "XX"}`, field)

			rec := callHandler(t, newTestHandler(&fakeService{t: t}).UpdateFlight, http.MethodPatch, "/api/v1/admin/flights/"+flightID, body, flightID)

			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.Equal(t, "VALIDATION_ERROR", decodeBody[errorBody](t, rec).Error.Code)
		})
	}
}

func TestUpdateFlight_mapsServiceErrorsToHTTPErrors(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
		wantFields []string
	}{
		{"validation error", validationError("body", "at least one field is required"),
			http.StatusBadRequest, "VALIDATION_ERROR", []string{"body"}},
		{"flight not found", ErrFlightNotFound, http.StatusNotFound, "NOT_FOUND", nil},
		{"schedule collides", ErrFlightExists, http.StatusConflict, "FLIGHT_EXISTS", nil},
		{"capacity below sold seats", ErrCapacityBelowSold, http.StatusConflict, "CAPACITY_BELOW_SOLD", nil},
		{"unexpected error", errBoom, http.StatusInternalServerError, "INTERNAL_ERROR", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{t: t, updateFlight: func(context.Context, string, UpdateFlightRequest) (AdminFlightResponse, error) {
				return AdminFlightResponse{}, tc.err
			}}

			rec := callHandler(t, newTestHandler(svc).UpdateFlight, http.MethodPatch, "/api/v1/admin/flights/"+flightID, `{"capacity": 100}`, flightID)

			require.Equal(t, tc.wantStatus, rec.Code)
			body := decodeBody[errorBody](t, rec)
			require.Equal(t, tc.wantCode, body.Error.Code)
			require.Equal(t, tc.wantFields, body.fields())
		})
	}
}

func TestListFlights_appliesPagingDefaultsWhenTheClientSendsNoQuery(t *testing.T) {
	var got ListFlightsRequest
	svc := &fakeService{t: t, listFlights: func(_ context.Context, request ListFlightsRequest) (AdminFlightListResponse, error) {
		got = request
		return AdminFlightListResponse{Data: []AdminFlightResponse{}, Meta: PageMeta{Page: 1, PageSize: 20}}, nil
	}}

	rec := callHandler(t, newTestHandler(svc).ListFlights, http.MethodGet, "/api/v1/admin/flights", "", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, ListFlightsRequest{Page: 1, PageSize: 20}, got)
	require.JSONEq(t, `{"data": [], "meta": {"page": 1, "page_size": 20, "total": 0}}`, rec.Body.String())
}

func TestListFlights_passesEveryFilterToTheService(t *testing.T) {
	var got ListFlightsRequest
	svc := &fakeService{t: t, listFlights: func(_ context.Context, request ListFlightsRequest) (AdminFlightListResponse, error) {
		got = request
		return AdminFlightListResponse{Data: []AdminFlightResponse{sampleAdminFlight()}, Meta: PageMeta{Page: 2, PageSize: 50, Total: 51}}, nil
	}}

	rec := callHandler(t, newTestHandler(svc).ListFlights, http.MethodGet,
		"/api/v1/admin/flights?origin=CGK&destination=DPS&date=2026-11-20&page=2&page_size=50", "", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	wantDate := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	require.Equal(t, ListFlightsRequest{Origin: "CGK", Destination: "DPS", Date: &wantDate, Page: 2, PageSize: 50}, got)
	require.JSONEq(t, toJSON(t, AdminFlightListResponse{
		Data: []AdminFlightResponse{sampleAdminFlight()},
		Meta: PageMeta{Page: 2, PageSize: 50, Total: 51},
	}), rec.Body.String())
}

func TestListFlights_rejectsInvalidQueryParametersWithoutCallingTheService(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantFields []string
	}{
		{"date in another format", "date=20-11-2026", []string{"date"}},
		{"date that does not exist", "date=2026-02-30", []string{"date"}},
		{"lower case origin", "origin=cgk", []string{"origin"}},
		{"airport code too long", "destination=CGKX", []string{"destination"}},
		{"page is not a number", "page=first", []string{"page"}},
		{"page is zero", "page=0", []string{"page"}},
		{"page is negative", "page=-1", []string{"page"}},
		{"page is above the maximum", "page=1000001", []string{"page"}},
		{"page overflows int32", "page=99999999999", []string{"page"}},
		{"page size is above the maximum", "page_size=101", []string{"page_size"}},
		{"page size is zero", "page_size=0", []string{"page_size"}},
		{"every problem is reported together", "origin=cgk&date=soon&page=0&page_size=500",
			[]string{"date", "origin", "page", "page_size"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := callHandler(t, newTestHandler(&fakeService{t: t}).ListFlights, http.MethodGet, "/api/v1/admin/flights?"+tc.query, "", "")

			require.Equal(t, http.StatusBadRequest, rec.Code)
			body := decodeBody[errorBody](t, rec)
			require.Equal(t, "VALIDATION_ERROR", body.Error.Code)
			require.Equal(t, tc.wantFields, body.fields())
		})
	}
}

func TestListFlights_returns500WhenTheServiceFails(t *testing.T) {
	svc := &fakeService{t: t, listFlights: func(context.Context, ListFlightsRequest) (AdminFlightListResponse, error) {
		return AdminFlightListResponse{}, errBoom
	}}

	rec := callHandler(t, newTestHandler(svc).ListFlights, http.MethodGet, "/api/v1/admin/flights", "", "")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "INTERNAL_ERROR", decodeBody[errorBody](t, rec).Error.Code)
}

func sampleFlightResponse() FlightResponse {
	admin := sampleAdminFlight()
	return FlightResponse{
		ID:              admin.ID,
		FlightNumber:    admin.FlightNumber,
		Airline:         admin.Airline,
		Origin:          admin.Origin,
		Destination:     admin.Destination,
		DepartureAt:     admin.DepartureAt,
		ArrivalAt:       admin.ArrivalAt,
		DurationMinutes: 115,
		Price:           admin.Price,
		AvailableSeats:  admin.AvailableSeats,
	}
}

const (
	validSearchQuery = "origin=CGK&destination=DPS&date=2026-11-20"
	searchQuery      = "/api/v1/flights?" + validSearchQuery
)

func TestSearchFlights_appliesTheDefaultsWhenOnlyTheRequiredParametersAreSent(t *testing.T) {
	var got SearchFlightsRequest
	svc := &fakeService{t: t, searchFlights: func(_ context.Context, request SearchFlightsRequest) (FlightListResponse, error) {
		got = request
		return FlightListResponse{Data: []FlightResponse{}, Meta: PageMeta{Page: 1, PageSize: 20}}, nil
	}}

	rec := callHandler(t, newTestHandler(svc).SearchFlights, http.MethodGet, searchQuery, "", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, validSearchRequest(), got)
	require.JSONEq(t, `{"data": [], "meta": {"page": 1, "page_size": 20, "total": 0}}`, rec.Body.String())
}

func TestSearchFlights_passesEveryParameterToTheService(t *testing.T) {
	var got SearchFlightsRequest
	svc := &fakeService{t: t, searchFlights: func(_ context.Context, request SearchFlightsRequest) (FlightListResponse, error) {
		got = request
		return FlightListResponse{Data: []FlightResponse{sampleFlightResponse()}, Meta: PageMeta{Page: 2, PageSize: 5, Total: 6}}, nil
	}}
	target := searchQuery + "&passengers=3&airlines=GA,JT&min_price=500000&max_price=1500000" +
		"&departure_from=06:00&departure_to=18:30&sort=price_desc&page=2&page_size=5"

	rec := callHandler(t, newTestHandler(svc).SearchFlights, http.MethodGet, target, "", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	want := validSearchRequest()
	want.Passengers = 3
	want.Airlines = []string{"GA", "JT"}
	want.MinPrice = ptr(int64(500_000))
	want.MaxPrice = ptr(int64(1_500_000))
	want.DepartureFrom = ptr(6 * time.Hour)
	want.DepartureTo = ptr(18*time.Hour + 30*time.Minute)
	want.Sort = sortPriceDesc
	want.Page = 2
	want.PageSize = 5
	require.Equal(t, want, got)
	require.JSONEq(t, toJSON(t, FlightListResponse{
		Data: []FlightResponse{sampleFlightResponse()},
		Meta: PageMeta{Page: 2, PageSize: 5, Total: 6},
	}), rec.Body.String())
}

func TestSearchFlights_acceptsTheWholeDayAsTheTimeWindow(t *testing.T) {
	var got SearchFlightsRequest
	svc := &fakeService{t: t, searchFlights: func(_ context.Context, request SearchFlightsRequest) (FlightListResponse, error) {
		got = request
		return FlightListResponse{Data: []FlightResponse{}}, nil
	}}

	rec := callHandler(t, newTestHandler(svc).SearchFlights, http.MethodGet, searchQuery+"&departure_from=00:00&departure_to=23:59", "", "")

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, time.Duration(0), *got.DepartureFrom)
	require.Equal(t, 23*time.Hour+59*time.Minute, *got.DepartureTo)
}

func TestSearchFlights_rejectsInvalidQueryParametersWithoutCallingTheService(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantFields []string
	}{
		{"nothing sent", "", []string{"origin", "destination", "date"}},
		{"date is missing", "origin=CGK&destination=DPS", []string{"date"}},
		{"date in another format", "origin=CGK&destination=DPS&date=20-11-2026", []string{"date"}},
		{"date that does not exist", "origin=CGK&destination=DPS&date=2026-02-30", []string{"date"}},
		{"lower case origin", "origin=cgk&destination=DPS&date=2026-11-20", []string{"origin"}},
		{"same origin and destination", "origin=CGK&destination=CGK&date=2026-11-20", []string{"destination"}},
		{"passengers is not a number", validSearchQuery + "&passengers=two", []string{"passengers"}},
		{"passengers is zero", validSearchQuery + "&passengers=0", []string{"passengers"}},
		{"passengers is above the maximum", validSearchQuery + "&passengers=10", []string{"passengers"}},
		{"airline code in lower case", validSearchQuery + "&airlines=ga", []string{"airlines"}},
		{"airline list with an empty entry", validSearchQuery + "&airlines=GA,", []string{"airlines"}},
		{"airline list with a space", validSearchQuery + "&airlines=GA,%20JT", []string{"airlines"}},
		{"min price is not a number", validSearchQuery + "&min_price=cheap", []string{"min_price"}},
		{"an unreadable max price is not also reported as below min price", validSearchQuery + "&min_price=500&max_price=cheap", []string{"max_price"}},
		{"min price has decimals", validSearchQuery + "&min_price=10.5", []string{"min_price"}},
		{"max price is negative", validSearchQuery + "&max_price=-1", []string{"max_price"}},
		{"max price is below min price", validSearchQuery + "&min_price=900000&max_price=100000", []string{"max_price"}},
		{"time without a leading zero", validSearchQuery + "&departure_from=7:30", []string{"departure_from"}},
		{"time that is not a clock time", validSearchQuery + "&departure_from=noon", []string{"departure_from"}},
		{"hour 24", validSearchQuery + "&departure_to=24:00", []string{"departure_to"}},
		{"minute 60", validSearchQuery + "&departure_to=10:60", []string{"departure_to"}},
		{"time window ends before it starts", validSearchQuery + "&departure_from=18:00&departure_to=06:00", []string{"departure_to"}},
		{"unknown sort", validSearchQuery + "&sort=cheapest", []string{"sort"}},
		{"page is zero", validSearchQuery + "&page=0", []string{"page"}},
		{"page size is above the maximum", validSearchQuery + "&page_size=101", []string{"page_size"}},
		{"every problem is reported together",
			"origin=cgk&destination=DPS&date=soon&min_price=x&departure_from=noon&sort=none&page=0",
			[]string{"date", "min_price", "departure_from", "origin", "sort", "page"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := callHandler(t, newTestHandler(&fakeService{t: t}).SearchFlights, http.MethodGet, "/api/v1/flights?"+tc.query, "", "")

			require.Equal(t, http.StatusBadRequest, rec.Code)
			body := decodeBody[errorBody](t, rec)
			require.Equal(t, "VALIDATION_ERROR", body.Error.Code)
			require.Equal(t, tc.wantFields, body.fields())
		})
	}
}

func TestSearchFlights_returns500WhenTheServiceFails(t *testing.T) {
	svc := &fakeService{t: t, searchFlights: func(context.Context, SearchFlightsRequest) (FlightListResponse, error) {
		return FlightListResponse{}, errBoom
	}}

	rec := callHandler(t, newTestHandler(svc).SearchFlights, http.MethodGet, searchQuery, "", "")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "INTERNAL_ERROR", decodeBody[errorBody](t, rec).Error.Code)
}

func TestGetFlightDetail_returnsThePublicViewForTheIDInThePath(t *testing.T) {
	svc := &fakeService{t: t, getFlightDetail: func(_ context.Context, id string) (FlightResponse, error) {
		require.Equal(t, flightID, id)
		return sampleFlightResponse(), nil
	}}

	rec := callHandler(t, newTestHandler(svc).GetFlightDetail, http.MethodGet, "/api/v1/flights/"+flightID, "", flightID)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(t, toJSON(t, sampleFlightResponse()), rec.Body.String())
	require.NotContains(t, rec.Body.String(), "capacity")
}

func TestGetFlightDetail_returns404WhenTheFlightDoesNotExist(t *testing.T) {
	svc := &fakeService{t: t, getFlightDetail: func(context.Context, string) (FlightResponse, error) {
		return FlightResponse{}, ErrFlightNotFound
	}}

	rec := callHandler(t, newTestHandler(svc).GetFlightDetail, http.MethodGet, "/api/v1/flights/"+flightID, "", flightID)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "NOT_FOUND", decodeBody[errorBody](t, rec).Error.Code)
}

func TestRoutes_servesSearchAndDetailWithoutAToken(t *testing.T) {
	svc := &fakeService{
		t: t,
		searchFlights: func(context.Context, SearchFlightsRequest) (FlightListResponse, error) {
			return FlightListResponse{Data: []FlightResponse{}}, nil
		},
		getFlightDetail: func(context.Context, string) (FlightResponse, error) {
			return sampleFlightResponse(), nil
		},
	}
	router := chi.NewRouter()
	newTestHandler(svc).Routes(router)

	for _, target := range []string{searchQuery, "/api/v1/flights/" + flightID} {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, http.NoBody)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code, target)
	}
}
