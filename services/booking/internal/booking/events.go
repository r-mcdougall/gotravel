package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

const (
	eventFlightChanged       = "flight.changed"
	eventAvailabilityChanged = "flight.availability_changed"
	eventRoomTypeCreated     = "room_type.created"
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

type EventHandler struct {
	db bookingStore
}

func NewEventHandler(db bookingStore) *EventHandler {
	return &EventHandler{db: db}
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

	return h.db.InTx(ctx, func(q queries) error {
		flight, err := q.UpsertFlightInventory(ctx, params)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // an older or repeated version changes nothing
		}
		if err != nil {
			return fmt.Errorf("upsert flight inventory %s: %w", data.FlightID, err)
		}

		return recordAvailabilityChanged(ctx, q, flight)
	})
}

type roomTypeCreatedEvent struct {
	Data roomTypeCreatedData `json:"data"`
}

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

// HandleRoomTypeCreated keeps the catalog's room type so holds never have to ask the catalog.
// Room types never change, so a repeated message keeps the row already stored.
func (h *EventHandler) HandleRoomTypeCreated(ctx context.Context, d messaging.Delivery) error {
	var event roomTypeCreatedEvent
	if err := json.Unmarshal(d.Body, &event); err != nil {
		return fmt.Errorf("decode %s message %s: %w: %w", eventRoomTypeCreated, d.ID, messaging.ErrPermanent, err)
	}

	data := event.Data
	if field := data.invalidField(); field != "" {
		return fmt.Errorf("%s message %s has an invalid %s: %w", eventRoomTypeCreated, d.ID, field, messaging.ErrPermanent)
	}

	err := h.db.InsertRoomType(ctx, store.InsertRoomTypeParams{
		RoomTypeID:    data.RoomTypeID,
		HotelID:       data.HotelID,
		HotelName:     data.HotelName,
		CityCode:      data.CityCode,
		Name:          data.Name,
		MaxGuests:     data.MaxGuests,
		TotalRooms:    data.TotalRooms,
		PricePerNight: data.PricePerNight,
	})
	if err != nil {
		return fmt.Errorf("insert room type %s: %w", data.RoomTypeID, err)
	}
	return nil
}

// invalidField names the first field booking cannot hold rooms with, or returns "".
func (d roomTypeCreatedData) invalidField() string {
	switch {
	case !validUUID(d.RoomTypeID):
		return "room_type_id"
	case !validUUID(d.HotelID):
		return "hotel_id"
	case d.HotelName == "":
		return "hotel_name"
	case d.CityCode == "":
		return "city_code"
	case d.Name == "":
		return "name"
	case d.MaxGuests < 1 || d.MaxGuests > maxGuests:
		return "max_guests"
	case d.TotalRooms < 1:
		return "total_rooms"
	case d.PricePerNight < 1:
		return "price_per_night"
	default:
		return ""
	}
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
