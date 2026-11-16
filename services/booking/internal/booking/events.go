package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

const eventFlightChanged = "flight.change"

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

type flightChangedEvent struct {
	EventID    string            `json:"event_id"`
	Type       string            `json:"type"`
	OccurredAt time.Time         `json:"occurred_at"`
	Data       flightChangedData `json:"data"`
}

type inventoryStore interface {
	UpsertFlightInventory(ctx context.Context, arg store.UpsertFlightInventoryParams) error
}

type EventHandler struct {
	store inventoryStore
}

func NewEventHandler(store inventoryStore) *EventHandler {
	return &EventHandler{store: store}
}

func (h *EventHandler) HandleFlightChanged(ctx context.Context, d messaging.Delivery) error {
	var event flightChangedEvent
	if err := json.Unmarshal(d.Body, &event); err != nil {
		return fmt.Errorf("decode %s message %s: %w: %w", eventFlightChanged, d.ID, messaging.ErrPermanent, err)
	}

	data := event.Data
	if !validUUID(data.FlightID) {
		return fmt.Errorf("%s message %s has flight_id %q: %w", eventFlightChanged, d.ID, data.FlightID, messaging.ErrPermanent)
	}

	params := store.UpsertFlightInventoryParams{
		FlightID:        data.FlightID,
		Version:         data.Version,
		FlightNumber:    data.FlightNumber,
		AirlineCode:     data.AirlineCode,
		OriginCode:      data.OriginCode,
		DestinationCode: data.DestinationCode,
		DepartureAt:     data.DepartureAt.UTC(),
		ArrivalAt:       data.ArrivalAt.UTC(),
		Price:           data.Price,
		Capacity:        data.Capacity,
	}
	if err := h.store.UpsertFlightInventory(ctx, params); err != nil {
		return fmt.Errorf("upsert flight inventory %s: %w", data.FlightID, err)
	}

	return nil
}
