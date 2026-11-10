package catalog

import (
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
	v.check(r.Page >= 1 && r.Page <= maxPage, "page", "must be between 1 and 1000000")
	v.check(r.PageSize >= 1 && r.PageSize <= maxPageSize, "page_size", "must be between 1 and 100")

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

type PageMeta struct {
	Page     int32 `json:"page"`
	PageSize int32 `json:"page_size"`
	Total    int64 `json:"total"`
}

type AdminFlightListResponse struct {
	Data []AdminFlightResponse `json:"data"`
	Meta PageMeta              `json:"meta"`
}
