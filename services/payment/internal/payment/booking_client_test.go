package payment

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBookingClientGetBill_readsTheBillWithTheCallersToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/internal/v1/bills/"+testBookingID, r.URL.Path)
		require.Equal(t, "Bearer john-token", r.Header.Get("Authorization"))

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"` + testBookingID + `","status":"HELD","total_price":2500000,` +
			`"expires_at":"2026-11-19T10:15:00Z","unknown":true}`))
	}))
	t.Cleanup(server.Close)

	bill, err := NewBookingClient(server.URL+"/").GetBill(t.Context(), "Bearer john-token", testBookingID)

	require.NoError(t, err)
	require.Equal(t, Bill{
		ID:         testBookingID,
		Status:     "HELD",
		TotalPrice: 2_500_000,
		ExpiresAt:  time.Date(2026, 11, 19, 10, 15, 0, 0, time.UTC),
	}, bill, "fields payment does not know are ignored")
}

func TestBookingClientGetBill_reportsAMissingBooking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)

	_, err := NewBookingClient(server.URL).GetBill(t.Context(), "Bearer john-token", testBookingID)

	require.ErrorIs(t, err, ErrBookingNotFound)
}

func TestBookingClientGetBill_reportsBookingAsUnavailable(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "server error", status: http.StatusInternalServerError, body: `{}`},
		{name: "unexpected status", status: http.StatusUnauthorized, body: `{}`},
		{name: "malformed body", status: http.StatusOK, body: `{"id":`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			_, err := NewBookingClient(server.URL).GetBill(t.Context(), "Bearer john-token", testBookingID)

			require.ErrorIs(t, err, ErrBookingUnavailable)
		})
	}
}

func TestBookingClientGetBill_reportsAnUnreachableBookingAsUnavailable(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()

	_, err := NewBookingClient(server.URL).GetBill(t.Context(), "Bearer john-token", testBookingID)

	require.ErrorIs(t, err, ErrBookingUnavailable)
}
