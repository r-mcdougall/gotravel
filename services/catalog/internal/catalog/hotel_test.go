package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

const sampleHotelID = "6f1c2a9e-3b7d-4c51-9a0e-2d8f4b6c1e73"

func sampleHotel() store.Hotel {
	return store.Hotel{
		ID:         sampleHotelID,
		Name:       "Seminyak Garden Hotel",
		CityCode:   "DPS",
		Address:    "Jl. Raya Seminyak 12, Badung",
		StarRating: 4,
		CreatedAt:  time.Date(2026, 11, 23, 11, 5, 0, 0, time.UTC),
	}
}

func sampleRoomType() store.RoomType {
	return store.RoomType{
		ID:            "0b8e5d21-7c4a-4f19-8e36-5a2d9c1f7b40",
		HotelID:       sampleHotelID,
		Name:          "Deluxe Double",
		MaxGuests:     2,
		TotalRooms:    20,
		PricePerNight: 850_000,
		CreatedAt:     time.Date(2026, 11, 23, 11, 5, 0, 0, time.UTC),
	}
}

// newTestHotelService returns the concrete service, whose hotel methods are not on the Service interface yet.
func newTestHotelService(t *testing.T, db *fakeStore) *service {
	t.Helper()
	db.t = t

	reference, err := LoadReference(t.Context(), sampleReferenceQueries(t))
	require.NoError(t, err)

	return newService(db, reference, nil, &fakeSeats{t: t}, time.Now)
}

// hotelDB stores what it is given the way PostgreSQL would and logs every call in order.
func hotelDB(calls *[]string, hotel *store.CreateHotelParams, rooms *[]store.CreateRoomTypeParams, events *[]store.InsertOutboxEventParams) *fakeStore {
	return &fakeStore{
		createHotel: func(_ context.Context, arg store.CreateHotelParams) (store.Hotel, error) {
			*calls = append(*calls, "hotel")
			*hotel = arg
			created := sampleHotel()
			created.Name, created.CityCode, created.Address, created.StarRating = arg.Name, arg.CityCode, arg.Address, arg.StarRating
			return created, nil
		},
		createRoomType: func(_ context.Context, arg store.CreateRoomTypeParams) (store.RoomType, error) {
			*calls = append(*calls, "room "+arg.Name)
			*rooms = append(*rooms, arg)
			return store.RoomType{
				ID:            fmt.Sprintf("00000000-0000-4000-8000-%012d", len(*rooms)),
				HotelID:       arg.HotelID,
				Name:          arg.Name,
				MaxGuests:     arg.MaxGuests,
				TotalRooms:    arg.TotalRooms,
				PricePerNight: arg.PricePerNight,
			}, nil
		},
		insertOutboxEvent: func(_ context.Context, arg store.InsertOutboxEventParams) error {
			*calls = append(*calls, "event")
			*events = append(*events, arg)
			return nil
		},
	}
}

func roomTypeEvent(t *testing.T, event store.InsertOutboxEventParams) roomTypeCreatedData {
	t.Helper()
	require.Equal(t, eventRoomTypeCreated, event.EventType)

	var data roomTypeCreatedData
	require.NoError(t, json.Unmarshal(event.Payload, &data))
	return data
}

func TestServiceCreateHotel_storesTheHotelThenEachRoomTypeWithItsEvent(t *testing.T) {
	var (
		calls  []string
		hotel  store.CreateHotelParams
		rooms  []store.CreateRoomTypeParams
		events []store.InsertOutboxEventParams
	)
	svc := newTestHotelService(t, hotelDB(&calls, &hotel, &rooms, &events))
	request := validCreateHotelRequest()
	request.Name = "  Seminyak Garden Hotel "
	request.RoomTypes[0].Name = " Deluxe Double"

	response, err := svc.CreateHotel(t.Context(), request)
	require.NoError(t, err)

	require.Equal(t, []string{"hotel", "room Deluxe Double", "event", "room Family Suite", "event"}, calls)
	require.Equal(t, store.CreateHotelParams{
		Name: "Seminyak Garden Hotel", CityCode: "DPS", Address: "Jl. Raya Seminyak 12, Badung", StarRating: 4,
	}, hotel, "texts are trimmed before they are stored")
	require.Equal(t, []store.CreateRoomTypeParams{
		{HotelID: sampleHotelID, Name: "Deluxe Double", MaxGuests: 2, TotalRooms: 20, PricePerNight: 850_000},
		{HotelID: sampleHotelID, Name: "Family Suite", MaxGuests: 4, TotalRooms: 5, PricePerNight: 1_600_000},
	}, rooms)

	require.Len(t, events, 2)
	require.Equal(t, roomTypeCreatedData{
		RoomTypeID: "00000000-0000-4000-8000-000000000002", HotelID: sampleHotelID, HotelName: "Seminyak Garden Hotel",
		CityCode: "DPS", Name: "Family Suite", MaxGuests: 4, TotalRooms: 5, PricePerNight: 1_600_000,
	}, roomTypeEvent(t, events[1]))

	require.Equal(t, AdminHotelResponse{
		ID:         sampleHotelID,
		Name:       "Seminyak Garden Hotel",
		City:       CityResponse{Code: "DPS", Name: "Denpasar"},
		Address:    "Jl. Raya Seminyak 12, Badung",
		StarRating: 4,
		RoomTypes: []AdminRoomTypeResponse{
			{ID: "00000000-0000-4000-8000-000000000001", Name: "Deluxe Double", MaxGuests: 2, TotalRooms: 20, PricePerNight: 850_000},
			{ID: "00000000-0000-4000-8000-000000000002", Name: "Family Suite", MaxGuests: 4, TotalRooms: 5, PricePerNight: 1_600_000},
		},
		CreatedAt: sampleHotel().CreatedAt,
	}, response)
}

func TestServiceCreateHotel_rejectsAnInvalidRequestWithoutTouchingTheStore(t *testing.T) {
	svc := newTestHotelService(t, &fakeStore{})

	_, err := svc.CreateHotel(t.Context(), CreateHotelRequest{})

	require.Equal(t, []string{"name", "city_code", "address", "star_rating", "room_types"}, invalidFields(t, err))
}

func TestServiceCreateHotel_rejectsTextThatIsOnlyWhitespace(t *testing.T) {
	svc := newTestHotelService(t, &fakeStore{})
	request := validCreateHotelRequest()
	request.Name = "   "
	request.RoomTypes[1].Name = "\t"

	_, err := svc.CreateHotel(t.Context(), request)

	require.Equal(t, []string{"name", "room_types[1].name"}, invalidFields(t, err))
}

func TestServiceCreateHotel_rejectsAnUnknownCityWithoutTouchingTheStore(t *testing.T) {
	svc := newTestHotelService(t, &fakeStore{})
	request := validCreateHotelRequest()
	request.CityCode = "TYO" // a real city, but not in the sample reference data

	_, err := svc.CreateHotel(t.Context(), request)

	require.Equal(t, []string{"city_code"}, invalidFields(t, err))
}

func TestServiceCreateHotel_reportsAHotelThatAlreadyExists(t *testing.T) {
	db := &fakeStore{
		createHotel: func(context.Context, store.CreateHotelParams) (store.Hotel, error) {
			return store.Hotel{}, uniqueViolation(hotelNameKey)
		},
	}
	svc := newTestHotelService(t, db)

	_, err := svc.CreateHotel(t.Context(), validCreateHotelRequest())

	require.ErrorIs(t, err, ErrHotelExists)
}

func TestServiceCreateHotel_doesNotMistakeOtherDatabaseErrorsForADuplicate(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"unique violation of another constraint", uniqueViolation("hotels_pkey")},
		{"unrelated failure", errBoom},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := &fakeStore{
				createHotel: func(context.Context, store.CreateHotelParams) (store.Hotel, error) {
					return store.Hotel{}, tc.err
				},
			}
			svc := newTestHotelService(t, db)

			_, err := svc.CreateHotel(t.Context(), validCreateHotelRequest())

			require.ErrorIs(t, err, tc.err)
			require.False(t, errors.Is(err, ErrHotelExists))
		})
	}
}

func TestServiceCreateHotel_failsWhenARoomTypeOrItsEventCannotBeStored(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(db *fakeStore)
	}{
		{"room type insert fails", func(db *fakeStore) {
			db.createRoomType = func(context.Context, store.CreateRoomTypeParams) (store.RoomType, error) {
				return store.RoomType{}, errBoom
			}
		}},
		{"outbox insert fails", func(db *fakeStore) {
			db.insertOutboxEvent = func(context.Context, store.InsertOutboxEventParams) error { return errBoom }
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var (
				calls  []string
				hotel  store.CreateHotelParams
				rooms  []store.CreateRoomTypeParams
				events []store.InsertOutboxEventParams
			)
			db := hotelDB(&calls, &hotel, &rooms, &events)
			tc.mutate(db)
			svc := newTestHotelService(t, db)

			_, err := svc.CreateHotel(t.Context(), validCreateHotelRequest())

			require.ErrorIs(t, err, errBoom)
		})
	}
}

// hotelService is the integration environment's service with its hotel methods.
func (e *env) hotelService() *service {
	return newService(e.pgStore, e.reference, nil, e.seats, time.Now)
}

type hotelRowCounts struct {
	Hotels, RoomTypes, Outbox int
}

func (e *env) hotelRowCounts(t *testing.T) hotelRowCounts {
	t.Helper()
	var counts hotelRowCounts
	err := e.pool.QueryRow(t.Context(),
		"SELECT (SELECT count(*) FROM hotels), (SELECT count(*) FROM room_types), (SELECT count(*) FROM outbox)",
	).Scan(&counts.Hotels, &counts.RoomTypes, &counts.Outbox)
	require.NoError(t, err)
	return counts
}

// roomTypeEvents returns the payload of every outbox row. One transaction writes them with the same created_at, so their order is not defined.
func (e *env) roomTypeEvents(t *testing.T) []roomTypeCreatedData {
	t.Helper()

	rows, err := e.pool.Query(t.Context(), "SELECT event_type, payload FROM outbox")
	require.NoError(t, err)
	defer rows.Close()

	var events []roomTypeCreatedData
	for rows.Next() {
		var event store.InsertOutboxEventParams
		require.NoError(t, rows.Scan(&event.EventType, &event.Payload))
		events = append(events, roomTypeEvent(t, event))
	}
	require.NoError(t, rows.Err())
	return events
}

func TestHotelsInPostgres_createStoresTheHotelItsRoomTypesAndOneEventPerRoomType(t *testing.T) {
	e := newEnv(t)

	created, err := e.hotelService().CreateHotel(t.Context(), validCreateHotelRequest())
	require.NoError(t, err)

	require.True(t, validUUID(created.ID))
	require.Equal(t, CityResponse{Code: "DPS", Name: "Denpasar"}, created.City)
	require.Len(t, created.RoomTypes, 2)
	require.Equal(t, hotelRowCounts{Hotels: 1, RoomTypes: 2, Outbox: 2}, e.hotelRowCounts(t))

	want := make([]roomTypeCreatedData, len(created.RoomTypes))
	for i, room := range created.RoomTypes {
		require.True(t, validUUID(room.ID))
		want[i] = roomTypeCreatedData{
			RoomTypeID: room.ID, HotelID: created.ID, HotelName: "Seminyak Garden Hotel", CityCode: "DPS",
			Name: room.Name, MaxGuests: room.MaxGuests, TotalRooms: room.TotalRooms, PricePerNight: room.PricePerNight,
		}
	}
	require.ElementsMatch(t, want, e.roomTypeEvents(t))
}

func TestHotelsInPostgres_createRejectsADuplicateInTheSameCityWithoutWritingAnything(t *testing.T) {
	e := newEnv(t)
	svc := e.hotelService()
	_, err := svc.CreateHotel(t.Context(), validCreateHotelRequest())
	require.NoError(t, err)

	_, err = svc.CreateHotel(t.Context(), validCreateHotelRequest())

	require.ErrorIs(t, err, ErrHotelExists)
	require.Equal(t, hotelRowCounts{Hotels: 1, RoomTypes: 2, Outbox: 2}, e.hotelRowCounts(t))

	elsewhere := validCreateHotelRequest()
	elsewhere.CityCode = "JKT"
	_, err = svc.CreateHotel(t.Context(), elsewhere)
	require.NoError(t, err, "the same name is allowed in another city")
}

func TestHotelsInPostgres_createRollsBackTheHotelAndItsRoomTypesWhenTheOutboxInsertFails(t *testing.T) {
	e := newEnv(t)
	svc := newService(failingOutboxStore{PgStore: e.pgStore}, e.reference, nil, e.seats, time.Now)

	_, err := svc.CreateHotel(t.Context(), validCreateHotelRequest())

	require.ErrorIs(t, err, errBoom)
	require.Equal(t, hotelRowCounts{}, e.hotelRowCounts(t))
}
