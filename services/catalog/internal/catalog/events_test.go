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
