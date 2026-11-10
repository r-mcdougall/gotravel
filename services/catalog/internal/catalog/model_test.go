package catalog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	wib  = time.FixedZone("WIB", 7*60*60)
	wita = time.FixedZone("WITA", 8*60*60)
)

func ptr[T any](v T) *T {
	return &v
}

func invalidFields(t *testing.T, err error) []string {
	t.Helper()

	var validationErr *ValidationError
	require.ErrorAs(t, err, &validationErr)

	fields := make([]string, len(validationErr.Details))
	for i, detail := range validationErr.Details {
		fields[i] = detail.Field
	}
	return fields
}

func validCreateRequest() CreateFlightRequest {
	return CreateFlightRequest{
		FlightNumber:    "GA410",
		AirlineCode:     "GA",
		OriginCode:      "CGK",
		DestinationCode: "DPS",
		DepartureAt:     time.Date(2026, 11, 20, 6, 0, 0, 0, wib),
		ArrivalAt:       time.Date(2026, 11, 20, 8, 55, 0, 0, wita),
		Price:           1_250_000,
		Capacity:        180,
	}
}

func TestCreateFlightRequestValidate_acceptsAValidRequest(t *testing.T) {
	request := validCreateRequest()

	require.NoError(t, request.Validate())
}

func TestCreateFlightRequestValidate_rejectsASingleInvalidField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *CreateFlightRequest)
		field  string
	}{
		{"lower case flight number", func(r *CreateFlightRequest) { r.FlightNumber = "ga410" }, "flight_number"},
		{"flight number without digits", func(r *CreateFlightRequest) { r.FlightNumber = "GA" }, "flight_number"},
		{"flight number with too many digits", func(r *CreateFlightRequest) { r.FlightNumber = "GA41000" }, "flight_number"},
		{"flight number of another airline", func(r *CreateFlightRequest) { r.FlightNumber = "JT410" }, "flight_number"},
		{"lower case airline code", func(r *CreateFlightRequest) { r.AirlineCode = "ga" }, "airline_code"},
		{"lower case origin", func(r *CreateFlightRequest) { r.OriginCode = "cgk" }, "origin_code"},
		{"origin with four letters", func(r *CreateFlightRequest) { r.OriginCode = "CGKX" }, "origin_code"},
		{"destination equal to origin", func(r *CreateFlightRequest) { r.DestinationCode = "CGK" }, "destination_code"},
		{"missing departure", func(r *CreateFlightRequest) { r.DepartureAt = time.Time{} }, "departure_at"},
		{"missing arrival", func(r *CreateFlightRequest) { r.ArrivalAt = time.Time{} }, "arrival_at"},
		{"arrival before departure", func(r *CreateFlightRequest) { r.ArrivalAt = r.DepartureAt.Add(-time.Minute) }, "arrival_at"},
		{"arrival equal to departure", func(r *CreateFlightRequest) { r.ArrivalAt = r.DepartureAt }, "arrival_at"},
		{"zero price", func(r *CreateFlightRequest) { r.Price = 0 }, "price"},
		{"negative price", func(r *CreateFlightRequest) { r.Price = -1 }, "price"},
		{"zero capacity", func(r *CreateFlightRequest) { r.Capacity = 0 }, "capacity"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := validCreateRequest()
			tc.mutate(&request)

			err := request.Validate()

			require.Equal(t, []string{tc.field}, invalidFields(t, err))
		})
	}
}

func TestCreateFlightRequestValidate_reportsEveryInvalidFieldOfAnEmptyRequest(t *testing.T) {
	request := CreateFlightRequest{}

	err := request.Validate()

	require.Equal(t, []string{
		"flight_number", "airline_code", "origin_code", "destination_code",
		"departure_at", "arrival_at", "price", "capacity",
	}, invalidFields(t, err))
}

func TestUpdateFlightRequestValidate_acceptsEachUpdatableField(t *testing.T) {
	tests := []struct {
		name    string
		request UpdateFlightRequest
	}{
		{"price only", UpdateFlightRequest{Price: ptr(int64(900_000))}},
		{"capacity only", UpdateFlightRequest{Capacity: ptr(int32(150))}},
		{"schedule only", UpdateFlightRequest{
			DepartureAt: ptr(time.Date(2026, 11, 20, 7, 0, 0, 0, wib)),
			ArrivalAt:   ptr(time.Date(2026, 11, 20, 9, 55, 0, 0, wita)),
		}},
		{"every field", UpdateFlightRequest{
			DepartureAt: ptr(time.Date(2026, 11, 20, 7, 0, 0, 0, wib)),
			ArrivalAt:   ptr(time.Date(2026, 11, 20, 9, 55, 0, 0, wita)),
			Price:       ptr(int64(900_000)),
			Capacity:    ptr(int32(150)),
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.request.Validate())
		})
	}
}

func TestUpdateFlightRequestValidate_rejectsASingleInvalidField(t *testing.T) {
	departure := time.Date(2026, 11, 20, 7, 0, 0, 0, wib)
	arrival := time.Date(2026, 11, 20, 9, 55, 0, 0, wita)

	tests := []struct {
		name    string
		request UpdateFlightRequest
		field   string
	}{
		{"empty body", UpdateFlightRequest{}, "body"},
		{"departure without arrival", UpdateFlightRequest{DepartureAt: &departure}, "arrival_at"},
		{"arrival without departure", UpdateFlightRequest{ArrivalAt: &arrival}, "departure_at"},
		{"arrival before departure", UpdateFlightRequest{DepartureAt: &arrival, ArrivalAt: &departure}, "arrival_at"},
		{"zero price", UpdateFlightRequest{Price: ptr(int64(0))}, "price"},
		{"zero capacity", UpdateFlightRequest{Capacity: ptr(int32(0))}, "capacity"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.request.Validate()

			require.Equal(t, []string{tc.field}, invalidFields(t, err))
		})
	}
}

func TestListFlightsRequestValidate_acceptsNoFiltersAndTheLimits(t *testing.T) {
	tests := []struct {
		name    string
		request ListFlightsRequest
	}{
		{"defaults", ListFlightsRequest{Page: defaultPage, PageSize: defaultPageSize}},
		{"every filter", ListFlightsRequest{Origin: "CGK", Destination: "DPS", Page: 2, PageSize: 50}},
		{"largest page and page size", ListFlightsRequest{Page: maxPage, PageSize: maxPageSize}},
		{"smallest page and page size", ListFlightsRequest{Page: 1, PageSize: 1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, tc.request.Validate())
		})
	}
}

func TestListFlightsRequestValidate_rejectsASingleInvalidField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *ListFlightsRequest)
		field  string
	}{
		{"lower case origin", func(r *ListFlightsRequest) { r.Origin = "cgk" }, "origin"},
		{"destination with four letters", func(r *ListFlightsRequest) { r.Destination = "DPSX" }, "destination"},
		{"page zero", func(r *ListFlightsRequest) { r.Page = 0 }, "page"},
		{"page above the limit", func(r *ListFlightsRequest) { r.Page = maxPage + 1 }, "page"},
		{"page size zero", func(r *ListFlightsRequest) { r.PageSize = 0 }, "page_size"},
		{"page size above the limit", func(r *ListFlightsRequest) { r.PageSize = maxPageSize + 1 }, "page_size"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := ListFlightsRequest{Page: defaultPage, PageSize: defaultPageSize}
			tc.mutate(&request)

			err := request.Validate()

			require.Equal(t, []string{tc.field}, invalidFields(t, err))
		})
	}
}
