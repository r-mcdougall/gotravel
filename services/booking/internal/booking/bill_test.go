package booking

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServiceBill_treatsAMalformedIDAsNotFound(t *testing.T) {
	svc := newService(unusedStore{t}, 15*time.Minute, time.Now)

	_, err := svc.Bill(t.Context(), johnID, "not-a-uuid")

	require.ErrorIs(t, err, ErrNotFound)
}

func TestServiceBill_chargesAFlightBookingForEverySeat(t *testing.T) {
	svc, pool, now := newTestService(t)
	addFlight(t, pool, testFlightID, now.Add(48*time.Hour), 10)
	held, err := svc.Hold(t.Context(), johnID, HoldRequest{FlightID: testFlightID, Passengers: twoPassengers()})
	require.NoError(t, err)

	bill, err := svc.Bill(t.Context(), johnID, held.ID)

	require.NoError(t, err)
	require.Equal(t, Bill{ID: held.ID, Status: "HELD", TotalPrice: 2_500_000, ExpiresAt: held.ExpiresAt}, bill)
}

func TestServiceBill_chargesAHotelBookingForEveryNight(t *testing.T) {
	svc, pool, now := newTestService(t)
	addRoomType(t, pool, 5, 2)
	held, err := svc.HoldRoom(t.Context(), johnID, hotelHold(now, 7, 10))
	require.NoError(t, err)
	_, err = svc.CancelHotelBooking(t.Context(), johnID, held.ID)
	require.NoError(t, err)

	bill, err := svc.Bill(t.Context(), johnID, held.ID)

	require.NoError(t, err)
	require.Equal(t, Bill{ID: held.ID, Status: "CANCELLED", TotalPrice: 4_500_000, ExpiresAt: held.ExpiresAt}, bill,
		"a bill reports any status; payment decides what it can charge")
}

func TestServiceBill_hidesBookingsOfOtherUsers(t *testing.T) {
	svc, pool, now := newTestService(t)
	addFlight(t, pool, testFlightID, now.Add(48*time.Hour), 10)
	addRoomType(t, pool, 5, 2)
	flight, err := svc.Hold(t.Context(), johnID, HoldRequest{FlightID: testFlightID, Passengers: twoPassengers()})
	require.NoError(t, err)
	hotel, err := svc.HoldRoom(t.Context(), johnID, hotelHold(now, 7, 8))
	require.NoError(t, err)

	for _, id := range []string{flight.ID, hotel.ID, "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e"} {
		_, err = svc.Bill(t.Context(), janeID, id)
		require.ErrorIs(t, err, ErrNotFound, id)
	}
}
