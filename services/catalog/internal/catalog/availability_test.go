package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

const availabilityFlightID = "0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"

type fakeAvailabilityStore struct {
	t     *testing.T
	apply func(arg store.ApplyAvailabilityParams) (store.Flight, error)
}

func (f *fakeAvailabilityStore) ApplyAvailability(_ context.Context, arg store.ApplyAvailabilityParams) (store.Flight, error) {
	if f.apply == nil {
		f.t.Fatal("unexpected call to ApplyAvailability")
	}
	return f.apply(arg)
}

func availabilityDelivery(body string) messaging.Delivery {
	return messaging.Delivery{ID: "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e", Type: eventAvailabilityChanged, Body: []byte(body)}
}

const availabilityBody = `{
	"event_id": "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e",
	"type": "flight.availability_changed",
	"occurred_at": "2026-11-18T10:00:00Z",
	"data": {"flight_id": "0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11", "availability_version": 7, "available_seats": 42}
}`

func TestEventHandlerHandleAvailabilityChanged_recordsTheReportedSeats(t *testing.T) {
	var got store.ApplyAvailabilityParams
	handler := NewEventHandler(&fakeAvailabilityStore{t: t, apply: func(arg store.ApplyAvailabilityParams) (store.Flight, error) {
		got = arg
		return store.Flight{ID: arg.ID, OriginCode: "CGK", DestinationCode: "DPS"}, nil
	}}, nil, nil)

	require.NoError(t, handler.HandleAvailabilityChanged(t.Context(), availabilityDelivery(availabilityBody)))

	require.Equal(t, store.ApplyAvailabilityParams{
		ID:                  availabilityFlightID,
		AvailableSeats:      42,
		AvailabilityVersion: 7,
	}, got)
}

func TestEventHandlerHandleAvailabilityChanged_ignoresAnOlderReport(t *testing.T) {
	handler := NewEventHandler(&fakeAvailabilityStore{t: t, apply: func(store.ApplyAvailabilityParams) (store.Flight, error) {
		return store.Flight{}, pgx.ErrNoRows
	}}, nil, nil)

	require.NoError(t, handler.HandleAvailabilityChanged(t.Context(), availabilityDelivery(availabilityBody)))
}

func TestEventHandlerHandleAvailabilityChanged_rejectsMessagesThatCanNeverApply(t *testing.T) {
	for name, body := range map[string]string{
		"not json":          "not json",
		"missing flight_id": `{"data": {"availability_version": 1, "available_seats": 3}}`,
		"malformed uuid":    `{"data": {"flight_id": "GA410", "availability_version": 1, "available_seats": 3}}`,
	} {
		t.Run(name, func(t *testing.T) {
			handler := NewEventHandler(&fakeAvailabilityStore{t: t}, nil, nil)

			err := handler.HandleAvailabilityChanged(t.Context(), availabilityDelivery(body))

			require.ErrorIs(t, err, messaging.ErrPermanent)
		})
	}
}

func TestEventHandlerHandleAvailabilityChanged_treatsStoreFailuresAsTemporary(t *testing.T) {
	handler := NewEventHandler(&fakeAvailabilityStore{t: t, apply: func(store.ApplyAvailabilityParams) (store.Flight, error) {
		return store.Flight{}, errors.New("database unavailable")
	}}, nil, nil)

	err := handler.HandleAvailabilityChanged(t.Context(), availabilityDelivery(availabilityBody))

	require.ErrorContains(t, err, "database unavailable")
	require.NotErrorIs(t, err, messaging.ErrPermanent)
}
