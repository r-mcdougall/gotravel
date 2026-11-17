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
