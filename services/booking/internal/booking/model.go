package booking

import "time"

const (
	maxPassengers   = 9
	maxFullNameLen  = 100
	defaultPage     = 1
	maxPage         = 10_000
	defaultPageSize = 20
	maxPageSize     = 100
)

type HoldRequest struct {
	FlightID   string             `json:"flight_id"`
	Passengers []PassengerRequest `json:"passengers"`
}

type PassengerRequest struct {
	FullName  string `json:"full_name"`
	BirthDate string `json:"birth_date"`
}

type ListRequest struct {
	Page     int32
	PageSize int32
}

type Response struct {
	ID             string              `json:"id"`
	Status         string              `json:"status"`
	Flight         FlightResponse      `json:"flight"`
	Passengers     []PassengerResponse `json:"passengers"`
	PassengerCount int32               `json:"passenger_count"`
	SeatPrice      int64               `json:"seat_price"`
	TotalPrice     int64               `json:"total_price"`
	ExpiresAt      time.Time           `json:"expires_at"`
	PaymentID      *string             `json:"payment_id"`
	CreatedAt      time.Time           `json:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at"`
}

type FlightResponse struct {
	ID              string    `json:"id"`
	FlightNumber    string    `json:"flight_number"`
	AirlineCode     string    `json:"airline_code"`
	OriginCode      string    `json:"origin_code"`
	DestinationCode string    `json:"destination_code"`
	DepartureAt     time.Time `json:"departure_at"`
	ArrivalAt       time.Time `json:"arrival_at"`
}

type PassengerResponse struct {
	FullName  string `json:"full_name"`
	BirthDate string `json:"birth_date"`
}

type ListResponse struct {
	Data []Response `json:"data"`
	Meta PageMeta   `json:"meta"`
}

type PageMeta struct {
	Page     int32 `json:"page"`
	PageSize int32 `json:"page_size"`
	Total    int64 `json:"total"`
}
