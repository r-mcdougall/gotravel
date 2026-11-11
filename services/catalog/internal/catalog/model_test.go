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

func validSearchRequest() SearchFlightsRequest {
	return SearchFlightsRequest{
		Origin:      "CGK",
		Destination: "DPS",
		Date:        time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		Passengers:  1,
		Sort:        sortDepartureAsc,
		Page:        defaultPage,
		PageSize:    defaultPageSize,
	}
}

func TestSearchFlightsRequestValidate_acceptsAValidRequest(t *testing.T) {
	request := validSearchRequest()

	require.NoError(t, request.Validate())
}

func TestSearchFlightsRequestValidate_acceptsEveryFilterAndTheLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *SearchFlightsRequest)
	}{
		{"every filter", func(r *SearchFlightsRequest) {
			r.Passengers = 4
			r.Airlines = []string{"GA", "JT"}
			r.MinPrice = ptr(int64(500_000))
			r.MaxPrice = ptr(int64(2_000_000))
			r.DepartureFrom = ptr(6 * time.Hour)
			r.DepartureTo = ptr(18*time.Hour + 30*time.Minute)
			r.Sort = sortPriceDesc
			r.Page = 3
			r.PageSize = 50
		}},
		{"most passengers", func(r *SearchFlightsRequest) { r.Passengers = maxPassengers }},
		{"zero prices", func(r *SearchFlightsRequest) {
			r.MinPrice = ptr(int64(0))
			r.MaxPrice = ptr(int64(0))
		}},
		{"same minimum and maximum price", func(r *SearchFlightsRequest) {
			r.MinPrice = ptr(int64(900_000))
			r.MaxPrice = ptr(int64(900_000))
		}},
		{"whole day", func(r *SearchFlightsRequest) {
			r.DepartureFrom = ptr(time.Duration(0))
			r.DepartureTo = ptr(23*time.Hour + 59*time.Minute)
		}},
		{"same earliest and latest departure", func(r *SearchFlightsRequest) {
			r.DepartureFrom = ptr(9 * time.Hour)
			r.DepartureTo = ptr(9 * time.Hour)
		}},
		{"largest page and page size", func(r *SearchFlightsRequest) {
			r.Page = maxPage
			r.PageSize = maxPageSize
		}},
		{"sort by departure descending", func(r *SearchFlightsRequest) { r.Sort = sortDepartureDesc }},
		{"sort by price ascending", func(r *SearchFlightsRequest) { r.Sort = sortPriceAsc }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := validSearchRequest()
			tc.mutate(&request)

			require.NoError(t, request.Validate())
		})
	}
}

func TestSearchFlightsRequestValidate_rejectsASingleInvalidField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *SearchFlightsRequest)
		field  string
	}{
		{"missing origin", func(r *SearchFlightsRequest) { r.Origin = "" }, "origin"},
		{"lower case origin", func(r *SearchFlightsRequest) { r.Origin = "cgk" }, "origin"},
		{"missing destination", func(r *SearchFlightsRequest) { r.Destination = "" }, "destination"},
		{"destination with four letters", func(r *SearchFlightsRequest) { r.Destination = "DPSX" }, "destination"},
		{"destination equals origin", func(r *SearchFlightsRequest) { r.Destination = "CGK" }, "destination"},
		{"missing date", func(r *SearchFlightsRequest) { r.Date = time.Time{} }, "date"},
		{"no passengers", func(r *SearchFlightsRequest) { r.Passengers = 0 }, "passengers"},
		{"too many passengers", func(r *SearchFlightsRequest) { r.Passengers = maxPassengers + 1 }, "passengers"},
		{"lower case airline", func(r *SearchFlightsRequest) { r.Airlines = []string{"GA", "jt"} }, "airlines"},
		{"empty airline code", func(r *SearchFlightsRequest) { r.Airlines = []string{""} }, "airlines"},
		{"negative minimum price", func(r *SearchFlightsRequest) { r.MinPrice = ptr(int64(-1)) }, "min_price"},
		{"negative maximum price", func(r *SearchFlightsRequest) { r.MaxPrice = ptr(int64(-1)) }, "max_price"},
		{"minimum price above maximum", func(r *SearchFlightsRequest) {
			r.MinPrice = ptr(int64(900_001))
			r.MaxPrice = ptr(int64(900_000))
		}, "max_price"},
		{"earliest departure at 24:00", func(r *SearchFlightsRequest) { r.DepartureFrom = ptr(24 * time.Hour) }, "departure_from"},
		{"negative earliest departure", func(r *SearchFlightsRequest) { r.DepartureFrom = ptr(-time.Minute) }, "departure_from"},
		{"latest departure at 24:00", func(r *SearchFlightsRequest) { r.DepartureTo = ptr(24 * time.Hour) }, "departure_to"},
		{"latest departure before earliest", func(r *SearchFlightsRequest) {
			r.DepartureFrom = ptr(18 * time.Hour)
			r.DepartureTo = ptr(6 * time.Hour)
		}, "departure_to"},
		{"unknown sort", func(r *SearchFlightsRequest) { r.Sort = "cheapest" }, "sort"},
		{"empty sort", func(r *SearchFlightsRequest) { r.Sort = "" }, "sort"},
		{"page zero", func(r *SearchFlightsRequest) { r.Page = 0 }, "page"},
		{"page above the limit", func(r *SearchFlightsRequest) { r.Page = maxPage + 1 }, "page"},
		{"page size zero", func(r *SearchFlightsRequest) { r.PageSize = 0 }, "page_size"},
		{"page size above the limit", func(r *SearchFlightsRequest) { r.PageSize = maxPageSize + 1 }, "page_size"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := validSearchRequest()
			tc.mutate(&request)

			err := request.Validate()

			require.Equal(t, []string{tc.field}, invalidFields(t, err))
		})
	}
}

func TestSearchFlightsRequestValidate_reportsEveryInvalidFieldTogether(t *testing.T) {
	request := SearchFlightsRequest{
		Origin:        "cgk",
		Destination:   "dps",
		Passengers:    10,
		Airlines:      []string{"ga"},
		MinPrice:      ptr(int64(2)),
		MaxPrice:      ptr(int64(1)),
		DepartureFrom: ptr(24 * time.Hour),
		DepartureTo:   ptr(-time.Hour),
		Sort:          "cheapest",
	}

	err := request.Validate()

	require.Equal(t, []string{
		"origin", "destination", "date", "passengers", "airlines", "max_price",
		"departure_from", "departure_to", "departure_to", "sort", "page", "page_size",
	}, invalidFields(t, err))
}

func TestSearchFlightsRequestValidate_doesNotReportASameAirportWhenBothCodesAreEmpty(t *testing.T) {
	request := validSearchRequest()
	request.Origin = ""
	request.Destination = ""

	err := request.Validate()

	require.Equal(t, []string{"origin", "destination"}, invalidFields(t, err))
}
