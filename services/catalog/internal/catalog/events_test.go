package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewFlightChangedEvent_carriesTheFullFlightStateInUTC(t *testing.T) {
	event, err := newFlightChangedEvent(sampleFlight())
	require.NoError(t, err)

	require.Equal(t, "flight.changed", event.EventType)
	require.JSONEq(t, `{
		"flight_id": "0b5c2a2e-7b53-4a3b-9d0e-2f6d4c1c9a11",
		"version": 3,
		"flight_number": "GA410",
		"airline_code": "GA",
		"origin_code": "CGK",
		"destination_code": "DPS",
		"departure_at": "2026-11-19T23:00:00Z",
		"arrival_at": "2026-11-20T00:55:00Z",
		"price": 1250000,
		"capacity": 180
	}`, string(event.Payload))
}

func TestNewFlightChangedEvent_leavesOutSeatAvailability(t *testing.T) {
	event, err := newFlightChangedEvent(sampleFlight())
	require.NoError(t, err)

	require.NotContains(t, string(event.Payload), "available_seats")
}

func TestNewRoomTypeCreatedEvent_carriesWhatBookingNeedsToHoldRooms(t *testing.T) {
	event, err := newRoomTypeCreatedEvent(sampleHotel(), sampleRoomType())
	require.NoError(t, err)

	require.Equal(t, "room_type.created", event.EventType)
	require.JSONEq(t, `{
		"room_type_id": "0b8e5d21-7c4a-4f19-8e36-5a2d9c1f7b40",
		"hotel_id": "6f1c2a9e-3b7d-4c51-9a0e-2d8f4b6c1e73",
		"hotel_name": "Seminyak Garden Hotel",
		"city_code": "DPS",
		"name": "Deluxe Double",
		"max_guests": 2,
		"total_rooms": 20,
		"price_per_night": 850000
	}`, string(event.Payload))
}
