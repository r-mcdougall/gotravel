package catalog

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

const eventFlightChanged = "flight.changed"

type flightChangedData struct {
	FlightID        string    `json:"flight_id"`
	Version         int32     `json:"version"`
	FlightNumber    string    `json:"flight_number"`
	AirlineCode     string    `json:"airline_code"`
	OriginCode      string    `json:"origin_code"`
	DestinationCode string    `json:"destination_code"`
	DepartureAt     time.Time `json:"departure_at"`
	ArrivalAt       time.Time `json:"arrival_at"`
	Price           int64     `json:"price"`
	Capacity        int32     `json:"capacity"`
}

func newFlightChangedEvent(flight store.Flight) (store.InsertOutboxEventParams, error) {
	data := flightChangedData{
		FlightID:        flight.ID,
		Version:         flight.Version,
		FlightNumber:    flight.FlightNumber,
		AirlineCode:     flight.AirlineCode,
		OriginCode:      flight.OriginCode,
		DestinationCode: flight.DestinationCode,
		DepartureAt:     flight.DepartureAt.UTC(),
		ArrivalAt:       flight.ArrivalAt.UTC(),
		Price:           flight.Price,
		Capacity:        flight.Capacity,
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return store.InsertOutboxEventParams{}, fmt.Errorf("marshal %s event: %w", eventFlightChanged, err)
	}

	return store.InsertOutboxEventParams{EventType: eventFlightChanged, Payload: payload}, nil
}

const eventRoomTypeCreated = "room_type.created"

// roomTypeCreatedData is what booking needs to hold rooms without asking the catalog.
type roomTypeCreatedData struct {
	RoomTypeID    string `json:"room_type_id"`
	HotelID       string `json:"hotel_id"`
	HotelName     string `json:"hotel_name"`
	CityCode      string `json:"city_code"`
	Name          string `json:"name"`
	MaxGuests     int32  `json:"max_guests"`
	TotalRooms    int32  `json:"total_rooms"`
	PricePerNight int64  `json:"price_per_night"`
}

func newRoomTypeCreatedEvent(hotel store.Hotel, roomType store.RoomType) (store.InsertOutboxEventParams, error) {
	data := roomTypeCreatedData{
		RoomTypeID:    roomType.ID,
		HotelID:       hotel.ID,
		HotelName:     hotel.Name,
		CityCode:      hotel.CityCode,
		Name:          roomType.Name,
		MaxGuests:     roomType.MaxGuests,
		TotalRooms:    roomType.TotalRooms,
		PricePerNight: roomType.PricePerNight,
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return store.InsertOutboxEventParams{}, fmt.Errorf("marshal %s event: %w", eventRoomTypeCreated, err)
	}

	return store.InsertOutboxEventParams{EventType: eventRoomTypeCreated, Payload: payload}, nil
}
