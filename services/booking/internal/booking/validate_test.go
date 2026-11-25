package booking

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestValidUUID(t *testing.T) {
	require.True(t, validUUID(testFlightID))
	require.True(t, validUUID("0B6F3F0E-6C0C-4C46-8F0B-3D6F1F0C9A11"))
	require.False(t, validUUID(""))
	require.False(t, validUUID("not-a-uuid"))
	require.False(t, validUUID("0b6f3f0e6c0c4c468f0b3d6f1f0c9a11"))
	require.False(t, validUUID("0b6f3f0e-6c0c-4c46-8f0b-3d6f1f0c9a1g"))
}

func TestHoldRequestValidate(t *testing.T) {
	valid := func() HoldRequest {
		return HoldRequest{FlightID: testFlightID, Passengers: twoPassengers()}
	}
	tomorrow := time.Now().UTC().AddDate(0, 0, 1).Format(time.DateOnly)

	tests := []struct {
		name      string
		change    func(r *HoldRequest)
		wantField string
	}{
		{"malformed flight id", func(r *HoldRequest) { r.FlightID = "GA410" }, "flight_id"},
		{"no passengers", func(r *HoldRequest) { r.Passengers = nil }, "passengers"},
		{"more passengers than a booking allows", func(r *HoldRequest) {
			r.Passengers = slices.Repeat(r.Passengers[:1], maxPassengers+1)
		}, "passengers"},
		{"empty name", func(r *HoldRequest) { r.Passengers[1].FullName = "" }, "passengers[1].full_name"},
		{"name too long", func(r *HoldRequest) { r.Passengers[0].FullName = strings.Repeat("a", maxFullNameLen+1) }, "passengers[0].full_name"},
		{"birth date in another format", func(r *HoldRequest) { r.Passengers[0].BirthDate = "17-05-1990" }, "passengers[0].birth_date"},
		{"birth date in the future", func(r *HoldRequest) { r.Passengers[0].BirthDate = tomorrow }, "passengers[0].birth_date"},
	}

	request := valid()
	require.NoError(t, request.Validate())
	maxParty := valid()
	maxParty.Passengers = slices.Repeat(maxParty.Passengers[:1], maxPassengers)
	require.NoError(t, maxParty.Validate(), "a full party of %d is allowed", maxPassengers)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := valid()
			tc.change(&request)

			var validationErr *ValidationError
			require.ErrorAs(t, request.Validate(), &validationErr)
			require.Len(t, validationErr.Details, 1)
			require.Equal(t, tc.wantField, validationErr.Details[0].Field)
		})
	}
}

func TestListBookingsRequestValidate(t *testing.T) {
	require.NoError(t, ListRequest{Page: 1, PageSize: maxPageSize}.Validate())

	var validationErr *ValidationError
	require.ErrorAs(t, ListRequest{Page: 0, PageSize: maxPageSize + 1}.Validate(), &validationErr)
	require.Len(t, validationErr.Details, 2)
}

const testRoomTypeID = "5d0c7a3e-2b1f-4e8a-9c6d-7f3e1a2b4c5d"

func validHotelHold() HotelHoldRequest {
	return HotelHoldRequest{
		RoomTypeID: testRoomTypeID,
		CheckIn:    "2026-12-01",
		CheckOut:   "2026-12-03",
		Guests:     2,
		GuestName:  "John Doe",
	}
}

func TestHotelHoldRequestValidate(t *testing.T) {
	tests := []struct {
		name      string
		change    func(r *HotelHoldRequest)
		wantField string
	}{
		{"malformed room type id", func(r *HotelHoldRequest) { r.RoomTypeID = "deluxe-king" }, "room_type_id"},
		{"check-in in another format", func(r *HotelHoldRequest) { r.CheckIn = "01-12-2026" }, "check_in"},
		{"check-in that is not a calendar date", func(r *HotelHoldRequest) { r.CheckIn = "2026-11-31" }, "check_in"},
		{"missing check-out", func(r *HotelHoldRequest) { r.CheckOut = "" }, "check_out"},
		{"check-out on the check-in date", func(r *HotelHoldRequest) { r.CheckOut = r.CheckIn }, "check_out"},
		{"check-out before check-in", func(r *HotelHoldRequest) { r.CheckOut = "2026-11-30" }, "check_out"},
		{"stay longer than allowed", func(r *HotelHoldRequest) { r.CheckOut = "2026-12-16" }, "check_out"},
		{"no guests", func(r *HotelHoldRequest) { r.Guests = 0 }, "guests"},
		{"more guests than a room allows", func(r *HotelHoldRequest) { r.Guests = maxGuests + 1 }, "guests"},
		{"empty guest name", func(r *HotelHoldRequest) { r.GuestName = "" }, "guest_name"},
		{"guest name too long", func(r *HotelHoldRequest) { r.GuestName = strings.Repeat("é", maxFullNameLen+1) }, "guest_name"},
	}

	request := validHotelHold()
	require.NoError(t, request.Validate())

	limits := validHotelHold()
	limits.CheckIn, limits.CheckOut = "2026-12-31", "2027-01-14"
	limits.Guests = maxGuests
	limits.GuestName = strings.Repeat("é", maxFullNameLen)
	require.NoError(t, limits.Validate(), "a stay of %d nights for %d guests is allowed", maxNights, maxGuests)

	single := validHotelHold()
	single.CheckOut = "2026-12-02"
	single.Guests = 1
	single.GuestName = "J"
	require.NoError(t, single.Validate(), "one night for one guest is allowed")

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := validHotelHold()
			tc.change(&request)

			var validationErr *ValidationError
			require.ErrorAs(t, request.Validate(), &validationErr)
			require.Len(t, validationErr.Details, 1)
			require.Equal(t, tc.wantField, validationErr.Details[0].Field)
		})
	}
}

func TestHotelHoldRequestValidateReportsEveryField(t *testing.T) {
	request := HotelHoldRequest{RoomTypeID: "x", CheckIn: "tomorrow", Guests: -1}

	var validationErr *ValidationError
	require.ErrorAs(t, request.Validate(), &validationErr)

	fields := make([]string, 0, len(validationErr.Details))
	for _, detail := range validationErr.Details {
		fields = append(fields, detail.Field)
	}
	require.Equal(t, []string{"room_type_id", "check_in", "check_out", "guests", "guest_name"}, fields)
}

func TestHotelHoldRequestNormalize(t *testing.T) {
	request := HotelHoldRequest{
		RoomTypeID: " " + testRoomTypeID + "\n",
		CheckIn:    " 2026-12-01",
		CheckOut:   "2026-12-03 ",
		Guests:     2,
		GuestName:  "  John Doe\t",
	}
	request.normalize()

	require.Equal(t, validHotelHold(), request)
	require.NoError(t, request.Validate())

	blank := HotelHoldRequest{RoomTypeID: testRoomTypeID, CheckIn: "2026-12-01", CheckOut: "2026-12-02", Guests: 1, GuestName: "   "}
	blank.normalize()
	require.Error(t, blank.Validate(), "a name of only spaces is empty")
}

func TestHotelHoldRequestStay(t *testing.T) {
	request := validHotelHold()
	checkIn, checkOut := request.stay()

	require.Equal(t, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), checkIn)
	require.Equal(t, time.Date(2026, 12, 3, 0, 0, 0, 0, time.UTC), checkOut)
	require.Equal(t, int64(2), nights(checkIn, checkOut))
	require.Equal(t, int64(14), nights(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC), time.Date(2027, 1, 14, 0, 0, 0, 0, time.UTC)))
}

func TestCheckCheckIn(t *testing.T) {
	wib := time.FixedZone("WIB", 7*60*60)
	now := time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)
	today := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)

	require.NoError(t, checkCheckIn(today, now), "today")
	require.NoError(t, checkCheckIn(today.AddDate(0, 0, 1), now), "tomorrow")

	var validationErr *ValidationError
	require.ErrorAs(t, checkCheckIn(today.AddDate(0, 0, -1), now), &validationErr, "yesterday")
	require.Equal(t, "check_in", validationErr.Details[0].Field)

	// 02:00 on 2 Dec in WIB is still 1 Dec in UTC, so 1 Dec is not yet in the past.
	earlyWIB := time.Date(2026, 12, 2, 2, 0, 0, 0, wib)
	require.NoError(t, checkCheckIn(today, earlyWIB))
	require.Error(t, checkCheckIn(today, earlyWIB.Add(5*time.Hour)), "07:00 WIB is 2 Dec in UTC")
}
