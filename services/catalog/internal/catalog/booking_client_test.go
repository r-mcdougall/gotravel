package catalog

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBookingClientFlightSeats_readsTheSeatsWithTheAdminsToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/internal/v1/flights/"+sampleFlightID+"/seats", r.URL.Path)
		require.Equal(t, adminAuthorization, r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"flight_id":"` + sampleFlightID + `","capacity":180,"held_seats":5,"sold_seats":140}`))
	}))
	t.Cleanup(server.Close)

	seats, found, err := NewBookingClient(server.URL+"/").FlightSeats(t.Context(), adminAuthorization, sampleFlightID)

	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, BookedSeats{HeldSeats: 5, SoldSeats: 140}, seats)
}

func TestBookingClientFlightSeats_reportsAFlightBookingDoesNotKnow(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	_, found, err := NewBookingClient(server.URL).FlightSeats(t.Context(), adminAuthorization, sampleFlightID)

	require.NoError(t, err)
	require.False(t, found)
}

func TestBookingClientFlightSeats_reportsBookingAsUnavailable(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "server error", status: http.StatusInternalServerError, body: `{}`},
		{name: "token refused", status: http.StatusForbidden, body: `{}`},
		{name: "malformed body", status: http.StatusOK, body: `{"held_seats":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			_, _, err := NewBookingClient(server.URL).FlightSeats(t.Context(), adminAuthorization, sampleFlightID)

			require.ErrorIs(t, err, ErrBookingUnavailable)
		})
	}
}

func TestBookingClientFlightSeats_reportsAnUnreachableBookingAsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()

	_, _, err := NewBookingClient(server.URL).FlightSeats(t.Context(), adminAuthorization, sampleFlightID)

	require.ErrorIs(t, err, ErrBookingUnavailable)
}
