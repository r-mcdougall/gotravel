package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

const sampleFlightID = "0b5c2a2e-7b53-4a3b-9d0e-2f6d4c1c9a11"

type fakeStore struct {
	t                     *testing.T
	createFlight          func(context.Context, store.CreateFlightParams) (store.Flight, error)
	getFlight             func(context.Context, string) (store.Flight, error)
	getFlightForUpdate    func(context.Context, string) (store.Flight, error)
	updateFlight          func(context.Context, store.UpdateFlightParams) (store.Flight, error)
	listFlights           func(context.Context, store.ListFlightsParams) ([]store.Flight, error)
	listFlightsByRouteDay func(context.Context, store.ListFlightsByRouteDayParams) ([]store.Flight, error)
	countFlights          func(context.Context, store.CountFlightsParams) (int64, error)
	insertOutboxEvent     func(context.Context, store.InsertOutboxEventParams) error
}

func (f *fakeStore) CreateFlight(ctx context.Context, arg store.CreateFlightParams) (store.Flight, error) {
	f.t.Helper()
	if f.createFlight == nil {
		f.t.Fatal("unexpected call to CreateFlight")
	}
	return f.createFlight(ctx, arg)
}

func (f *fakeStore) GetFlight(ctx context.Context, id string) (store.Flight, error) {
	f.t.Helper()
	if f.getFlight == nil {
		f.t.Fatal("unexpected call to GetFlight")
	}
	return f.getFlight(ctx, id)
}

func (f *fakeStore) GetFlightForUpdate(ctx context.Context, id string) (store.Flight, error) {
	f.t.Helper()
	if f.getFlightForUpdate == nil {
		f.t.Fatal("unexpected call to GetFlightForUpdate")
	}
	return f.getFlightForUpdate(ctx, id)
}

func (f *fakeStore) UpdateFlight(ctx context.Context, arg store.UpdateFlightParams) (store.Flight, error) {
	f.t.Helper()
	if f.updateFlight == nil {
		f.t.Fatal("unexpected call to UpdateFlight")
	}
	return f.updateFlight(ctx, arg)
}

func (f *fakeStore) ListFlights(ctx context.Context, arg store.ListFlightsParams) ([]store.Flight, error) {
	f.t.Helper()
	if f.listFlights == nil {
		f.t.Fatal("unexpected call to ListFlights")
	}
	return f.listFlights(ctx, arg)
}

func (f *fakeStore) ListFlightsByRouteDay(ctx context.Context, arg store.ListFlightsByRouteDayParams) ([]store.Flight, error) {
	f.t.Helper()
	if f.listFlightsByRouteDay == nil {
		f.t.Fatal("unexpected call to ListFlightsByRouteDay")
	}
	return f.listFlightsByRouteDay(ctx, arg)
}

func (f *fakeStore) CountFlights(ctx context.Context, arg store.CountFlightsParams) (int64, error) {
	f.t.Helper()
	if f.countFlights == nil {
		f.t.Fatal("unexpected call to CountFlights")
	}
	return f.countFlights(ctx, arg)
}

func (f *fakeStore) InsertOutboxEvent(ctx context.Context, arg store.InsertOutboxEventParams) error {
	f.t.Helper()
	if f.insertOutboxEvent == nil {
		f.t.Fatal("unexpected call to InsertOutboxEvent")
	}
	return f.insertOutboxEvent(ctx, arg)
}

func (f *fakeStore) InTx(_ context.Context, fn func(queries) error) error {
	return fn(f)
}

const adminAuthorization = "Bearer admin-token"

// fakeSeats answers for booking; a nil flightSeats fails the test, so booking must not be asked.
type fakeSeats struct {
	t           *testing.T
	flightSeats func(authorization, flightID string) (BookedSeats, bool, error)
}

func (f *fakeSeats) FlightSeats(_ context.Context, authorization, flightID string) (BookedSeats, bool, error) {
	f.t.Helper()
	if f.flightSeats == nil {
		f.t.Fatal("unexpected call to FlightSeats")
	}
	return f.flightSeats(authorization, flightID)
}

// bookingHolds is a booking that reports held and sold seats for sampleFlight, and checks the admin's token.
func bookingHolds(held, sold int32) *fakeSeats {
	return &fakeSeats{flightSeats: func(authorization, flightID string) (BookedSeats, bool, error) {
		if authorization != adminAuthorization || flightID != sampleFlightID {
			return BookedSeats{}, false, fmt.Errorf("unexpected booking call with %q for %s", authorization, flightID)
		}
		return BookedSeats{HeldSeats: held, SoldSeats: sold}, true, nil
	}}
}

func newTestService(t *testing.T, db *fakeStore) Service {
	t.Helper()
	return newTestServiceAt(t, db, time.Now())
}

// newTestServiceAt pins the clock, so tests decide which flights already departed.
func newTestServiceAt(t *testing.T, db *fakeStore, now time.Time) Service {
	t.Helper()
	return newTestServiceWithCache(t, db, nil, now)
}

// newTestServiceWithCache is newTestServiceAt with a route-day cache; nil disables it.
func newTestServiceWithCache(t *testing.T, db *fakeStore, routeCache *RouteDayCache, now time.Time) Service {
	t.Helper()
	return newTestServiceWithSeats(t, db, routeCache, &fakeSeats{}, now)
}

// newTestServiceWithSeats is newTestServiceWithCache with a booking that reports seats.
func newTestServiceWithSeats(t *testing.T, db *fakeStore, routeCache *RouteDayCache, seats *fakeSeats, now time.Time) Service {
	t.Helper()
	db.t = t
	seats.t = t

	reference, err := LoadReference(t.Context(), sampleReferenceQueries(t))
	require.NoError(t, err)

	return newService(db, reference, routeCache, seats, func() time.Time { return now })
}

// sampleFlight is GA410 from Jakarta to Denpasar: 06:00 WIB, 08:55 WITA.
func sampleFlight() store.Flight {
	return store.Flight{
		ID:              sampleFlightID,
		FlightNumber:    "GA410",
		AirlineCode:     "GA",
		OriginCode:      "CGK",
		DestinationCode: "DPS",
		DepartureAt:     time.Date(2026, 11, 20, 6, 0, 0, 0, wib).UTC(),
		ArrivalAt:       time.Date(2026, 11, 20, 8, 55, 0, 0, wita).UTC(),
		Price:           1_250_000,
		Capacity:        180,
		AvailableSeats:  42,
		Version:         3,
		CreatedAt:       time.Date(2026, 11, 10, 3, 0, 0, 0, time.UTC),
		UpdatedAt:       time.Date(2026, 11, 10, 4, 0, 0, 0, time.UTC),
	}
}

func uniqueViolation(constraint string) error {
	return &pgconn.PgError{Code: uniqueViolationCode, ConstraintName: constraint}
}

func outboxPayload(t *testing.T, event store.InsertOutboxEventParams) flightChangedData {
	t.Helper()
	require.Equal(t, eventFlightChanged, event.EventType)

	var data flightChangedData
	require.NoError(t, json.Unmarshal(event.Payload, &data))
	return data
}

func TestServiceCreateFlight_persistsTheFlightAndRecordsAnOutboxEvent(t *testing.T) {
	var (
		created store.CreateFlightParams
		event   store.InsertOutboxEventParams
	)
	db := &fakeStore{
		createFlight: func(_ context.Context, arg store.CreateFlightParams) (store.Flight, error) {
			created = arg
			flight := sampleFlight()
			flight.Version = 1
			flight.AvailableSeats = arg.AvailableSeats
			return flight, nil
		},
		insertOutboxEvent: func(_ context.Context, arg store.InsertOutboxEventParams) error {
			event = arg
			return nil
		},
	}
	svc := newTestService(t, db)

	response, err := svc.CreateFlight(t.Context(), validCreateRequest())
	require.NoError(t, err)

	require.Equal(t, int32(180), created.Capacity)
	require.Equal(t, int32(180), created.AvailableSeats)
	require.Equal(t, int32(180), response.AvailableSeats)

	data := outboxPayload(t, event)
	require.Equal(t, sampleFlightID, data.FlightID)
	require.Equal(t, int32(1), data.Version)
	require.Equal(t, int32(180), data.Capacity)
}

func TestServiceCreateFlight_showsEachTimeInItsOwnAirportOffset(t *testing.T) {
	db := &fakeStore{
		createFlight:      func(context.Context, store.CreateFlightParams) (store.Flight, error) { return sampleFlight(), nil },
		insertOutboxEvent: func(context.Context, store.InsertOutboxEventParams) error { return nil },
	}
	svc := newTestService(t, db)

	response, err := svc.CreateFlight(t.Context(), validCreateRequest())
	require.NoError(t, err)

	require.Equal(t, "2026-11-20T06:00:00+07:00", response.DepartureAt.Format(time.RFC3339))
	require.Equal(t, "2026-11-20T08:55:00+08:00", response.ArrivalAt.Format(time.RFC3339))
	require.Equal(t, 115, response.DurationMinutes)
	require.Equal(t, "Garuda Indonesia", response.Airline.Name)
	require.Equal(t, "Denpasar", response.Destination.City)
}

func TestServiceCreateFlight_rejectsAnInvalidRequestWithoutTouchingTheStore(t *testing.T) {
	svc := newTestService(t, &fakeStore{})

	_, err := svc.CreateFlight(t.Context(), CreateFlightRequest{})

	require.Equal(t, []string{
		"flight_number", "airline_code", "origin_code", "destination_code",
		"departure_at", "arrival_at", "price", "capacity",
	}, invalidFields(t, err))
}

func TestServiceCreateFlight_rejectsUnknownAirlineAndAirports(t *testing.T) {
	svc := newTestService(t, &fakeStore{})
	request := validCreateRequest()
	request.FlightNumber = "XX410"
	request.AirlineCode = "XX"
	request.OriginCode = "AAA"
	request.DestinationCode = "BBB"

	_, err := svc.CreateFlight(t.Context(), request)

	require.Equal(t, []string{"airline_code", "origin_code", "destination_code"}, invalidFields(t, err))
}

func TestServiceCreateFlight_reportsAFlightThatAlreadyExists(t *testing.T) {
	db := &fakeStore{
		createFlight: func(context.Context, store.CreateFlightParams) (store.Flight, error) {
			return store.Flight{}, uniqueViolation(flightScheduleKey)
		},
	}
	svc := newTestService(t, db)

	_, err := svc.CreateFlight(t.Context(), validCreateRequest())

	require.ErrorIs(t, err, ErrFlightExists)
}

func TestServiceCreateFlight_doesNotMistakeOtherDatabaseErrorsForADuplicate(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"unique violation of another constraint", uniqueViolation("flights_pkey")},
		{"unrelated failure", errBoom},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeStore{
				createFlight: func(context.Context, store.CreateFlightParams) (store.Flight, error) {
					return store.Flight{}, tc.err
				},
			}
			svc := newTestService(t, db)

			_, err := svc.CreateFlight(t.Context(), validCreateRequest())

			require.ErrorIs(t, err, tc.err)
			require.False(t, errors.Is(err, ErrFlightExists))
		})
	}
}

func TestServiceCreateFlight_failsWhenTheOutboxInsertFails(t *testing.T) {
	db := &fakeStore{
		createFlight:      func(context.Context, store.CreateFlightParams) (store.Flight, error) { return sampleFlight(), nil },
		insertOutboxEvent: func(context.Context, store.InsertOutboxEventParams) error { return errBoom },
	}
	svc := newTestService(t, db)

	_, err := svc.CreateFlight(t.Context(), validCreateRequest())

	require.ErrorIs(t, err, errBoom)
}

// updateDB returns a store holding sampleFlight (180 seats, 42 available, so 138 sold)
// that applies updates the way PostgreSQL would, and records what it was asked to write.
func updateDB(updated *store.UpdateFlightParams, event *store.InsertOutboxEventParams) *fakeStore {
	return &fakeStore{
		getFlight:          func(context.Context, string) (store.Flight, error) { return sampleFlight(), nil },
		getFlightForUpdate: func(context.Context, string) (store.Flight, error) { return sampleFlight(), nil },
		updateFlight: func(_ context.Context, arg store.UpdateFlightParams) (store.Flight, error) {
			*updated = arg
			flight := sampleFlight()
			flight.DepartureAt = arg.DepartureAt
			flight.ArrivalAt = arg.ArrivalAt
			flight.Price = arg.Price
			flight.Capacity = arg.Capacity
			flight.AvailableSeats = arg.AvailableSeats
			flight.Version++
			return flight, nil
		},
		insertOutboxEvent: func(_ context.Context, arg store.InsertOutboxEventParams) error {
			*event = arg
			return nil
		},
	}
}

func TestServiceUpdateFlight_changesThePriceAndKeepsEverythingElse(t *testing.T) {
	var (
		updated store.UpdateFlightParams
		event   store.InsertOutboxEventParams
	)
	svc := newTestService(t, updateDB(&updated, &event))
	current := sampleFlight()

	response, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{Price: ptr(int64(900_000))})
	require.NoError(t, err)

	require.Equal(t, sampleFlightID, updated.ID)
	require.Equal(t, int64(900_000), updated.Price)
	require.Equal(t, current.DepartureAt, updated.DepartureAt)
	require.Equal(t, current.ArrivalAt, updated.ArrivalAt)
	require.Equal(t, current.Capacity, updated.Capacity)
	require.Equal(t, current.AvailableSeats, updated.AvailableSeats)
	require.Equal(t, int64(900_000), response.Price)
}

func TestServiceUpdateFlight_recordsAnEventWithTheNewVersion(t *testing.T) {
	var (
		updated store.UpdateFlightParams
		event   store.InsertOutboxEventParams
	)
	svc := newTestService(t, updateDB(&updated, &event))

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{Price: ptr(int64(900_000))})
	require.NoError(t, err)

	data := outboxPayload(t, event)
	require.Equal(t, int32(4), data.Version)
	require.Equal(t, int64(900_000), data.Price)
}

func TestServiceUpdateFlight_replacesTheSchedule(t *testing.T) {
	var (
		updated store.UpdateFlightParams
		event   store.InsertOutboxEventParams
	)
	svc := newTestService(t, updateDB(&updated, &event))
	departure := time.Date(2026, 11, 21, 7, 0, 0, 0, wib)
	arrival := time.Date(2026, 11, 21, 9, 55, 0, 0, wita)

	response, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{DepartureAt: &departure, ArrivalAt: &arrival})
	require.NoError(t, err)

	require.True(t, departure.Equal(updated.DepartureAt))
	require.True(t, arrival.Equal(updated.ArrivalAt))
	require.Equal(t, "2026-11-21T07:00:00+07:00", response.DepartureAt.Format(time.RFC3339))
}

func TestServiceUpdateFlight_movesAvailableSeatsWithCapacity(t *testing.T) {
	tests := []struct {
		name      string
		capacity  int32
		booking   *fakeSeats
		available int32
	}{
		{"capacity grows without asking booking", 200, &fakeSeats{}, 62},
		{"capacity shrinks", 150, bookingHolds(10, 128), 12},
		{"capacity drops to exactly the seats booking holds and sold", 138, bookingHolds(10, 128), 0},
		{"the local count goes no lower than zero", 100, bookingHolds(10, 80), 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				updated store.UpdateFlightParams
				event   store.InsertOutboxEventParams
			)
			svc := newTestServiceWithSeats(t, updateDB(&updated, &event), nil, tc.booking, time.Now())

			response, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{Capacity: ptr(tc.capacity)})
			require.NoError(t, err)

			require.Equal(t, tc.capacity, updated.Capacity)
			require.Equal(t, tc.available, updated.AvailableSeats)
			require.Equal(t, tc.available, response.AvailableSeats)
		})
	}
}

func TestServiceUpdateFlight_rejectsCapacityBelowTheSeatsBookingHoldsAndSold(t *testing.T) {
	db := &fakeStore{
		getFlight: func(context.Context, string) (store.Flight, error) { return sampleFlight(), nil },
		// No transaction: the update is refused before the row is locked.
	}
	svc := newTestServiceWithSeats(t, db, nil, bookingHolds(5, 140), time.Now())

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{Capacity: ptr(int32(144))})

	require.ErrorIs(t, err, ErrCapacityBelowSold)
}

func TestServiceUpdateFlight_lowersTheCapacityOfAFlightBookingDoesNotKnowYet(t *testing.T) {
	var (
		updated store.UpdateFlightParams
		event   store.InsertOutboxEventParams
	)
	unknown := &fakeSeats{flightSeats: func(string, string) (BookedSeats, bool, error) { return BookedSeats{}, false, nil }}
	svc := newTestServiceWithSeats(t, updateDB(&updated, &event), nil, unknown, time.Now())

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{Capacity: ptr(int32(10))})

	require.NoError(t, err)
	require.Equal(t, int32(10), updated.Capacity)
}

func TestServiceUpdateFlight_refusesToLowerTheCapacityWhileBookingIsUnavailable(t *testing.T) {
	db := &fakeStore{
		getFlight: func(context.Context, string) (store.Flight, error) { return sampleFlight(), nil },
	}
	down := &fakeSeats{flightSeats: func(string, string) (BookedSeats, bool, error) {
		return BookedSeats{}, false, fmt.Errorf("%w: connection refused", ErrBookingUnavailable)
	}}
	svc := newTestServiceWithSeats(t, db, nil, down, time.Now())

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{Capacity: ptr(int32(100))})

	require.ErrorIs(t, err, ErrBookingUnavailable)
}

func TestServiceUpdateFlight_reportsAMissingFlightBeforeAskingBooking(t *testing.T) {
	db := &fakeStore{
		getFlight: func(context.Context, string) (store.Flight, error) { return store.Flight{}, pgx.ErrNoRows },
	}
	svc := newTestServiceWithSeats(t, db, nil, &fakeSeats{}, time.Now())

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{Capacity: ptr(int32(100))})

	require.ErrorIs(t, err, ErrFlightNotFound)
}

func TestServiceUpdateFlight_reportsAMissingFlight(t *testing.T) {
	db := &fakeStore{
		getFlightForUpdate: func(context.Context, string) (store.Flight, error) { return store.Flight{}, pgx.ErrNoRows },
	}
	svc := newTestService(t, db)

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{Price: ptr(int64(900_000))})

	require.ErrorIs(t, err, ErrFlightNotFound)
}

func TestServiceUpdateFlight_treatsAMalformedIDAsMissing(t *testing.T) {
	svc := newTestService(t, &fakeStore{})

	_, err := svc.UpdateFlight(t.Context(), "not-a-uuid", adminAuthorization, UpdateFlightRequest{Price: ptr(int64(900_000))})

	require.ErrorIs(t, err, ErrFlightNotFound)
}

func TestServiceUpdateFlight_rejectsAnInvalidRequestWithoutTouchingTheStore(t *testing.T) {
	svc := newTestService(t, &fakeStore{})

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{})

	require.Equal(t, []string{"body"}, invalidFields(t, err))
}

func TestServiceUpdateFlight_reportsAScheduleThatCollidesWithAnotherFlight(t *testing.T) {
	db := &fakeStore{
		getFlightForUpdate: func(context.Context, string) (store.Flight, error) { return sampleFlight(), nil },
		updateFlight: func(context.Context, store.UpdateFlightParams) (store.Flight, error) {
			return store.Flight{}, uniqueViolation(flightScheduleKey)
		},
	}
	svc := newTestService(t, db)
	departure := time.Date(2026, 11, 21, 7, 0, 0, 0, wib)
	arrival := time.Date(2026, 11, 21, 9, 55, 0, 0, wita)

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{DepartureAt: &departure, ArrivalAt: &arrival})

	require.ErrorIs(t, err, ErrFlightExists)
}

func TestServiceGetFlight_returnsTheFlightWithLocalTimes(t *testing.T) {
	db := &fakeStore{
		getFlight: func(_ context.Context, id string) (store.Flight, error) {
			require.Equal(t, sampleFlightID, id)
			return sampleFlight(), nil
		},
	}
	svc := newTestService(t, db)

	response, err := svc.GetFlight(t.Context(), sampleFlightID)
	require.NoError(t, err)

	require.Equal(t, "GA410", response.FlightNumber)
	require.Equal(t, "2026-11-20T06:00:00+07:00", response.DepartureAt.Format(time.RFC3339))
	require.Equal(t, int32(180), response.Capacity)
	require.Equal(t, int32(42), response.AvailableSeats)
}

func TestServiceGetFlight_reportsAMissingFlight(t *testing.T) {
	db := &fakeStore{
		getFlight: func(context.Context, string) (store.Flight, error) { return store.Flight{}, pgx.ErrNoRows },
	}
	svc := newTestService(t, db)

	_, err := svc.GetFlight(t.Context(), sampleFlightID)

	require.ErrorIs(t, err, ErrFlightNotFound)
}

func TestServiceGetFlight_treatsAMalformedIDAsMissing(t *testing.T) {
	svc := newTestService(t, &fakeStore{})

	_, err := svc.GetFlight(t.Context(), "not-a-uuid")

	require.ErrorIs(t, err, ErrFlightNotFound)
}

func TestServiceListFlights_passesFiltersAndPagingToTheStore(t *testing.T) {
	date := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	var (
		listed  store.ListFlightsParams
		counted store.CountFlightsParams
	)
	db := &fakeStore{
		listFlights: func(_ context.Context, arg store.ListFlightsParams) ([]store.Flight, error) {
			listed = arg
			return []store.Flight{sampleFlight()}, nil
		},
		countFlights: func(_ context.Context, arg store.CountFlightsParams) (int64, error) {
			counted = arg
			return 57, nil
		},
	}
	svc := newTestService(t, db)

	response, err := svc.ListFlights(t.Context(), ListFlightsRequest{Origin: "CGK", Date: &date, Page: 3, PageSize: 20})
	require.NoError(t, err)

	require.Equal(t, ptr("CGK"), listed.OriginCode)
	require.Nil(t, listed.DestinationCode)
	require.Equal(t, &date, listed.DepartureDate)
	require.Equal(t, int32(40), listed.RowOffset)
	require.Equal(t, int32(20), listed.RowLimit)
	require.Equal(t, ptr("CGK"), counted.OriginCode)
	require.Equal(t, &date, counted.DepartureDate)

	require.Len(t, response.Data, 1)
	require.Equal(t, PageMeta{Page: 3, PageSize: 20, Total: 57}, response.Meta)
}

func TestServiceListFlights_returnsAnEmptyListInsteadOfNull(t *testing.T) {
	db := &fakeStore{
		listFlights:  func(context.Context, store.ListFlightsParams) ([]store.Flight, error) { return nil, nil },
		countFlights: func(context.Context, store.CountFlightsParams) (int64, error) { return 0, nil },
	}
	svc := newTestService(t, db)

	response, err := svc.ListFlights(t.Context(), ListFlightsRequest{Page: defaultPage, PageSize: defaultPageSize})
	require.NoError(t, err)

	require.Equal(t, []AdminFlightResponse{}, response.Data)
}

func TestServiceListFlights_rejectsAnInvalidRequestWithoutTouchingTheStore(t *testing.T) {
	svc := newTestService(t, &fakeStore{})

	_, err := svc.ListFlights(t.Context(), ListFlightsRequest{Origin: "cgk", Page: 0, PageSize: 101})

	require.Equal(t, []string{"origin", "page", "page_size"}, invalidFields(t, err))
}

func TestServiceListFlights_failsWhenCountingFails(t *testing.T) {
	db := &fakeStore{
		listFlights:  func(context.Context, store.ListFlightsParams) ([]store.Flight, error) { return nil, nil },
		countFlights: func(context.Context, store.CountFlightsParams) (int64, error) { return 0, errBoom },
	}
	svc := newTestService(t, db)

	_, err := svc.ListFlights(t.Context(), ListFlightsRequest{Page: defaultPage, PageSize: defaultPageSize})

	require.ErrorIs(t, err, errBoom)
}

// searchTestNow is before every departure in sampleSearchFlights.
var searchTestNow = time.Date(2026, 11, 20, 5, 0, 0, 0, wib)

func sampleSearchFlight(id string, departure time.Time, price int64) store.Flight {
	flight := sampleFlight()
	flight.ID = id
	flight.DepartureAt = departure.UTC()
	flight.ArrivalAt = departure.Add(1*time.Hour + 55*time.Minute).UTC()
	flight.Price = price
	return flight
}

func TestServiceSearchFlights_queriesTheLocalDayAtTheOriginAirport(t *testing.T) {
	var got store.ListFlightsByRouteDayParams
	db := &fakeStore{
		listFlightsByRouteDay: func(_ context.Context, arg store.ListFlightsByRouteDayParams) ([]store.Flight, error) {
			got = arg
			return nil, nil
		},
	}
	service := newTestServiceAt(t, db, searchTestNow)

	_, err := service.SearchFlights(t.Context(), validSearchRequest())

	require.NoError(t, err)
	require.Equal(t, "CGK", got.OriginCode)
	require.Equal(t, "DPS", got.DestinationCode)
	require.True(t, time.Date(2026, 11, 20, 0, 0, 0, 0, wib).Equal(got.DayStart), "day starts at local midnight in Jakarta, got %s", got.DayStart)
	require.True(t, time.Date(2026, 11, 21, 0, 0, 0, 0, wib).Equal(got.DayEnd), "day ends at the next local midnight, got %s", got.DayEnd)
}

func TestServiceSearchFlights_usesTheOriginZoneForTheDayBounds(t *testing.T) {
	var got store.ListFlightsByRouteDayParams
	db := &fakeStore{
		listFlightsByRouteDay: func(_ context.Context, arg store.ListFlightsByRouteDayParams) ([]store.Flight, error) {
			got = arg
			return nil, nil
		},
	}
	service := newTestServiceAt(t, db, searchTestNow)
	request := validSearchRequest()
	request.Origin, request.Destination = "DPS", "CGK"

	_, err := service.SearchFlights(t.Context(), request)

	require.NoError(t, err)
	require.True(t, time.Date(2026, 11, 20, 0, 0, 0, 0, wita).Equal(got.DayStart), "got %s", got.DayStart)
	require.True(t, time.Date(2026, 11, 21, 0, 0, 0, 0, wita).Equal(got.DayEnd), "got %s", got.DayEnd)
}

func TestServiceSearchFlights_returnsTheMatchingFlightsWithLocalTimesAndPageMeta(t *testing.T) {
	db := &fakeStore{
		listFlightsByRouteDay: func(context.Context, store.ListFlightsByRouteDayParams) ([]store.Flight, error) {
			return []store.Flight{
				sampleSearchFlight("a", time.Date(2026, 11, 20, 6, 0, 0, 0, wib), 1_250_000),
				sampleSearchFlight("b", time.Date(2026, 11, 20, 9, 30, 0, 0, wib), 990_000),
			}, nil
		},
	}
	service := newTestServiceAt(t, db, searchTestNow)

	response, err := service.SearchFlights(t.Context(), validSearchRequest())

	require.NoError(t, err)
	require.Len(t, response.Data, 2)
	require.Equal(t, PageMeta{Page: 1, PageSize: 20, Total: 2}, response.Meta)

	first := response.Data[0]
	require.Equal(t, "a", first.ID)
	require.Equal(t, "GA410", first.FlightNumber)
	require.Equal(t, "Garuda Indonesia", first.Airline.Name)
	require.Equal(t, "CGK", first.Origin.Code)
	require.Equal(t, "DPS", first.Destination.Code)
	require.Equal(t, "2026-11-20T06:00:00+07:00", first.DepartureAt.Format(time.RFC3339))
	require.Equal(t, "2026-11-20T08:55:00+08:00", first.ArrivalAt.Format(time.RFC3339))
	require.Equal(t, 115, first.DurationMinutes)
	require.Equal(t, int64(1_250_000), first.Price)
	require.Equal(t, int32(42), first.AvailableSeats)
}

func TestServiceSearchFlights_appliesFiltersSortingAndPagingToTheDay(t *testing.T) {
	db := &fakeStore{
		listFlightsByRouteDay: func(context.Context, store.ListFlightsByRouteDayParams) ([]store.Flight, error) {
			return []store.Flight{
				sampleSearchFlight("early", time.Date(2026, 11, 20, 6, 0, 0, 0, wib), 900_000),
				sampleSearchFlight("noon", time.Date(2026, 11, 20, 12, 0, 0, 0, wib), 700_000),
				sampleSearchFlight("night", time.Date(2026, 11, 20, 21, 0, 0, 0, wib), 800_000),
			}, nil
		},
	}
	service := newTestServiceAt(t, db, searchTestNow)
	request := validSearchRequest()
	request.Sort = sortPriceAsc
	request.PageSize = 2
	request.Page = 2

	response, err := service.SearchFlights(t.Context(), request)

	require.NoError(t, err)
	require.Equal(t, []string{"early"}, responseIDs(response))
	require.Equal(t, PageMeta{Page: 2, PageSize: 2, Total: 3}, response.Meta)
}

func responseIDs(response FlightListResponse) []string {
	ids := make([]string, len(response.Data))
	for i, flight := range response.Data {
		ids[i] = flight.ID
	}
	return ids
}

func TestServiceSearchFlights_hidesFlightsThatHaveAlreadyDeparted(t *testing.T) {
	db := &fakeStore{
		listFlightsByRouteDay: func(context.Context, store.ListFlightsByRouteDayParams) ([]store.Flight, error) {
			return []store.Flight{
				sampleSearchFlight("gone", time.Date(2026, 11, 20, 6, 0, 0, 0, wib), 900_000),
				sampleSearchFlight("upcoming", time.Date(2026, 11, 20, 12, 0, 0, 0, wib), 900_000),
			}, nil
		},
	}
	now := time.Date(2026, 11, 20, 8, 0, 0, 0, wib)
	service := newTestServiceAt(t, db, now)

	response, err := service.SearchFlights(t.Context(), validSearchRequest())

	require.NoError(t, err)
	require.Equal(t, []string{"upcoming"}, responseIDs(response))
}

func TestServiceSearchFlights_returnsAnEmptyListWhenNothingMatches(t *testing.T) {
	db := &fakeStore{
		listFlightsByRouteDay: func(context.Context, store.ListFlightsByRouteDayParams) ([]store.Flight, error) {
			return nil, nil
		},
	}
	service := newTestServiceAt(t, db, searchTestNow)

	response, err := service.SearchFlights(t.Context(), validSearchRequest())

	require.NoError(t, err)
	require.NotNil(t, response.Data, "data must encode as [] and not null")
	require.Empty(t, response.Data)
	require.Equal(t, PageMeta{Page: 1, PageSize: 20, Total: 0}, response.Meta)
}

func TestServiceSearchFlights_returnsAnEmptyListForAnUnknownAirportWithoutQuerying(t *testing.T) {
	tests := []struct {
		name        string
		origin      string
		destination string
	}{
		{"unknown origin", "ZZZ", "DPS"},
		{"unknown destination", "CGK", "ZZZ"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			service := newTestServiceAt(t, &fakeStore{}, searchTestNow)
			request := validSearchRequest()
			request.Origin, request.Destination = tc.origin, tc.destination

			response, err := service.SearchFlights(t.Context(), request)

			require.NoError(t, err)
			require.NotNil(t, response.Data)
			require.Empty(t, response.Data)
			require.Equal(t, PageMeta{Page: 1, PageSize: 20, Total: 0}, response.Meta)
		})
	}
}

func TestServiceSearchFlights_rejectsAnInvalidRequestWithoutQuerying(t *testing.T) {
	service := newTestServiceAt(t, &fakeStore{}, searchTestNow)
	request := validSearchRequest()
	request.Passengers = 0

	_, err := service.SearchFlights(t.Context(), request)

	var validation *ValidationError
	require.ErrorAs(t, err, &validation)
}

func TestServiceSearchFlights_wrapsADatabaseFailure(t *testing.T) {
	db := &fakeStore{
		listFlightsByRouteDay: func(context.Context, store.ListFlightsByRouteDayParams) ([]store.Flight, error) {
			return nil, errBoom
		},
	}
	service := newTestServiceAt(t, db, searchTestNow)

	_, err := service.SearchFlights(t.Context(), validSearchRequest())

	require.ErrorIs(t, err, errBoom)
}

func TestServiceSearchFlights_failsWhenAFlightReferencesUnknownReferenceData(t *testing.T) {
	db := &fakeStore{
		listFlightsByRouteDay: func(context.Context, store.ListFlightsByRouteDayParams) ([]store.Flight, error) {
			flight := sampleSearchFlight("a", time.Date(2026, 11, 20, 6, 0, 0, 0, wib), 900_000)
			flight.AirlineCode = "ZZ"
			return []store.Flight{flight}, nil
		},
	}
	service := newTestServiceAt(t, db, searchTestNow)
	request := validSearchRequest()

	_, err := service.SearchFlights(t.Context(), request)

	require.ErrorContains(t, err, `unknown airline "ZZ"`)
}

func TestServiceGetFlightDetail_returnsThePublicViewOfAFlight(t *testing.T) {
	db := &fakeStore{
		getFlight: func(_ context.Context, id string) (store.Flight, error) {
			require.Equal(t, sampleFlightID, id)
			return sampleFlight(), nil
		},
	}
	service := newTestService(t, db)

	response, err := service.GetFlightDetail(t.Context(), sampleFlightID)

	require.NoError(t, err)
	require.Equal(t, sampleFlightID, response.ID)
	require.Equal(t, "2026-11-20T06:00:00+07:00", response.DepartureAt.Format(time.RFC3339))
	require.Equal(t, int32(42), response.AvailableSeats)
	require.Equal(t, 115, response.DurationMinutes)
}

func TestServiceGetFlightDetail_stillShowsAFlightThatHasDeparted(t *testing.T) {
	db := &fakeStore{
		getFlight: func(context.Context, string) (store.Flight, error) { return sampleFlight(), nil },
	}
	service := newTestServiceAt(t, db, time.Date(2026, 12, 1, 0, 0, 0, 0, wib))

	response, err := service.GetFlightDetail(t.Context(), sampleFlightID)

	require.NoError(t, err)
	require.Equal(t, sampleFlightID, response.ID)
}

func TestServiceGetFlightDetail_reportsAMalformedIDAsNotFoundWithoutQuerying(t *testing.T) {
	service := newTestService(t, &fakeStore{})

	_, err := service.GetFlightDetail(t.Context(), "not-a-uuid")

	require.ErrorIs(t, err, ErrFlightNotFound)
}

func TestServiceGetFlightDetail_reportsAMissingFlight(t *testing.T) {
	db := &fakeStore{
		getFlight: func(context.Context, string) (store.Flight, error) { return store.Flight{}, pgx.ErrNoRows },
	}
	service := newTestService(t, db)

	_, err := service.GetFlightDetail(t.Context(), sampleFlightID)

	require.ErrorIs(t, err, ErrFlightNotFound)
}

// routeDayDB serves the same two flights for every route-day query and counts the queries.
func routeDayDB(queries *int) *fakeStore {
	return &fakeStore{
		listFlightsByRouteDay: func(context.Context, store.ListFlightsByRouteDayParams) ([]store.Flight, error) {
			*queries++
			return []store.Flight{
				sampleSearchFlight("cheap", time.Date(2026, 11, 20, 10, 0, 0, 0, wib), 700_000),
				sampleSearchFlight("dear", time.Date(2026, 11, 20, 12, 0, 0, 0, wib), 1_900_000),
			}, nil
		},
	}
}

func TestServiceSearchFlights_servesRepeatedSearchesOfARouteDayFromTheCache(t *testing.T) {
	queries := 0
	_, fake := newMemoryCache(t)
	routeCache, _ := newTestRouteDayCache(fake)
	svc := newTestServiceWithCache(t, routeDayDB(&queries), routeCache, searchTestNow)
	affordable := validSearchRequest()
	affordable.MaxPrice = ptr(int64(1_000_000))

	all, err := svc.SearchFlights(t.Context(), validSearchRequest())
	require.NoError(t, err)
	filtered, err := svc.SearchFlights(t.Context(), affordable)
	require.NoError(t, err)

	require.Equal(t, []string{"cheap", "dear"}, responseIDs(all))
	require.Equal(t, []string{"cheap"}, responseIDs(filtered), "filters run on the cached rows, per request")
	require.Equal(t, 1, queries, "one database query serves every search of the route day")
}

func TestServiceSearchFlights_keysTheCacheByRouteAndLocalDate(t *testing.T) {
	var keys []string
	fake := &fakeSearchCache{
		t: t,
		get: func(_ context.Context, key string) ([]byte, bool, error) {
			keys = append(keys, key)
			return nil, false, nil
		},
		set: func(context.Context, string, []byte, time.Duration) error { return nil },
	}
	routeCache, _ := newTestRouteDayCache(fake)
	queries := 0
	svc := newTestServiceWithCache(t, routeDayDB(&queries), routeCache, searchTestNow)
	reverse := validSearchRequest()
	reverse.Origin, reverse.Destination = "DPS", "CGK"
	later := validSearchRequest()
	later.Date = time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)

	for _, request := range []SearchFlightsRequest{validSearchRequest(), reverse, later} {
		_, err := svc.SearchFlights(t.Context(), request)
		require.NoError(t, err)
	}

	require.Equal(t, []string{
		"catalog:flights:CGK:DPS:2026-11-20",
		"catalog:flights:DPS:CGK:2026-11-20",
		"catalog:flights:CGK:DPS:2026-11-22",
	}, keys, "the date is the searched date, not today's")
}

func TestServiceSearchFlights_fallsBackToTheDatabaseWhenTheCacheFails(t *testing.T) {
	queries := 0
	fake := &fakeSearchCache{
		t:   t,
		get: func(context.Context, string) ([]byte, bool, error) { return nil, false, errBoom },
	}
	routeCache, _ := newTestRouteDayCache(fake)
	svc := newTestServiceWithCache(t, routeDayDB(&queries), routeCache, searchTestNow)

	response, err := svc.SearchFlights(t.Context(), validSearchRequest())

	require.NoError(t, err)
	require.Equal(t, []string{"cheap", "dear"}, responseIDs(response))
	require.Equal(t, 1, queries)
}

func TestServiceSearchFlights_doesNotTouchTheCacheForAnUnknownAirport(t *testing.T) {
	routeCache, _ := newTestRouteDayCache(&fakeSearchCache{t: t}) // any call fails the test
	svc := newTestServiceWithCache(t, &fakeStore{}, routeCache, searchTestNow)
	request := validSearchRequest()
	request.Origin = "ZZZ"

	response, err := svc.SearchFlights(t.Context(), request)

	require.NoError(t, err)
	require.Empty(t, response.Data)
}

func TestServiceSearchFlights_doesNotCacheADatabaseFailure(t *testing.T) {
	fake := &fakeSearchCache{
		t:   t,
		get: func(context.Context, string) ([]byte, bool, error) { return nil, false, nil },
		// set is nil: nothing may be stored when the query failed.
	}
	routeCache, _ := newTestRouteDayCache(fake)
	db := &fakeStore{
		listFlightsByRouteDay: func(context.Context, store.ListFlightsByRouteDayParams) ([]store.Flight, error) {
			return nil, errBoom
		},
	}
	svc := newTestServiceWithCache(t, db, routeCache, searchTestNow)

	_, err := svc.SearchFlights(t.Context(), validSearchRequest())

	require.ErrorIs(t, err, errBoom)
}

// recordDeletes returns a cache that only accepts deletes and remembers the keys.
func recordDeletes(t *testing.T, deleted *[]string) *RouteDayCache {
	t.Helper()
	routeCache, _ := newTestRouteDayCache(&fakeSearchCache{
		t: t,
		delete: func(_ context.Context, keys ...string) error {
			*deleted = append(*deleted, keys...)
			return nil
		},
	})
	return routeCache
}

func TestServiceCreateFlight_forgetsTheCachedRouteDayOfTheNewFlight(t *testing.T) {
	var deleted []string
	db := &fakeStore{
		createFlight:      func(context.Context, store.CreateFlightParams) (store.Flight, error) { return sampleFlight(), nil },
		insertOutboxEvent: func(context.Context, store.InsertOutboxEventParams) error { return nil },
	}
	svc := newTestServiceWithCache(t, db, recordDeletes(t, &deleted), searchTestNow)

	_, err := svc.CreateFlight(t.Context(), validCreateRequest())

	require.NoError(t, err)
	require.Equal(t, []string{"catalog:flights:CGK:DPS:2026-11-20"}, deleted)
}

func TestServiceCreateFlight_forgetsTheLocalDayOfAFlightThatLeavesAfterLocalMidnight(t *testing.T) {
	var deleted []string
	db := &fakeStore{
		createFlight: func(context.Context, store.CreateFlightParams) (store.Flight, error) {
			flight := sampleFlight()
			flight.DepartureAt = time.Date(2026, 11, 21, 1, 0, 0, 0, wib).UTC() // still the 20th in UTC
			flight.ArrivalAt = time.Date(2026, 11, 21, 3, 55, 0, 0, wita).UTC()
			return flight, nil
		},
		insertOutboxEvent: func(context.Context, store.InsertOutboxEventParams) error { return nil },
	}
	svc := newTestServiceWithCache(t, db, recordDeletes(t, &deleted), searchTestNow)

	_, err := svc.CreateFlight(t.Context(), validCreateRequest())

	require.NoError(t, err)
	require.Equal(t, []string{"catalog:flights:CGK:DPS:2026-11-21"}, deleted, "the date is the origin's local date")
}

func TestServiceCreateFlight_keepsTheCacheWhenTheWriteFails(t *testing.T) {
	routeCache, _ := newTestRouteDayCache(&fakeSearchCache{t: t}) // a delete would fail the test
	db := &fakeStore{
		createFlight: func(context.Context, store.CreateFlightParams) (store.Flight, error) {
			return store.Flight{}, errBoom
		},
	}
	svc := newTestServiceWithCache(t, db, routeCache, searchTestNow)

	_, err := svc.CreateFlight(t.Context(), validCreateRequest())

	require.ErrorIs(t, err, errBoom)
}

func TestServiceCreateFlight_succeedsEvenWhenTheCacheCannotBeInvalidated(t *testing.T) {
	routeCache, _ := newTestRouteDayCache(&fakeSearchCache{
		t:      t,
		delete: func(context.Context, ...string) error { return errBoom },
	})
	db := &fakeStore{
		createFlight:      func(context.Context, store.CreateFlightParams) (store.Flight, error) { return sampleFlight(), nil },
		insertOutboxEvent: func(context.Context, store.InsertOutboxEventParams) error { return nil },
	}
	svc := newTestServiceWithCache(t, db, routeCache, searchTestNow)

	_, err := svc.CreateFlight(t.Context(), validCreateRequest())

	require.NoError(t, err, "the write is committed; the entry just expires after the TTL")
}

func TestServiceUpdateFlight_forgetsTheCachedRouteDay(t *testing.T) {
	var (
		updated store.UpdateFlightParams
		event   store.InsertOutboxEventParams
		deleted []string
	)
	svc := newTestServiceWithCache(t, updateDB(&updated, &event), recordDeletes(t, &deleted), searchTestNow)

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{Price: ptr(int64(900_000))})

	require.NoError(t, err)
	require.Equal(t, []string{"catalog:flights:CGK:DPS:2026-11-20"}, deleted, "one key, not the same day twice")
}

func TestServiceUpdateFlight_forgetsBothDaysWhenTheFlightMovesToAnotherDay(t *testing.T) {
	var (
		updated store.UpdateFlightParams
		event   store.InsertOutboxEventParams
		deleted []string
	)
	svc := newTestServiceWithCache(t, updateDB(&updated, &event), recordDeletes(t, &deleted), searchTestNow)
	departure := time.Date(2026, 11, 21, 7, 0, 0, 0, wib)
	arrival := time.Date(2026, 11, 21, 9, 55, 0, 0, wita)

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{DepartureAt: &departure, ArrivalAt: &arrival})

	require.NoError(t, err)
	require.Equal(t, []string{"catalog:flights:CGK:DPS:2026-11-20", "catalog:flights:CGK:DPS:2026-11-21"}, deleted)
}

func TestServiceUpdateFlight_keepsTheCacheWhenTheUpdateIsRejected(t *testing.T) {
	routeCache, _ := newTestRouteDayCache(&fakeSearchCache{t: t}) // a delete would fail the test
	db := &fakeStore{
		getFlight: func(context.Context, string) (store.Flight, error) { return sampleFlight(), nil },
	}
	svc := newTestServiceWithSeats(t, db, routeCache, bookingHolds(5, 140), searchTestNow)

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, adminAuthorization, UpdateFlightRequest{Capacity: ptr(int32(10))})

	require.ErrorIs(t, err, ErrCapacityBelowSold)
}
