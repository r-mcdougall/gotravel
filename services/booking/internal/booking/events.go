package booking

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

const (
	eventFlightChanged       = "flight.changed"
	eventAvailabilityChanged = "flight.availability_changed"
)

type flightChangedEvent struct {
	EventID    string            `json:"event_id"`
	Type       string            `json:"type"`
	OccurredAt time.Time         `json:"occurred_at"`
	Data       flightChangedData `json:"data"`
}

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

type inventoryStore interface {
	UpsertFlightInventory(ctx context.Context, arg store.UpsertFlightInventoryParams) error
}

type EventHandler struct {
	store inventoryStore
}

func NewEventHandler(store inventoryStore) *EventHandler {
	return &EventHandler{store: store}
}

// HandleFlightChanged copies the catalog's view of a flight into the inventory.
func (h *EventHandler) HandleFlightChanged(ctx context.Context, d messaging.Delivery) error {
	var event flightChangedEvent
	if err := json.Unmarshal(d.Body, &event); err != nil {
		return fmt.Errorf("decode %s message %s: %w: %w", eventFlightChanged, d.ID, messaging.ErrPermanent, err)
	}

	data := event.Data
	if !validUUID(data.FlightID) {
		return fmt.Errorf("%s message %s has flight_id %q: %w", eventFlightChanged, d.ID, data.FlightID, messaging.ErrPermanent)
	}

	err := h.store.UpsertFlightInventory(ctx, store.UpsertFlightInventoryParams{
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
	})
	if err != nil {
		return fmt.Errorf("upsert flight inventory %s: %w", data.FlightID, err)
	}

	return nil
}

type availabilityChangedData struct {
	FlightID            string `json:"flight_id"`
	AvailabilityVersion int64  `json:"availability_version"`
	AvailableSeats      int32  `json:"available_seats"`
}

type outboxWriter interface {
	InsertOutboxEvent(ctx context.Context, arg store.InsertOutboxEventParams) error
}

// recordAvailabilityChanged tells the catalog how many seats a flight has left.
func recordAvailabilityChanged(ctx context.Context, q outboxWriter, flight store.FlightInventory) error {
	payload, err := json.Marshal(availabilityChangedData{
		FlightID:            flight.FlightID,
		AvailabilityVersion: flight.AvailabilityVersion,
		AvailableSeats:      availableSeats(flight),
	})
	if err != nil {
		return fmt.Errorf("marshal %s event: %w", eventAvailabilityChanged, err)
	}

	if err := q.InsertOutboxEvent(ctx, store.InsertOutboxEventParams{EventType: eventAvailabilityChanged, Payload: payload}); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

// availableSeats can see a negative balance after the catalog lowers capacity; it reports zero then.
func availableSeats(flight store.FlightInventory) int32 {
	return max(flight.Capacity-flight.HeldSeats-flight.SoldSeats, 0)
}
