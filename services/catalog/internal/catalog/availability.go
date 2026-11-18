package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

const eventAvailabilityChanged = "flight.availability_changed"

type availabilityChangedEvent struct {
	Data struct {
		FlightID            string `json:"flight_id"`
		AvailabilityVersion int64  `json:"availability_version"`
		AvailableSeats      int32  `json:"available_seats"`
	} `json:"data"`
}

type availabilityStore interface {
	ApplyAvailability(ctx context.Context, arg store.ApplyAvailabilityParams) (store.Flight, error)
}

var _ availabilityStore = (*store.Queries)(nil)

type EventHandler struct {
	db        availabilityStore
	reference *Reference
	cache     *RouteDayCache
}

func NewEventHandler(db availabilityStore, reference *Reference, cache *RouteDayCache) *EventHandler {
	return &EventHandler{db: db, reference: reference, cache: cache}
}

func (h *EventHandler) HandleAvailabilityChanged(ctx context.Context, d messaging.Delivery) error {
	var event availabilityChangedEvent
	if err := json.Unmarshal(d.Body, &event); err != nil {
		return fmt.Errorf("decode %s message %s: %w: %w", eventAvailabilityChanged, d.ID, messaging.ErrPermanent, err)
	}

	data := event.Data
	if !validUUID(data.FlightID) {
		return fmt.Errorf("%s message %s has flight_id %q: %w", eventAvailabilityChanged, d.ID, data.FlightID, messaging.ErrPermanent)
	}

	params := store.ApplyAvailabilityParams{
		ID:                  data.FlightID,
		AvailableSeats:      data.AvailableSeats,
		AvailabilityVersion: data.AvailabilityVersion,
	}
	flight, err := h.db.ApplyAvailability(ctx, params)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // an older or repeated report, or a flight the catalog does not know
	}
	if err != nil {
		return fmt.Errorf("apply availability of flight %s: %w", data.FlightID, err)
	}

	h.cache.forget(ctx, h.reference, flight)
	return nil
}
