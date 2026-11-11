package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

const sampleFlightID = "0b5c2a2e-7b53-4a3b-9d0e-2f6d4c1c9a11"

type fakeStore struct {
	t                  *testing.T
	createFlight       func(context.Context, store.CreateFlightParams) (store.Flight, error)
	getFlight          func(context.Context, string) (store.Flight, error)
	getFlightForUpdate func(context.Context, string) (store.Flight, error)
	updateFlight       func(context.Context, store.UpdateFlightParams) (store.Flight, error)
	listFlights        func(context.Context, store.ListFlightsParams) ([]store.Flight, error)
	countFlights       func(context.Context, store.CountFlightsParams) (int64, error)
	insertOutboxEvent  func(context.Context, store.InsertOutboxEventParams) error
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

func newTestService(t *testing.T, db *fakeStore) Service {
	t.Helper()
	db.t = t

	reference, err := LoadReference(t.Context(), sampleReferenceQueries(t))
	require.NoError(t, err)

	return NewService(db, reference)
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

	response, err := svc.UpdateFlight(t.Context(), sampleFlightID, UpdateFlightRequest{Price: ptr(int64(900_000))})
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

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, UpdateFlightRequest{Price: ptr(int64(900_000))})
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

	response, err := svc.UpdateFlight(t.Context(), sampleFlightID, UpdateFlightRequest{DepartureAt: &departure, ArrivalAt: &arrival})
	require.NoError(t, err)

	require.True(t, departure.Equal(updated.DepartureAt))
	require.True(t, arrival.Equal(updated.ArrivalAt))
	require.Equal(t, "2026-11-21T07:00:00+07:00", response.DepartureAt.Format(time.RFC3339))
}

func TestServiceUpdateFlight_movesAvailableSeatsWithCapacity(t *testing.T) {
	tests := []struct {
		name      string
		capacity  int32
		available int32
	}{
		{"capacity grows", 200, 62},
		{"capacity shrinks but stays above the sold seats", 150, 12},
		{"capacity drops to exactly the sold seats", 138, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				updated store.UpdateFlightParams
				event   store.InsertOutboxEventParams
			)
			svc := newTestService(t, updateDB(&updated, &event))

			response, err := svc.UpdateFlight(t.Context(), sampleFlightID, UpdateFlightRequest{Capacity: ptr(tc.capacity)})
			require.NoError(t, err)

			require.Equal(t, tc.capacity, updated.Capacity)
			require.Equal(t, tc.available, updated.AvailableSeats)
			require.Equal(t, tc.available, response.AvailableSeats)
		})
	}
}

func TestServiceUpdateFlight_rejectsCapacityBelowTheSoldSeats(t *testing.T) {
	db := &fakeStore{
		getFlightForUpdate: func(context.Context, string) (store.Flight, error) { return sampleFlight(), nil },
	}
	svc := newTestService(t, db)

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, UpdateFlightRequest{Capacity: ptr(int32(137))})

	require.ErrorIs(t, err, ErrCapacityBelowSold)
}

func TestServiceUpdateFlight_reportsAMissingFlight(t *testing.T) {
	db := &fakeStore{
		getFlightForUpdate: func(context.Context, string) (store.Flight, error) { return store.Flight{}, pgx.ErrNoRows },
	}
	svc := newTestService(t, db)

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, UpdateFlightRequest{Price: ptr(int64(900_000))})

	require.ErrorIs(t, err, ErrFlightNotFound)
}

func TestServiceUpdateFlight_treatsAMalformedIDAsMissing(t *testing.T) {
	svc := newTestService(t, &fakeStore{})

	_, err := svc.UpdateFlight(t.Context(), "not-a-uuid", UpdateFlightRequest{Price: ptr(int64(900_000))})

	require.ErrorIs(t, err, ErrFlightNotFound)
}

func TestServiceUpdateFlight_rejectsAnInvalidRequestWithoutTouchingTheStore(t *testing.T) {
	svc := newTestService(t, &fakeStore{})

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, UpdateFlightRequest{})

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

	_, err := svc.UpdateFlight(t.Context(), sampleFlightID, UpdateFlightRequest{DepartureAt: &departure, ArrivalAt: &arrival})

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
