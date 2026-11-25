package booking

import "time"

const (
	maxPassengers   = 9
	maxFullNameLen  = 100
	maxGuests       = 10
	maxNights       = 14
	defaultPage     = 1
	maxPage         = 10_000
	defaultPageSize = 20
	maxPageSize     = 100

	defaultAnalyticsDays = 30
	maxAnalyticsDays     = 366
	maxTopRoutes         = 10
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

type HotelHoldRequest struct {
	RoomTypeID string `json:"room_type_id"`
	CheckIn    string `json:"check_in"`
	CheckOut   string `json:"check_out"`
	Guests     int32  `json:"guests"`
	GuestName  string `json:"guest_name"`
}

type HotelBookingResponse struct {
	ID            string          `json:"id"`
	Status        string          `json:"status"`
	Hotel         HotelSummary    `json:"hotel"`
	RoomType      RoomTypeSummary `json:"room_type"`
	CheckIn       string          `json:"check_in"`
	CheckOut      string          `json:"check_out"`
	Nights        int64           `json:"nights"`
	Guests        int32           `json:"guests"`
	GuestName     string          `json:"guest_name"`
	PricePerNight int64           `json:"price_per_night"`
	TotalPrice    int64           `json:"total_price"`
	ExpiresAt     time.Time       `json:"expires_at"`
	PaymentID     *string         `json:"payment_id"`
	CreatedAt     time.Time       `json:"created_at"`
	UpdatedAt     time.Time       `json:"updated_at"`
}

type HotelSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	CityCode string `json:"city_code"`
}

type RoomTypeSummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Bill is what payment needs to charge a flight or hotel booking.
type Bill struct {
	ID         string    `json:"id"`
	Status     string    `json:"status"`
	TotalPrice int64     `json:"total_price"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type HotelBookingListResponse struct {
	Data []HotelBookingResponse `json:"data"`
	Meta PageMeta               `json:"meta"`
}

type AnalyticsRequest struct {
	From string
	To   string
}

type Analytics struct {
	From             string         `json:"from"`
	To               string         `json:"to"`
	BookingsByStatus StatusCounts   `json:"bookings_by_status"`
	Revenue          int64          `json:"revenue"`
	TopRoutes        []RouteSummary `json:"top_routes"`
}

type StatusCounts struct {
	Held      int64 `json:"HELD"`
	Confirmed int64 `json:"CONFIRMED"`
	Expired   int64 `json:"EXPIRED"`
	Cancelled int64 `json:"CANCELLED"`
}

type RouteSummary struct {
	OriginCode      string `json:"origin_code"`
	DestinationCode string `json:"destination_code"`
	Bookings        int64  `json:"bookings"`
	Seats           int64  `json:"seats"`
	Revenue         int64  `json:"revenue"`
}

// FlightSeats is booking's seat count of a flight, read by catalog before it lowers the capacity.
type FlightSeats struct {
	FlightID  string `json:"flight_id"`
	Capacity  int32  `json:"capacity"`
	HeldSeats int32  `json:"held_seats"`
	SoldSeats int32  `json:"sold_seats"`
}
