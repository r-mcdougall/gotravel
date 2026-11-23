package booking

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

const testHotelID = "7c9e6679-7425-40de-944b-e07fc1f90ae7"

func TestServiceHoldRoom_rejectsAnInvalidRequestBeforeTouchingTheDatabase(t *testing.T) {
	now := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)
	svc := newService(unusedStore{t}, 15*time.Minute, func() time.Time { return now })

	for _, tc := range []struct {
		name      string
		change    func(r *HotelHoldRequest)
		wantField string
	}{
		{"malformed room type id", func(r *HotelHoldRequest) { r.RoomTypeID = "deluxe-king" }, "room_type_id"},
		{"check-in in the past", func(r *HotelHoldRequest) { r.CheckIn, r.CheckOut = "2026-11-30", "2026-12-02" }, "check_in"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := validHotelHold()
			tc.change(&request)

			_, err := svc.HoldRoom(t.Context(), johnID, request)

			var validationErr *ValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Equal(t, tc.wantField, validationErr.Details[0].Field)
		})
	}
}

func TestServiceHotelBookings_treatAMalformedIDAsNotFound(t *testing.T) {
	svc := newService(unusedStore{t}, 15*time.Minute, time.Now)

	_, err := svc.GetHotelBooking(t.Context(), johnID, "not-a-uuid")
	require.ErrorIs(t, err, ErrNotFound)

	_, err = svc.CancelHotelBooking(t.Context(), johnID, "not-a-uuid")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestServiceHoldRoom_holdsOneRoomOnEveryNightOfTheStay(t *testing.T) {
	svc, pool, now := newTestService(t)
	addRoomType(t, pool, 5, 2)

	held, err := svc.HoldRoom(t.Context(), johnID, HotelHoldRequest{
		RoomTypeID: testRoomTypeID,
		CheckIn:    day(now, 7),
		CheckOut:   day(now, 10),
		Guests:     1,
		GuestName:  "  John Doe ",
	})
	require.NoError(t, err)

	require.Equal(t, "HELD", held.Status)
	require.Equal(t, HotelSummary{ID: testHotelID, Name: "Seminyak Garden Hotel", CityCode: "DPS"}, held.Hotel)
	require.Equal(t, RoomTypeSummary{ID: testRoomTypeID, Name: "Deluxe King"}, held.RoomType)
	require.Equal(t, day(now, 7), held.CheckIn)
	require.Equal(t, day(now, 10), held.CheckOut)
	require.Equal(t, int64(3), held.Nights)
	require.Equal(t, int32(1), held.Guests)
	require.Equal(t, "John Doe", held.GuestName)
	require.Equal(t, int64(1_500_000), held.PricePerNight)
	require.Equal(t, int64(4_500_000), held.TotalPrice)
	require.True(t, now.Add(15*time.Minute).Equal(held.ExpiresAt))
	require.Nil(t, held.PaymentID)

	require.Equal(t, map[string]int32{day(now, 7): 1, day(now, 8): 1, day(now, 9): 1}, heldRooms(t, pool),
		"the check-out night is not held")
	require.Equal(t, 3, count(t, pool, "room_nights WHERE total_rooms = 5"), "each night copies the room count")

	fetched, err := svc.GetHotelBooking(t.Context(), johnID, held.ID)
	require.NoError(t, err)
	require.Equal(t, held, fetched)
}

func TestServiceHoldRoom_refusesAStayTheRoomTypeCannotTake(t *testing.T) {
	svc, pool, now := newTestService(t)
	addRoomType(t, pool, 1, 2)
	_, err := svc.HoldRoom(t.Context(), janeID, hotelHold(now, 8, 9))
	require.NoError(t, err)
	_, err = pool.Exec(t.Context(), `UPDATE room_nights SET held_rooms = 0, sold_rooms = 1`)
	require.NoError(t, err)

	_, err = svc.HoldRoom(t.Context(), johnID, HotelHoldRequest{
		RoomTypeID: "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e", CheckIn: day(now, 7), CheckOut: day(now, 8), Guests: 1, GuestName: "John Doe",
	})
	require.ErrorIs(t, err, ErrRoomTypeNotFound)

	tooMany := hotelHold(now, 7, 8)
	tooMany.Guests = 3
	_, err = svc.HoldRoom(t.Context(), johnID, tooMany)
	var validationErr *ValidationError
	require.ErrorAs(t, err, &validationErr)
	require.Equal(t, "guests", validationErr.Details[0].Field)
	require.Equal(t, "must be at most 2 for this room type", validationErr.Details[0].Message)

	_, err = svc.HoldRoom(t.Context(), johnID, hotelHold(now, 7, 10))
	require.ErrorIs(t, err, ErrRoomsUnavailable, "the middle night is sold")

	require.Equal(t, map[string]int32{day(now, 8): 0}, heldRooms(t, pool), "a refused hold leaves no night behind")
	require.Equal(t, 1, count(t, pool, "hotel_bookings"))

	_, err = svc.HoldRoom(t.Context(), johnID, hotelHold(now, 9, 10))
	require.NoError(t, err, "a stay can start on the check-out date of another")
	_, err = svc.HoldRoom(t.Context(), johnID, hotelHold(now, 7, 8))
	require.NoError(t, err, "a stay can end on the check-in date of another")
}

func TestServiceHoldRoom_neverHoldsMoreRoomsThanANightHas(t *testing.T) {
	const rooms, guests = 3, 24

	svc, pool, now := newTestService(t)
	addRoomType(t, pool, rooms, 2)

	// Every stay starts between night 7 and night 10 and lasts five nights, so all of them share nights 10 and 11.
	errs := make([]error, guests)
	var wg sync.WaitGroup
	for i := range guests {
		start := 7 + i%4
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = svc.HoldRoom(context.Background(), johnID, hotelHold(now, start, start+5))
		}()
	}
	wg.Wait()

	succeeded := 0
	for _, err := range errs {
		if err == nil {
			succeeded++
			continue
		}
		require.ErrorIs(t, err, ErrRoomsUnavailable)
	}
	require.Equal(t, rooms, succeeded)

	var mismatched int
	require.NoError(t, pool.QueryRow(t.Context(), `
		SELECT count(*) FROM room_nights n
		WHERE n.held_rooms <> (
			SELECT count(*) FROM hotel_bookings b
			WHERE b.status = 'HELD' AND b.check_in <= n.night AND n.night < b.check_out)`).Scan(&mismatched))
	require.Zero(t, mismatched, "every night holds exactly the rooms of the held bookings that cover it")
	require.Equal(t, int32(rooms), heldRooms(t, pool)[day(now, 10)])
}

func TestServiceGetHotelBooking_hidesBookingsOfOtherUsers(t *testing.T) {
	svc, pool, now := newTestService(t)
	addRoomType(t, pool, 5, 2)
	held, err := svc.HoldRoom(t.Context(), johnID, hotelHold(now, 7, 8))
	require.NoError(t, err)

	_, err = svc.GetHotelBooking(t.Context(), janeID, held.ID)
	require.ErrorIs(t, err, ErrNotFound)

	_, err = svc.GetHotelBooking(t.Context(), johnID, "6a1c0e55-1c7e-4c63-9a52-0b1d6b0f2d3e")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestServiceListHotelBookings_pagesThroughTheUsersBookingsNewestFirst(t *testing.T) {
	svc, pool, now := newTestService(t)
	addRoomType(t, pool, 5, 2)

	var johns []string
	for start := 7; start <= 9; start++ {
		held, err := svc.HoldRoom(t.Context(), johnID, hotelHold(now, start, start+1))
		require.NoError(t, err)
		johns = append(johns, held.ID)
	}
	_, err := svc.HoldRoom(t.Context(), janeID, hotelHold(now, 7, 8))
	require.NoError(t, err)

	first, err := svc.ListHotelBookings(t.Context(), johnID, ListRequest{Page: 1, PageSize: 2})
	require.NoError(t, err)
	require.Equal(t, PageMeta{Page: 1, PageSize: 2, Total: 3}, first.Meta)
	require.Len(t, first.Data, 2)
	require.Equal(t, johns[2], first.Data[0].ID)
	require.Equal(t, johns[1], first.Data[1].ID)
	require.Equal(t, "Seminyak Garden Hotel", first.Data[1].Hotel.Name)

	second, err := svc.ListHotelBookings(t.Context(), johnID, ListRequest{Page: 2, PageSize: 2})
	require.NoError(t, err)
	require.Len(t, second.Data, 1)
	require.Equal(t, johns[0], second.Data[0].ID)

	empty, err := svc.ListHotelBookings(t.Context(), johnID, ListRequest{Page: 3, PageSize: 2})
	require.NoError(t, err)
	require.NotNil(t, empty.Data, "an empty page is an empty list, not null")
	require.Empty(t, empty.Data)

	_, err = svc.ListHotelBookings(t.Context(), johnID, ListRequest{Page: 0, PageSize: 2})
	var validationErr *ValidationError
	require.ErrorAs(t, err, &validationErr)
}

func TestServiceCancelHotelBooking_releasesEveryNightOfAHeldBooking(t *testing.T) {
	svc, pool, now := newTestService(t)
	addRoomType(t, pool, 5, 2)
	held, err := svc.HoldRoom(t.Context(), johnID, hotelHold(now, 7, 9))
	require.NoError(t, err)
	_, err = svc.HoldRoom(t.Context(), janeID, hotelHold(now, 8, 10))
	require.NoError(t, err)

	_, err = svc.CancelHotelBooking(t.Context(), janeID, held.ID)
	require.ErrorIs(t, err, ErrNotFound, "another user's booking looks missing")

	cancelled, err := svc.CancelHotelBooking(t.Context(), johnID, held.ID)
	require.NoError(t, err)
	require.Equal(t, "CANCELLED", cancelled.Status)
	require.Equal(t, "Deluxe King", cancelled.RoomType.Name)
	require.Equal(t, int64(3_000_000), cancelled.TotalPrice)

	want := map[string]int32{day(now, 7): 0, day(now, 8): 1, day(now, 9): 1}
	require.Equal(t, want, heldRooms(t, pool), "only John's nights are released")

	_, err = svc.CancelHotelBooking(t.Context(), johnID, held.ID)
	require.ErrorIs(t, err, ErrNotHeld)
	require.Equal(t, want, heldRooms(t, pool), "a second cancel releases nothing")
}

// day is the date offset days after now, in the format of a hold request.
func day(now time.Time, offset int) string {
	return now.UTC().AddDate(0, 0, offset).Format(time.DateOnly)
}

func hotelHold(now time.Time, checkIn, checkOut int) HotelHoldRequest {
	return HotelHoldRequest{
		RoomTypeID: testRoomTypeID,
		CheckIn:    day(now, checkIn),
		CheckOut:   day(now, checkOut),
		Guests:     2,
		GuestName:  "John Doe",
	}
}

func addRoomType(t *testing.T, pool *pgxpool.Pool, totalRooms, maxGuests int32) {
	t.Helper()

	err := store.New(pool).InsertRoomType(t.Context(), store.InsertRoomTypeParams{
		RoomTypeID:    testRoomTypeID,
		HotelID:       testHotelID,
		HotelName:     "Seminyak Garden Hotel",
		CityCode:      "DPS",
		Name:          "Deluxe King",
		MaxGuests:     maxGuests,
		TotalRooms:    totalRooms,
		PricePerNight: 1_500_000,
	})
	require.NoError(t, err)
}

// heldRooms returns the held rooms of the test room type per night.
func heldRooms(t *testing.T, pool *pgxpool.Pool) map[string]int32 {
	t.Helper()

	rows, err := pool.Query(t.Context(), `SELECT night, held_rooms FROM room_nights WHERE room_type_id = $1`, testRoomTypeID)
	require.NoError(t, err)
	defer rows.Close()

	held := map[string]int32{}
	for rows.Next() {
		var night time.Time
		var rooms int32
		require.NoError(t, rows.Scan(&night, &rooms))
		held[night.Format(time.DateOnly)] = rooms
	}
	require.NoError(t, rows.Err())
	return held
}
