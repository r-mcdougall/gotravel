package catalog

import (
	"fmt"
	"strings"
	"time"
)

type CreateFlightRequest struct {
	FlightNumber    string    `json:"flight_number"`
	AirlineCode     string    `json:"airline_code"`
	OriginCode      string    `json:"origin_code"`
	DestinationCode string    `json:"destination_code"`
	DepartureAt     time.Time `json:"departure_at"`
	ArrivalAt       time.Time `json:"arrival_at"`
	Price           int64     `json:"price"`
	Capacity        int32     `json:"capacity"`
}

func (r *CreateFlightRequest) Validate() error {
	var v ValidationError

	validNumber := flightNumberPattern.MatchString(r.FlightNumber)
	validAirline := airlineCodePattern.MatchString(r.AirlineCode)
	v.check(validNumber, "flight_number", flightNumberMessage)
	v.check(validAirline, "airline_code", airlineCodeMessage)
	v.check(!validNumber || !validAirline || strings.HasPrefix(r.FlightNumber, r.AirlineCode), "flight_number", "must start with the airline code")

	validOrigin := airportCodePattern.MatchString(r.OriginCode)
	validDestination := airportCodePattern.MatchString(r.DestinationCode)
	v.check(validOrigin, "origin_code", airportCodeMessage)
	v.check(validDestination, "destination_code", airportCodeMessage)
	v.check(!validOrigin || !validDestination || r.OriginCode != r.DestinationCode, "destination_code", "must differ from origin_code")

	v.check(!r.DepartureAt.IsZero(), "departure_at", "is required")
	v.check(!r.ArrivalAt.IsZero(), "arrival_at", "is required")
	v.check(r.DepartureAt.IsZero() || r.ArrivalAt.IsZero() || r.ArrivalAt.After(r.DepartureAt), "arrival_at", "must be after departure_at")

	v.check(r.Price >= 1, "price", "must be at least 1")
	v.check(r.Capacity >= 1, "capacity", "must be at least 1")

	return v.err()
}

type UpdateFlightRequest struct {
	DepartureAt *time.Time `json:"departure_at"`
	ArrivalAt   *time.Time `json:"arrival_at"`
	Price       *int64     `json:"price"`
	Capacity    *int32     `json:"capacity"`
}

func (r *UpdateFlightRequest) Validate() error {
	var v ValidationError

	v.check(r.DepartureAt != nil || r.ArrivalAt != nil || r.Price != nil || r.Capacity != nil, "body", "at least one field is required")

	v.check(r.DepartureAt == nil || r.ArrivalAt != nil, "arrival_at", "is required when departure_at is set")
	v.check(r.ArrivalAt == nil || r.DepartureAt != nil, "departure_at", "is required when arrival_at is set")
	v.check(r.DepartureAt == nil || r.ArrivalAt == nil || r.ArrivalAt.After(*r.DepartureAt), "arrival_at", "must be after departure_at")

	v.check(r.Price == nil || *r.Price >= 1, "price", "must be at least 1")
	v.check(r.Capacity == nil || *r.Capacity >= 1, "capacity", "must be at least 1")

	return v.err()
}

type ListFlightsRequest struct {
	Origin      string
	Destination string
	Date        *time.Time
	Page        int32
	PageSize    int32
}

func (r *ListFlightsRequest) Validate() error {
	var v ValidationError

	v.check(r.Origin == "" || airportCodePattern.MatchString(r.Origin), "origin", airportCodeMessage)
	v.check(r.Destination == "" || airportCodePattern.MatchString(r.Destination), "destination", airportCodeMessage)
	checkPaging(&v, r.Page, r.PageSize)

	return v.err()
}

// SearchFlightsRequest holds already parsed query parameters, with every
// default applied: Passengers is at least 1, Sort is set, and Page and
// PageSize are set. Date is the local departure date at the origin airport as
// midnight UTC, and zero means it was not sent. The departure bounds are
// offsets from local midnight at the origin airport.
type SearchFlightsRequest struct {
	Origin        string
	Destination   string
	Date          time.Time
	Passengers    int32
	Airlines      []string
	MinPrice      *int64
	MaxPrice      *int64
	DepartureFrom *time.Duration
	DepartureTo   *time.Duration
	Sort          string
	Page          int32
	PageSize      int32
}

func (r *SearchFlightsRequest) Validate() error {
	var v ValidationError

	v.check(airportCodePattern.MatchString(r.Origin), "origin", airportCodeMessage)
	v.check(airportCodePattern.MatchString(r.Destination), "destination", airportCodeMessage)
	sameAirport := r.Origin == r.Destination && airportCodePattern.MatchString(r.Origin)
	v.check(!sameAirport, "destination", "must differ from origin")
	v.check(!r.Date.IsZero(), "date", "is required")
	v.check(r.Passengers >= 1 && r.Passengers <= maxPassengers, "passengers", "must be between 1 and 9")

	for _, code := range r.Airlines {
		if !airlineCodePattern.MatchString(code) {
			v.check(false, "airlines", airlineCodeMessage)
			break
		}
	}

	v.check(r.MinPrice == nil || *r.MinPrice >= 0, "min_price", "must not be negative")
	v.check(r.MaxPrice == nil || *r.MaxPrice >= 0, "max_price", "must not be negative")
	v.check(r.MinPrice == nil || r.MaxPrice == nil || *r.MinPrice <= *r.MaxPrice, "max_price", "must not be lower than min_price")

	v.check(r.DepartureFrom == nil || validTimeOfDay(*r.DepartureFrom), "departure_from", timeOfDayMessage)
	v.check(r.DepartureTo == nil || validTimeOfDay(*r.DepartureTo), "departure_to", timeOfDayMessage)
	v.check(r.DepartureFrom == nil || r.DepartureTo == nil || *r.DepartureFrom <= *r.DepartureTo, "departure_to", "must not be before departure_from")

	v.check(validSort(r.Sort), "sort", "must be one of departure_asc, departure_desc, price_asc, price_desc")
	checkPaging(&v, r.Page, r.PageSize)

	return v.err()
}

type AirlineResponse struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type AirportResponse struct {
	Code string `json:"code"`
	Name string `json:"name"`
	City string `json:"city"`
}

type FlightResponse struct {
	ID              string          `json:"id"`
	FlightNumber    string          `json:"flight_number"`
	Airline         AirlineResponse `json:"airline"`
	Origin          AirportResponse `json:"origin"`
	Destination     AirportResponse `json:"destination"`
	DepartureAt     time.Time       `json:"departure_at"`
	ArrivalAt       time.Time       `json:"arrival_at"`
	DurationMinutes int             `json:"duration_minutes"`
	Price           int64           `json:"price"`
	AvailableSeats  int32           `json:"available_seats"`
}

type FlightListResponse struct {
	Data []FlightResponse `json:"data"`
	Meta PageMeta         `json:"meta"`
}

type AdminFlightResponse struct {
	ID              string          `json:"id"`
	FlightNumber    string          `json:"flight_number"`
	Airline         AirlineResponse `json:"airline"`
	Origin          AirportResponse `json:"origin"`
	Destination     AirportResponse `json:"destination"`
	DepartureAt     time.Time       `json:"departure_at"`
	ArrivalAt       time.Time       `json:"arrival_at"`
	DurationMinutes int             `json:"duration_minutes"`
	Price           int64           `json:"price"`
	Capacity        int32           `json:"capacity"`
	AvailableSeats  int32           `json:"available_seats"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type CreateHotelRequest struct {
	Name       string            `json:"name"`
	CityCode   string            `json:"city_code"`
	Address    string            `json:"address"`
	StarRating int32             `json:"star_rating"`
	RoomTypes  []RoomTypeRequest `json:"room_types"`
}

type RoomTypeRequest struct {
	Name          string `json:"name"`
	MaxGuests     int32  `json:"max_guests"`
	TotalRooms    int32  `json:"total_rooms"`
	PricePerNight int64  `json:"price_per_night"`
}

func (r *CreateHotelRequest) normalize() {
	r.Name = strings.TrimSpace(r.Name)
	r.Address = strings.TrimSpace(r.Address)
	for i := range r.RoomTypes {
		r.RoomTypes[i].Name = strings.TrimSpace(r.RoomTypes[i].Name)
	}
}

func (r *CreateHotelRequest) Validate() error {
	var v ValidationError

	checkText(&v, "name", r.Name, maxHotelNameLength)
	v.check(cityCodePattern.MatchString(r.CityCode), "city_code", cityCodeMessage)
	checkText(&v, "address", r.Address, maxAddressLength)
	v.check(r.StarRating >= 1 && r.StarRating <= maxStarRating, "star_rating", "must be between 1 and 5")
	v.check(len(r.RoomTypes) >= 1 && len(r.RoomTypes) <= maxRoomTypes, "room_types", "must hold between 1 and 10 room types")

	seen := make(map[string]bool, len(r.RoomTypes))
	for i, room := range r.RoomTypes {
		field := fmt.Sprintf("room_types[%d].", i)

		checkText(&v, field+"name", room.Name, maxRoomTypeNameLength)
		v.check(room.Name == "" || !seen[room.Name], field+"name", "must be unique within the hotel")
		seen[room.Name] = true

		v.check(room.MaxGuests >= 1 && room.MaxGuests <= maxGuests, field+"max_guests", "must be between 1 and 10")
		v.check(room.TotalRooms >= 1 && room.TotalRooms <= maxTotalRooms, field+"total_rooms", "must be between 1 and 1000")
		v.check(room.PricePerNight >= 1, field+"price_per_night", "must be at least 1")
	}

	return v.err()
}

// SearchHotelsRequest holds already parsed query parameters, with every
// default applied. CheckIn and CheckOut are local dates at the hotel as
// midnight UTC, and zero means they were not sent. Whether CheckIn is in the
// past is checked by the service, which knows the time.
type SearchHotelsRequest struct {
	City     string
	CheckIn  time.Time
	CheckOut time.Time
	Guests   int32
	MinPrice *int64
	MaxPrice *int64
	Sort     string
	Page     int32
	PageSize int32
}

func (r *SearchHotelsRequest) Validate() error {
	var v ValidationError

	v.check(cityCodePattern.MatchString(r.City), "city", cityCodeMessage)

	v.check(!r.CheckIn.IsZero(), "check_in", "is required")
	v.check(!r.CheckOut.IsZero(), "check_out", "is required")
	if !r.CheckIn.IsZero() && !r.CheckOut.IsZero() {
		v.check(r.CheckOut.After(r.CheckIn), "check_out", "must be after check_in")
		v.check(r.nights() <= maxNights, "check_out", "must be at most 14 nights after check_in")
	}

	v.check(r.Guests >= 1 && r.Guests <= maxGuests, "guests", "must be between 1 and 10")

	v.check(r.MinPrice == nil || *r.MinPrice >= 0, "min_price", "must not be negative")
	v.check(r.MaxPrice == nil || *r.MaxPrice >= 0, "max_price", "must not be negative")
	v.check(r.MinPrice == nil || r.MaxPrice == nil || *r.MinPrice <= *r.MaxPrice, "max_price", "must not be lower than min_price")

	v.check(validHotelSort(r.Sort), "sort", "must be one of price_asc, price_desc")
	checkPaging(&v, r.Page, r.PageSize)

	return v.err()
}

// nights is the length of the stay; both dates are midnight UTC.
func (r *SearchHotelsRequest) nights() int64 {
	return int64(r.CheckOut.Sub(r.CheckIn) / (24 * time.Hour))
}

type CityResponse struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

type AdminRoomTypeResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	MaxGuests     int32  `json:"max_guests"`
	TotalRooms    int32  `json:"total_rooms"`
	PricePerNight int64  `json:"price_per_night"`
}

type AdminHotelResponse struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	City       CityResponse            `json:"city"`
	Address    string                  `json:"address"`
	StarRating int32                   `json:"star_rating"`
	RoomTypes  []AdminRoomTypeResponse `json:"room_types"`
	CreatedAt  time.Time               `json:"created_at"`
}

type RoomTypeResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	MaxGuests     int32  `json:"max_guests"`
	PricePerNight int64  `json:"price_per_night"`
	TotalPrice    int64  `json:"total_price"`
}

type HotelResponse struct {
	ID                  string             `json:"id"`
	Name                string             `json:"name"`
	City                CityResponse       `json:"city"`
	Address             string             `json:"address"`
	StarRating          int32              `json:"star_rating"`
	LowestPricePerNight int64              `json:"lowest_price_per_night"`
	RoomTypes           []RoomTypeResponse `json:"room_types"`
}

type HotelListResponse struct {
	Data []HotelResponse `json:"data"`
	Meta PageMeta        `json:"meta"`
}

type PageMeta struct {
	Page     int32 `json:"page"`
	PageSize int32 `json:"page_size"`
	Total    int64 `json:"total"`
}

type AdminFlightListResponse struct {
	Data []AdminFlightResponse `json:"data"`
	Meta PageMeta              `json:"meta"`
}
