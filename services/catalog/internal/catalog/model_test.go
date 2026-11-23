package catalog

import (
	"fmt"
	"strings"
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

func validCreateHotelRequest() CreateHotelRequest {
	return CreateHotelRequest{
		Name:       "Seminyak Garden Hotel",
		CityCode:   "DPS",
		Address:    "Jl. Raya Seminyak 12, Badung",
		StarRating: 4,
		RoomTypes: []RoomTypeRequest{
			{Name: "Deluxe Double", MaxGuests: 2, TotalRooms: 20, PricePerNight: 850_000},
			{Name: "Family Suite", MaxGuests: 4, TotalRooms: 5, PricePerNight: 1_600_000},
		},
	}
}

func TestCreateHotelRequestNormalize_trimsEveryText(t *testing.T) {
	request := validCreateHotelRequest()
	request.Name = "  Seminyak Garden Hotel\t"
	request.Address = "\n Jl. Raya Seminyak 12, Badung "
	request.RoomTypes[1].Name = " Family Suite "

	request.normalize()

	require.Equal(t, validCreateHotelRequest(), request)
}

func TestCreateHotelRequestValidate_acceptsAValidRequestAndTheLimits(t *testing.T) {
	tenRoomTypes := make([]RoomTypeRequest, maxRoomTypes)
	for i := range tenRoomTypes {
		tenRoomTypes[i] = RoomTypeRequest{Name: fmt.Sprintf("Room %d", i+1), MaxGuests: 2, TotalRooms: 1, PricePerNight: 1}
	}

	tests := []struct {
		name   string
		mutate func(r *CreateHotelRequest)
	}{
		{"valid request", func(*CreateHotelRequest) {}},
		{"longest name", func(r *CreateHotelRequest) { r.Name = strings.Repeat("a", maxHotelNameLength) }},
		{"longest name counted in characters, not bytes", func(r *CreateHotelRequest) { r.Name = strings.Repeat("é", maxHotelNameLength) }},
		{"longest address", func(r *CreateHotelRequest) { r.Address = strings.Repeat("a", maxAddressLength) }},
		{"one star", func(r *CreateHotelRequest) { r.StarRating = 1 }},
		{"five stars", func(r *CreateHotelRequest) { r.StarRating = 5 }},
		{"one room type", func(r *CreateHotelRequest) { r.RoomTypes = r.RoomTypes[:1] }},
		{"most room types", func(r *CreateHotelRequest) { r.RoomTypes = tenRoomTypes }},
		{"largest room type", func(r *CreateHotelRequest) {
			r.RoomTypes[0] = RoomTypeRequest{
				Name:          strings.Repeat("a", maxRoomTypeNameLength),
				MaxGuests:     maxGuests,
				TotalRooms:    maxTotalRooms,
				PricePerNight: 1,
			}
		}},
		{"room type names that differ only in case", func(r *CreateHotelRequest) { r.RoomTypes[1].Name = "deluxe double" }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := validCreateHotelRequest()
			tc.mutate(&request)

			require.NoError(t, request.Validate())
		})
	}
}

func TestCreateHotelRequestValidate_rejectsASingleInvalidField(t *testing.T) {
	elevenRoomTypes := make([]RoomTypeRequest, maxRoomTypes+1)
	for i := range elevenRoomTypes {
		elevenRoomTypes[i] = RoomTypeRequest{Name: fmt.Sprintf("Room %d", i+1), MaxGuests: 2, TotalRooms: 1, PricePerNight: 1}
	}

	tests := []struct {
		name   string
		mutate func(r *CreateHotelRequest)
		field  string
	}{
		{"missing name", func(r *CreateHotelRequest) { r.Name = "" }, "name"},
		{"name too long", func(r *CreateHotelRequest) { r.Name = strings.Repeat("é", maxHotelNameLength+1) }, "name"},
		{"missing city", func(r *CreateHotelRequest) { r.CityCode = "" }, "city_code"},
		{"lower case city", func(r *CreateHotelRequest) { r.CityCode = "dps" }, "city_code"},
		{"city with four letters", func(r *CreateHotelRequest) { r.CityCode = "DPSX" }, "city_code"},
		{"missing address", func(r *CreateHotelRequest) { r.Address = "" }, "address"},
		{"address too long", func(r *CreateHotelRequest) { r.Address = strings.Repeat("a", maxAddressLength+1) }, "address"},
		{"no stars", func(r *CreateHotelRequest) { r.StarRating = 0 }, "star_rating"},
		{"six stars", func(r *CreateHotelRequest) { r.StarRating = 6 }, "star_rating"},
		{"no room types", func(r *CreateHotelRequest) { r.RoomTypes = nil }, "room_types"},
		{"too many room types", func(r *CreateHotelRequest) { r.RoomTypes = elevenRoomTypes }, "room_types"},
		{"room type without a name", func(r *CreateHotelRequest) { r.RoomTypes[1].Name = "" }, "room_types[1].name"},
		{"room type name too long", func(r *CreateHotelRequest) { r.RoomTypes[0].Name = strings.Repeat("a", maxRoomTypeNameLength+1) }, "room_types[0].name"},
		{"duplicate room type name", func(r *CreateHotelRequest) { r.RoomTypes[1].Name = "Deluxe Double" }, "room_types[1].name"},
		{"room for no guests", func(r *CreateHotelRequest) { r.RoomTypes[0].MaxGuests = 0 }, "room_types[0].max_guests"},
		{"room for too many guests", func(r *CreateHotelRequest) { r.RoomTypes[0].MaxGuests = maxGuests + 1 }, "room_types[0].max_guests"},
		{"no rooms", func(r *CreateHotelRequest) { r.RoomTypes[1].TotalRooms = 0 }, "room_types[1].total_rooms"},
		{"too many rooms", func(r *CreateHotelRequest) { r.RoomTypes[1].TotalRooms = maxTotalRooms + 1 }, "room_types[1].total_rooms"},
		{"free room", func(r *CreateHotelRequest) { r.RoomTypes[0].PricePerNight = 0 }, "room_types[0].price_per_night"},
		{"negative price", func(r *CreateHotelRequest) { r.RoomTypes[0].PricePerNight = -1 }, "room_types[0].price_per_night"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := validCreateHotelRequest()
			tc.mutate(&request)

			err := request.Validate()

			require.Equal(t, []string{tc.field}, invalidFields(t, err))
		})
	}
}

func TestCreateHotelRequestValidate_reportsEveryInvalidFieldOfAnEmptyRequest(t *testing.T) {
	request := CreateHotelRequest{}

	err := request.Validate()

	require.Equal(t, []string{"name", "city_code", "address", "star_rating", "room_types"}, invalidFields(t, err))
}

func TestCreateHotelRequestValidate_reportsEveryInvalidRoomTypeField(t *testing.T) {
	request := validCreateHotelRequest()
	request.RoomTypes = []RoomTypeRequest{{}, {Name: "Twin", MaxGuests: 2, TotalRooms: 1, PricePerNight: 1}, {Name: "Twin"}}

	err := request.Validate()

	require.Equal(t, []string{
		"room_types[0].name", "room_types[0].max_guests", "room_types[0].total_rooms", "room_types[0].price_per_night",
		"room_types[2].name", "room_types[2].max_guests", "room_types[2].total_rooms", "room_types[2].price_per_night",
	}, invalidFields(t, err))
}

func validSearchHotelsRequest() SearchHotelsRequest {
	return SearchHotelsRequest{
		City:     "DPS",
		CheckIn:  time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC),
		CheckOut: time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC),
		Guests:   defaultGuests,
		Sort:     sortPriceAsc,
		Page:     defaultPage,
		PageSize: defaultPageSize,
	}
}

func TestSearchHotelsRequestValidate_acceptsEveryFilterAndTheLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *SearchHotelsRequest)
	}{
		{"valid request", func(*SearchHotelsRequest) {}},
		{"every filter", func(r *SearchHotelsRequest) {
			r.Guests = 3
			r.MinPrice = ptr(int64(500_000))
			r.MaxPrice = ptr(int64(2_000_000))
			r.Sort = sortPriceDesc
			r.Page = 3
			r.PageSize = 50
		}},
		{"one night", func(r *SearchHotelsRequest) { r.CheckOut = r.CheckIn.AddDate(0, 0, 1) }},
		{"longest stay", func(r *SearchHotelsRequest) { r.CheckOut = r.CheckIn.AddDate(0, 0, maxNights) }},
		{"most guests", func(r *SearchHotelsRequest) { r.Guests = maxGuests }},
		{"same minimum and maximum price", func(r *SearchHotelsRequest) {
			r.MinPrice = ptr(int64(900_000))
			r.MaxPrice = ptr(int64(900_000))
		}},
		{"largest page and page size", func(r *SearchHotelsRequest) {
			r.Page = maxPage
			r.PageSize = maxPageSize
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := validSearchHotelsRequest()
			tc.mutate(&request)

			require.NoError(t, request.Validate())
		})
	}
}

func TestSearchHotelsRequestValidate_rejectsASingleInvalidField(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *SearchHotelsRequest)
		field  string
	}{
		{"missing city", func(r *SearchHotelsRequest) { r.City = "" }, "city"},
		{"lower case city", func(r *SearchHotelsRequest) { r.City = "dps" }, "city"},
		{"missing check-in", func(r *SearchHotelsRequest) { r.CheckIn = time.Time{} }, "check_in"},
		{"missing check-out", func(r *SearchHotelsRequest) { r.CheckOut = time.Time{} }, "check_out"},
		{"check-out on the check-in day", func(r *SearchHotelsRequest) { r.CheckOut = r.CheckIn }, "check_out"},
		{"check-out before check-in", func(r *SearchHotelsRequest) { r.CheckOut = r.CheckIn.AddDate(0, 0, -1) }, "check_out"},
		{"stay one night too long", func(r *SearchHotelsRequest) { r.CheckOut = r.CheckIn.AddDate(0, 0, maxNights+1) }, "check_out"},
		{"no guests", func(r *SearchHotelsRequest) { r.Guests = 0 }, "guests"},
		{"too many guests", func(r *SearchHotelsRequest) { r.Guests = maxGuests + 1 }, "guests"},
		{"negative minimum price", func(r *SearchHotelsRequest) { r.MinPrice = ptr(int64(-1)) }, "min_price"},
		{"negative maximum price", func(r *SearchHotelsRequest) { r.MaxPrice = ptr(int64(-1)) }, "max_price"},
		{"minimum price above maximum", func(r *SearchHotelsRequest) {
			r.MinPrice = ptr(int64(900_001))
			r.MaxPrice = ptr(int64(900_000))
		}, "max_price"},
		{"flight sort", func(r *SearchHotelsRequest) { r.Sort = sortDepartureAsc }, "sort"},
		{"empty sort", func(r *SearchHotelsRequest) { r.Sort = "" }, "sort"},
		{"page zero", func(r *SearchHotelsRequest) { r.Page = 0 }, "page"},
		{"page size above the limit", func(r *SearchHotelsRequest) { r.PageSize = maxPageSize + 1 }, "page_size"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := validSearchHotelsRequest()
			tc.mutate(&request)

			err := request.Validate()

			require.Equal(t, []string{tc.field}, invalidFields(t, err))
		})
	}
}

func TestSearchHotelsRequestValidate_reportsEveryInvalidFieldTogether(t *testing.T) {
	request := SearchHotelsRequest{
		City:     "dps",
		Guests:   11,
		MinPrice: ptr(int64(2)),
		MaxPrice: ptr(int64(1)),
		Sort:     "cheapest",
	}

	err := request.Validate()

	require.Equal(t, []string{
		"city", "check_in", "check_out", "guests", "max_price", "sort", "page", "page_size",
	}, invalidFields(t, err))
}

func TestSearchHotelsRequestNights_countsTheNightsOfTheStay(t *testing.T) {
	request := validSearchHotelsRequest()

	require.Equal(t, int64(2), request.nights())
}
