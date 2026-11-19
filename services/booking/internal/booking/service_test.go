package booking

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

const (
	johnID = "11111111-1111-4111-8111-111111111111"
	janeID = "22222222-2222-4222-8222-222222222222"
)

func twoPassengers() []PassengerRequest {
	return []PassengerRequest{
		{FullName: "John Doe", BirthDate: "1990-05-17"},
		{FullName: "Jane Smith", BirthDate: "1992-08-03"},
	}
}

// unusedStore fails the test on any call, for requests that must be refused before reaching the database.
type unusedStore struct{ t *testing.T }

func (s unusedStore) fail(name string) { s.t.Fatalf("unexpected call to %s", name) }

func (s unusedStore) HoldSeats(context.Context, store.HoldSeatsParams) (store.FlightInventory, error) {
	s.fail("HoldSeats")
	return store.FlightInventory{}, nil
}

func (s unusedStore) ReleaseHeldSeats(context.Context, store.ReleaseHeldSeatsParams) (store.FlightInventory, error) {
	s.fail("ReleaseHeldSeats")
	return store.FlightInventory{}, nil
}

func (s unusedStore) GetFlightInventory(context.Context, string) (store.FlightInventory, error) {
	s.fail("GetFlightInventory")
	return store.FlightInventory{}, nil
}

func (s unusedStore) CreateBooking(context.Context, store.CreateBookingParams) (store.Booking, error) {
	s.fail("CreateBooking")
	return store.Booking{}, nil
}

func (s unusedStore) CreatePassengers(context.Context, []store.CreatePassengersParams) (int64, error) {
	s.fail("CreatePassengers")
	return 0, nil
}

func (s unusedStore) GetBooking(context.Context, store.GetBookingParams) (store.GetBookingRow, error) {
	s.fail("GetBooking")
	return store.GetBookingRow{}, nil
}

func (s unusedStore) ListBookings(context.Context, store.ListBookingsParams) ([]store.ListBookingsRow, error) {
	s.fail("ListBookings")
	return nil, nil
}

func (s unusedStore) CountBookings(context.Context, string) (int64, error) {
	s.fail("CountBookings")
	return 0, nil
}

func (s unusedStore) ListPassengers(context.Context, []string) ([]store.Passenger, error) {
	s.fail("ListPassengers")
	return nil, nil
}

func (s unusedStore) CancelBooking(context.Context, store.CancelBookingParams) (store.Booking, error) {
	s.fail("CancelBooking")
	return store.Booking{}, nil
}

func (s unusedStore) InsertOutboxEvent(context.Context, store.InsertOutboxEventParams) error {
	s.fail("InsertOutboxEvent")
	return nil
}

func (s unusedStore) ExpireHolds(context.Context, int32) ([]store.ExpireHoldsRow, error) {
	s.fail("ExpireHolds")
	return nil, nil
}

func (s unusedStore) UpsertFlightInventory(context.Context, store.UpsertFlightInventoryParams) (store.FlightInventory, error) {
	s.fail("UpsertFlightInventory")
	return store.FlightInventory{}, nil
}

func (s unusedStore) ConfirmBooking(context.Context, store.ConfirmBookingParams) (store.Booking, error) {
	s.fail("ConfirmBooking")
	return store.Booking{}, nil
}

func (s unusedStore) GetBookingPayment(context.Context, string) (store.GetBookingPaymentRow, error) {
	s.fail("GetBookingPayment")
	return store.GetBookingPaymentRow{}, nil
}

func (s unusedStore) SellHeldSeats(context.Context, store.SellHeldSeatsParams) error {
	s.fail("SellHeldSeats")
	return nil
}

func (s unusedStore) SummarizeBookingsByStatus(context.Context, store.SummarizeBookingsByStatusParams) ([]store.SummarizeBookingsByStatusRow, error) {
	s.fail("SummarizeBookingsByStatus")
	return nil, nil
}

func (s unusedStore) ListTopRoutes(context.Context, store.ListTopRoutesParams) ([]store.ListTopRoutesRow, error) {
	s.fail("ListTopRoutes")
	return nil, nil
}

func (s unusedStore) InTx(context.Context, func(queries) error) error {
	s.fail("InTx")
	return nil
}

func TestServiceHold_rejectsAnInvalidRequestBeforeTouchingTheDatabase(t *testing.T) {
	svc := newService(unusedStore{t}, 15*time.Minute, time.Now)

	_, err := svc.Hold(t.Context(), johnID, HoldRequest{FlightID: "not-a-uuid", Passengers: twoPassengers()})

	var validationErr *ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "flight_id", validationErr.Details[0].Field)
}

func TestServiceGetBookingAndCancelBooking_treatAMalformedIDAsNotFound(t *testing.T) {
	svc := newService(unusedStore{t}, 15*time.Minute, time.Now)

	_, err := svc.GetBooking(t.Context(), johnID, "not-a-uuid")
	require.ErrorIs(t, err, ErrNotFound)

	_, err = svc.CancelBooking(t.Context(), johnID, "not-a-uuid")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestServiceHold_reservesSeatsAndRecordsTheBooking(t *testing.T) {
	svc, pool, now := newTestService(t)
	departure := now.Add(7 * 24 * time.Hour)
	addFlight(t, pool, testFlightID, departure, 180)

	held, err := svc.Hold(t.Context(), johnID, HoldRequest{
		FlightID: testFlightID,
		Passengers: []PassengerRequest{
			{FullName: "  John Doe ", BirthDate: "1990-05-17"},
			{FullName: "Jane Smith", BirthDate: "1992-08-03"},
		},
	})
	require.NoError(t, err)

	require.Equal(t, "HELD", held.Status)
	require.Equal(t, int32(2), held.PassengerCount)
	require.Equal(t, int64(1_250_000), held.SeatPrice)
	require.Equal(t, int64(2_500_000), held.TotalPrice)
	require.True(t, now.Add(15*time.Minute).Equal(held.ExpiresAt))
	require.Equal(t, testFlightID, held.Flight.ID)
	require.Equal(t, "GA410", held.Flight.FlightNumber)
	require.Equal(t, []PassengerResponse{
		{FullName: "John Doe", BirthDate: "1990-05-17"},
		{FullName: "Jane Smith", BirthDate: "1992-08-03"},
	}, held.Passengers)

	inventory := getInventory(t, pool)
	require.Equal(t, int32(2), inventory.HeldSeats)
	require.Equal(t, int64(2), inventory.AvailabilityVersion, "1 when the flight arrived, 2 after the hold")
	requireOutbox(t, pool,
		fmt.Sprintf(`{"flight_id":%q,"availability_version":2,"available_seats":178}`, testFlightID))

	fetched, err := svc.GetBooking(t.Context(), johnID, held.ID)
	require.NoError(t, err)
	require.Equal(t, held, fetched)
}

func TestServiceHold_refusesAFlightThatCannotTakeTheParty(t *testing.T) {
	const departedID = "3b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"
	const smallID = "4b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"

	svc, pool, now := newTestService(t)
	addFlight(t, pool, departedID, now.Add(-time.Hour), 180)
	addFlight(t, pool, smallID, now.Add(24*time.Hour), 1)

	for _, tc := range []struct {
		name     string
		flightID string
		want     error
	}{
		{"unknown flight", "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e", ErrFlightNotFound},
		{"departed flight", departedID, ErrFlightDeparted},
		{"flight with fewer free seats than passengers", smallID, ErrNotEnoughSeats},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Hold(t.Context(), johnID, HoldRequest{FlightID: tc.flightID, Passengers: twoPassengers()})

			require.ErrorIs(t, err, tc.want)
		})
	}

	require.Zero(t, count(t, pool, "bookings"))
	require.Zero(t, count(t, pool, "outbox"))
}

func TestServiceHold_neverSellsTheSameSeatTwice(t *testing.T) {
	const seats, buyers = 5, 20

	svc, pool, now := newTestService(t)
	addFlight(t, pool, testFlightID, now.Add(24*time.Hour), seats)

	errs := make([]error, buyers)
	var wg sync.WaitGroup
	for i := range buyers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = svc.Hold(context.Background(), johnID, HoldRequest{
				FlightID:   testFlightID,
				Passengers: []PassengerRequest{{FullName: "John Doe", BirthDate: "1990-05-17"}},
			})
		}()
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, ErrNotEnoughSeats)
	}
	require.Equal(t, seats, succeeded)
	require.Equal(t, int32(seats), getInventory(t, pool).HeldSeats)
	require.Equal(t, seats, count(t, pool, "bookings"))
}

func TestServiceGetBooking_hidesBookingsOfOtherUsers(t *testing.T) {
	svc, pool, now := newTestService(t)
	addFlight(t, pool, testFlightID, now.Add(24*time.Hour), 180)
	held, err := svc.Hold(t.Context(), johnID, HoldRequest{FlightID: testFlightID, Passengers: twoPassengers()})
	require.NoError(t, err)

	_, err = svc.GetBooking(t.Context(), janeID, held.ID)
	require.ErrorIs(t, err, ErrNotFound)

	_, err = svc.GetBooking(t.Context(), johnID, "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestServiceListBookings_pagesThroughTheUsersBookingsNewestFirst(t *testing.T) {
	svc, pool, now := newTestService(t)
	addFlight(t, pool, testFlightID, now.Add(24*time.Hour), 180)

	var johns []string
	for party := 1; party <= 3; party++ {
		held, err := svc.Hold(t.Context(), johnID, HoldRequest{FlightID: testFlightID, Passengers: twoPassengers()[:min(party, 2)]})
		require.NoError(t, err)
		johns = append(johns, held.ID)
	}
	_, err := svc.Hold(t.Context(), janeID, HoldRequest{FlightID: testFlightID, Passengers: twoPassengers()})
	require.NoError(t, err)

	first, err := svc.ListBookings(t.Context(), johnID, ListRequest{Page: 1, PageSize: 2})
	require.NoError(t, err)
	require.Equal(t, PageMeta{Page: 1, PageSize: 2, Total: 3}, first.Meta)
	require.Len(t, first.Data, 2)
	require.Equal(t, johns[2], first.Data[0].ID)
	require.Equal(t, johns[1], first.Data[1].ID)
	require.Len(t, first.Data[1].Passengers, 2, "each booking carries its own passengers")

	second, err := svc.ListBookings(t.Context(), johnID, ListRequest{Page: 2, PageSize: 2})
	require.NoError(t, err)
	require.Len(t, second.Data, 1)
	require.Equal(t, johns[0], second.Data[0].ID)
	require.Len(t, second.Data[0].Passengers, 1)

	empty, err := svc.ListBookings(t.Context(), johnID, ListRequest{Page: 3, PageSize: 2})
	require.NoError(t, err)
	require.NotNil(t, empty.Data, "an empty page is an empty list, not null")
	require.Empty(t, empty.Data)
}

func TestServiceCancelBooking_releasesTheSeatsOfAHeldBooking(t *testing.T) {
	svc, pool, now := newTestService(t)
	addFlight(t, pool, testFlightID, now.Add(24*time.Hour), 180)
	held, err := svc.Hold(t.Context(), johnID, HoldRequest{FlightID: testFlightID, Passengers: twoPassengers()})
	require.NoError(t, err)

	_, err = svc.CancelBooking(t.Context(), janeID, held.ID)
	require.ErrorIs(t, err, ErrNotFound, "another user's booking looks missing")

	cancelled, err := svc.CancelBooking(t.Context(), johnID, held.ID)
	require.NoError(t, err)
	require.Equal(t, "CANCELLED", cancelled.Status)
	require.Len(t, cancelled.Passengers, 2)

	inventory := getInventory(t, pool)
	require.Zero(t, inventory.HeldSeats)
	require.Equal(t, int64(3), inventory.AvailabilityVersion)
	requireOutbox(t, pool,
		fmt.Sprintf(`{"flight_id":%q,"availability_version":2,"available_seats":178}`, testFlightID),
		fmt.Sprintf(`{"flight_id":%q,"availability_version":3,"available_seats":180}`, testFlightID))

	_, err = svc.CancelBooking(t.Context(), johnID, held.ID)
	require.ErrorIs(t, err, ErrNotHeld)
	require.Zero(t, getInventory(t, pool).HeldSeats, "a second cancel releases nothing")
}

func newTestService(t *testing.T) (*service, *pgxpool.Pool, time.Time) {
	t.Helper()

	pool := newTestPool(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	return newService(NewPgStore(pool), 15*time.Minute, func() time.Time { return now }), pool, now
}

func addFlight(t *testing.T, pool *pgxpool.Pool, id string, departure time.Time, capacity int32) {
	t.Helper()

	_, err := store.New(pool).UpsertFlightInventory(t.Context(), store.UpsertFlightInventoryParams{
		FlightID:        id,
		Version:         1,
		FlightNumber:    "GA410",
		AirlineCode:     "GA",
		OriginCode:      "CGK",
		DestinationCode: "DPS",
		DepartureAt:     departure,
		ArrivalAt:       departure.Add(2 * time.Hour),
		Price:           1_250_000,
		Capacity:        capacity,
	})
	require.NoError(t, err)
}

func getInventory(t *testing.T, pool *pgxpool.Pool) store.FlightInventory {
	t.Helper()

	inventory, err := store.New(pool).GetFlightInventory(t.Context(), testFlightID)
	require.NoError(t, err)
	return inventory
}

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()

	var n int
	require.NoError(t, pool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&n))
	return n
}

// requireOutbox checks the availability events in the outbox, oldest first.
func requireOutbox(t *testing.T, pool *pgxpool.Pool, wantPayloads ...string) {
	t.Helper()

	rows, err := pool.Query(t.Context(), `SELECT event_type, payload FROM outbox ORDER BY created_at, id`)
	require.NoError(t, err)
	defer rows.Close()

	var got []string
	for rows.Next() {
		var eventType string
		var payload []byte
		require.NoError(t, rows.Scan(&eventType, &payload))
		require.Equal(t, "flight.availability_changed", eventType)
		got = append(got, string(payload))
	}
	require.NoError(t, rows.Err())

	require.Len(t, got, len(wantPayloads))
	for i, want := range wantPayloads {
		require.JSONEq(t, want, got[i])
	}
}

func TestServiceFlightSeats_returnsTheSeatCountersOfTheInventory(t *testing.T) {
	svc, pool, now := newTestService(t)
	addFlight(t, pool, testFlightID, now.Add(7*24*time.Hour), 180)
	_, err := svc.Hold(t.Context(), johnID, HoldRequest{FlightID: testFlightID, Passengers: twoPassengers()})
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `UPDATE flight_inventories SET sold_seats = 5`)
	require.NoError(t, err)

	seats, err := svc.FlightSeats(t.Context(), testFlightID)
	require.NoError(t, err)

	require.Equal(t, FlightSeats{FlightID: testFlightID, Capacity: 180, HeldSeats: 2, SoldSeats: 5}, seats)
}

func TestServiceFlightSeats_reportsAFlightBookingDoesNotKnow(t *testing.T) {
	svc, _, _ := newTestService(t)

	_, err := svc.FlightSeats(t.Context(), "33333333-3333-4333-8333-333333333333")

	require.ErrorIs(t, err, ErrFlightNotFound)
}

func TestServiceFlightSeats_treatsAMalformedIDAsUnknownWithoutQuerying(t *testing.T) {
	svc := newService(unusedStore{t}, 15*time.Minute, time.Now)

	_, err := svc.FlightSeats(t.Context(), "not-a-uuid")

	require.ErrorIs(t, err, ErrFlightNotFound)
}
