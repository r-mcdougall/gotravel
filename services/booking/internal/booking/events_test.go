package booking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/database"
	"github.com/r-mcdougall/gotravel/internal/platform/dbtest"
	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
	"github.com/r-mcdougall/gotravel/services/booking/migrations"
)

const testFlightID = "0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a11"

var (
	databaseOnce      sync.Once
	databaseURLValue  string
	databaseErr       error
	terminateDatabase func()
)

func TestMain(m *testing.M) {
	code := m.Run()
	if terminateDatabase != nil {
		terminateDatabase()
	}
	os.Exit(code)
}

// recordingStore runs the handler's transaction against recordingQueries; any other call fails the test.
type recordingStore struct {
	unusedStore
	q *recordingQueries
}

func (s recordingStore) InTx(_ context.Context, fn func(queries) error) error {
	return fn(s.q)
}

type recordingQueries struct {
	unusedStore
	upsert func(arg store.UpsertFlightInventoryParams) (store.FlightInventory, error)
	events []store.InsertOutboxEventParams
}

func (q *recordingQueries) UpsertFlightInventory(_ context.Context, arg store.UpsertFlightInventoryParams) (store.FlightInventory, error) {
	return q.upsert(arg)
}

func (q *recordingQueries) InsertOutboxEvent(_ context.Context, arg store.InsertOutboxEventParams) error {
	q.events = append(q.events, arg)
	return nil
}

// flightChangedDelivery builds the message the catalog publishes for one version of the test flight.
func flightChangedDelivery(version int32, price int64, capacity int32) messaging.Delivery {
	body := fmt.Sprintf(`{
		"event_id": "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e",
		"type": "flight.changed",
		"occurred_at": "2026-11-16T10:00:00Z",
		"data": {
			"flight_id": %q, "version": %d,
			"flight_number": "GA410", "airline_code": "GA",
			"origin_code": "CGK", "destination_code": "DPS",
			"departure_at": "2027-03-15T06:00:00+07:00",
			"arrival_at": "2027-03-15T08:55:00+08:00",
			"price": %d, "capacity": %d
		}
	}`, testFlightID, version, price, capacity)

	return messaging.Delivery{ID: "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e", Type: eventFlightChanged, Body: []byte(body)}
}

func TestEventHandlerHandleFlightChanged_upsertsTheFlightAndReportsItsAvailability(t *testing.T) {
	var got store.UpsertFlightInventoryParams
	q := &recordingQueries{unusedStore: unusedStore{t}, upsert: func(arg store.UpsertFlightInventoryParams) (store.FlightInventory, error) {
		got = arg
		return store.FlightInventory{FlightID: arg.FlightID, Capacity: arg.Capacity, HeldSeats: 4, AvailabilityVersion: 7}, nil
	}}
	handler := NewEventHandler(recordingStore{unusedStore: unusedStore{t}, q: q})

	require.NoError(t, handler.HandleFlightChanged(t.Context(), flightChangedDelivery(2, 1_250_000, 180)))

	require.Equal(t, store.UpsertFlightInventoryParams{
		FlightID:        testFlightID,
		Version:         2,
		FlightNumber:    "GA410",
		AirlineCode:     "GA",
		OriginCode:      "CGK",
		DestinationCode: "DPS",
		DepartureAt:     time.Date(2027, 3, 14, 23, 0, 0, 0, time.UTC),
		ArrivalAt:       time.Date(2027, 3, 15, 0, 55, 0, 0, time.UTC),
		Price:           1_250_000,
		Capacity:        180,
	}, got)
	require.Len(t, q.events, 1)
	require.Equal(t, "flight.availability_changed", q.events[0].EventType)
	require.JSONEq(t,
		fmt.Sprintf(`{"flight_id":%q,"availability_version":7,"available_seats":176}`, testFlightID),
		string(q.events[0].Payload))
}

func TestEventHandlerHandleFlightChanged_reportsNothingForAnIgnoredVersion(t *testing.T) {
	q := &recordingQueries{unusedStore: unusedStore{t}, upsert: func(store.UpsertFlightInventoryParams) (store.FlightInventory, error) {
		return store.FlightInventory{}, pgx.ErrNoRows
	}}
	handler := NewEventHandler(recordingStore{unusedStore: unusedStore{t}, q: q})

	require.NoError(t, handler.HandleFlightChanged(t.Context(), flightChangedDelivery(1, 1_250_000, 180)))
	require.Empty(t, q.events)
}

func TestEventHandlerHandleFlightChanged_rejectsAMessageThatIsNotJSON(t *testing.T) {
	handler := NewEventHandler(unusedStore{t})

	err := handler.HandleFlightChanged(t.Context(), messaging.Delivery{ID: "m1", Body: []byte("not json")})

	require.ErrorIs(t, err, messaging.ErrPermanent)
}

func TestEventHandlerHandleFlightChanged_rejectsAnEventWithoutAValidFlightID(t *testing.T) {
	for name, body := range map[string]string{
		"missing flight_id": `{"data": {"version": 1}}`,
		"malformed uuid":    `{"data": {"flight_id": "not-a-uuid", "version": 1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			handler := NewEventHandler(unusedStore{t})

			err := handler.HandleFlightChanged(t.Context(), messaging.Delivery{ID: "m1", Body: []byte(body)})

			require.ErrorIs(t, err, messaging.ErrPermanent)
		})
	}
}

func TestEventHandlerHandleFlightChanged_treatsStoreFailuresAsTemporary(t *testing.T) {
	q := &recordingQueries{unusedStore: unusedStore{t}, upsert: func(store.UpsertFlightInventoryParams) (store.FlightInventory, error) {
		return store.FlightInventory{}, errors.New("database unavailable")
	}}
	handler := NewEventHandler(recordingStore{unusedStore: unusedStore{t}, q: q})

	err := handler.HandleFlightChanged(t.Context(), flightChangedDelivery(1, 1_250_000, 180))

	require.ErrorContains(t, err, "database unavailable")
	require.NotErrorIs(t, err, messaging.ErrPermanent)
}

func TestEventHandlerHandleFlightChanged_ignoresRepeatedAndOlderVersions(t *testing.T) {
	pool := newTestPool(t)
	queries := store.New(pool)
	handler := NewEventHandler(NewPgStore(pool))
	apply := func(version int32, price int64, capacity int32) store.FlightInventory {
		t.Helper()
		require.NoError(t, handler.HandleFlightChanged(t.Context(), flightChangedDelivery(version, price, capacity)))
		inventory, err := queries.GetFlightInventory(t.Context(), testFlightID)
		require.NoError(t, err)
		return inventory
	}

	inventory := apply(2, 900_000, 180)
	require.Equal(t, int32(2), inventory.Version)
	require.Equal(t, "GA410", inventory.FlightNumber)
	require.True(t, time.Date(2027, 3, 14, 23, 0, 0, 0, time.UTC).Equal(inventory.DepartureAt))

	inventory = apply(1, 1_250_000, 200)
	require.Equal(t, int32(2), inventory.Version, "an older version arriving late is ignored")
	require.Equal(t, int64(900_000), inventory.Price)

	inventory = apply(2, 1, 1)
	require.Equal(t, int64(900_000), inventory.Price, "a repeated version is ignored")
	require.Equal(t, int32(180), inventory.Capacity)

	inventory = apply(3, 800_000, 150)
	require.Equal(t, int32(3), inventory.Version)
	require.Equal(t, int64(800_000), inventory.Price)
	require.Equal(t, int32(150), inventory.Capacity)

	require.Equal(t, int64(2), inventory.AvailabilityVersion, "only the two applied versions moved availability")
	requireOutbox(t, pool,
		fmt.Sprintf(`{"flight_id":%q,"availability_version":1,"available_seats":180}`, testFlightID),
		fmt.Sprintf(`{"flight_id":%q,"availability_version":2,"available_seats":150}`, testFlightID))
}

func TestEventHandlerHandleFlightChanged_neverTouchesTheSeatCounters(t *testing.T) {
	pool := newTestPool(t)
	queries := store.New(pool)
	handler := NewEventHandler(NewPgStore(pool))

	require.NoError(t, handler.HandleFlightChanged(t.Context(), flightChangedDelivery(1, 1_250_000, 180)))
	_, err := pool.Exec(t.Context(),
		`UPDATE flight_inventories SET held_seats = 5, sold_seats = 7, availability_version = 3 WHERE flight_id = $1`, testFlightID)
	require.NoError(t, err)

	require.NoError(t, handler.HandleFlightChanged(t.Context(), flightChangedDelivery(2, 1_250_000, 150)))

	inventory, err := queries.GetFlightInventory(t.Context(), testFlightID)
	require.NoError(t, err)
	require.Equal(t, int32(150), inventory.Capacity)
	require.Equal(t, int32(5), inventory.HeldSeats)
	require.Equal(t, int32(7), inventory.SoldSeats)
	require.Equal(t, int64(4), inventory.AvailabilityVersion, "a capacity change moves availability on")
	require.JSONEq(t,
		fmt.Sprintf(`{"flight_id":%q,"availability_version":4,"available_seats":138}`, testFlightID),
		latestEvent(t, pool, testFlightID))
}

func databaseURL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	databaseOnce.Do(func() {
		databaseURLValue, terminateDatabase, databaseErr = dbtest.Start(context.Background())
		if databaseErr == nil {
			databaseErr = database.Migrate(context.Background(), slog.New(slog.DiscardHandler), databaseURLValue, migrations.FS)
		}
	})
	require.NoError(t, databaseErr)
	return databaseURLValue
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := database.Connect(context.Background(), databaseURL(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, err = pool.Exec(t.Context(), "TRUNCATE passengers, bookings, outbox, flight_inventories, hotel_bookings, room_nights, room_types")
	require.NoError(t, err)
	return pool
}

// roomTypeCreatedDelivery builds the message the catalog publishes for the test room type.
func roomTypeCreatedDelivery(totalRooms int32, pricePerNight int64) messaging.Delivery {
	body := fmt.Sprintf(`{
		"event_id": "9b2d4f61-3a7c-4e15-8d2b-6c0e1f3a5b7d",
		"type": "room_type.created",
		"occurred_at": "2026-11-23T10:00:00Z",
		"data": {
			"room_type_id": %q, "hotel_id": %q,
			"hotel_name": "Seminyak Garden Hotel", "city_code": "DPS",
			"name": "Deluxe King", "max_guests": 2,
			"total_rooms": %d, "price_per_night": %d
		}
	}`, testRoomTypeID, testHotelID, totalRooms, pricePerNight)

	return messaging.Delivery{ID: "9b2d4f61-3a7c-4e15-8d2b-6c0e1f3a5b7d", Type: eventRoomTypeCreated, Body: []byte(body)}
}

func TestEventHandlerHandleRoomTypeCreated_storesTheRoomTypeOnce(t *testing.T) {
	pool := newTestPool(t)
	handler := NewEventHandler(NewPgStore(pool))

	require.NoError(t, handler.HandleRoomTypeCreated(t.Context(), roomTypeCreatedDelivery(12, 1_500_000)))
	require.NoError(t, handler.HandleRoomTypeCreated(t.Context(), roomTypeCreatedDelivery(1, 1)), "a repeated message is acked")

	roomType, err := store.New(pool).GetRoomType(t.Context(), testRoomTypeID)
	require.NoError(t, err)
	roomType.CreatedAt = time.Time{}
	require.Equal(t, store.RoomType{
		RoomTypeID:    testRoomTypeID,
		HotelID:       testHotelID,
		HotelName:     "Seminyak Garden Hotel",
		CityCode:      "DPS",
		Name:          "Deluxe King",
		MaxGuests:     2,
		TotalRooms:    12,
		PricePerNight: 1_500_000,
	}, roomType, "the first message wins")
	require.Equal(t, 1, count(t, pool, "room_types"))
}

func TestEventHandlerHandleRoomTypeCreated_rejectsAMessageThatIsNotJSON(t *testing.T) {
	handler := NewEventHandler(unusedStore{t})

	err := handler.HandleRoomTypeCreated(t.Context(), messaging.Delivery{ID: "m1", Body: []byte("not json")})

	require.ErrorIs(t, err, messaging.ErrPermanent)
}

func TestEventHandlerHandleRoomTypeCreated_rejectsARoomTypeBookingCannotHold(t *testing.T) {
	for _, tc := range []struct{ field, value string }{
		{"room_type_id", `"not-a-uuid"`},
		{"hotel_id", `""`},
		{"hotel_name", `""`},
		{"city_code", `""`},
		{"name", `""`},
		{"max_guests", `0`},
		{"max_guests", `11`},
		{"total_rooms", `0`},
		{"price_per_night", `0`},
	} {
		field, value := tc.field, tc.value
		t.Run(field+"="+value, func(t *testing.T) {
			var event, data map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(roomTypeCreatedDelivery(12, 1_500_000).Body, &event))
			require.NoError(t, json.Unmarshal(event["data"], &data))
			data[field] = json.RawMessage(value)
			var err error
			event["data"], err = json.Marshal(data)
			require.NoError(t, err)
			body, err := json.Marshal(event)
			require.NoError(t, err)
			handler := NewEventHandler(unusedStore{t})

			err = handler.HandleRoomTypeCreated(t.Context(), messaging.Delivery{ID: "m1", Body: body})

			require.ErrorIs(t, err, messaging.ErrPermanent)
			require.ErrorContains(t, err, "invalid "+field)
		})
	}
}

func TestEventHandlerHandleRoomTypeCreated_acceptsTheLimitsOfARoomType(t *testing.T) {
	var got store.InsertRoomTypeParams
	handler := NewEventHandler(roomTypeRecorder{unusedStore: unusedStore{t}, insert: func(arg store.InsertRoomTypeParams) error {
		got = arg
		return nil
	}})

	body := strings.Replace(string(roomTypeCreatedDelivery(1, 1).Body), `"max_guests": 2`, `"max_guests": 10`, 1)
	require.NoError(t, handler.HandleRoomTypeCreated(t.Context(), messaging.Delivery{ID: "m1", Body: []byte(body)}))

	require.Equal(t, int32(10), got.MaxGuests)
	require.Equal(t, int32(1), got.TotalRooms)
	require.Equal(t, int64(1), got.PricePerNight)
}

func TestEventHandlerHandleRoomTypeCreated_treatsStoreFailuresAsTemporary(t *testing.T) {
	handler := NewEventHandler(roomTypeRecorder{unusedStore: unusedStore{t}, insert: func(store.InsertRoomTypeParams) error {
		return errors.New("database unavailable")
	}})

	err := handler.HandleRoomTypeCreated(t.Context(), roomTypeCreatedDelivery(12, 1_500_000))

	require.ErrorContains(t, err, "database unavailable")
	require.NotErrorIs(t, err, messaging.ErrPermanent)
}

type roomTypeRecorder struct {
	unusedStore
	insert func(arg store.InsertRoomTypeParams) error
}

func (s roomTypeRecorder) InsertRoomType(_ context.Context, arg store.InsertRoomTypeParams) error {
	return s.insert(arg)
}
