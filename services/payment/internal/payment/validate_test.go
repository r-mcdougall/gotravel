package payment

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const testBookingID = "5f0c6f1e-2b7a-4d3e-9c8b-7a6e5d4c3b21"

func TestPayRequestNormalize_trimsAndDefaultsToASucceededOutcome(t *testing.T) {
	request := PayRequest{BookingID: "  " + testBookingID + " ", Outcome: " "}

	request.normalize()

	require.Equal(t, PayRequest{BookingID: testBookingID, Outcome: "succeeded"}, request)
}

func TestPayRequestValidate(t *testing.T) {
	tests := []struct {
		name      string
		request   PayRequest
		wantField string
	}{
		{name: "succeeded outcome", request: PayRequest{BookingID: testBookingID, Outcome: "succeeded"}},
		{name: "failed outcome", request: PayRequest{BookingID: testBookingID, Outcome: "failed"}},
		{name: "booking id is not a uuid", request: PayRequest{BookingID: "abc", Outcome: "succeeded"}, wantField: "booking_id"},
		{name: "unknown outcome", request: PayRequest{BookingID: testBookingID, Outcome: "pending"}, wantField: "outcome"},
		{name: "outcome is case sensitive", request: PayRequest{BookingID: testBookingID, Outcome: "SUCCEEDED"}, wantField: "outcome"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()

			if tt.wantField == "" {
				require.NoError(t, err)
				return
			}
			var validationErr *ValidationError
			require.ErrorAs(t, err, &validationErr)
			require.Len(t, validationErr.Details, 1)
			require.Equal(t, tt.wantField, validationErr.Details[0].Field)
		})
	}
}
