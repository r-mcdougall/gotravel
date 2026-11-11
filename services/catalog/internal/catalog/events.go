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
