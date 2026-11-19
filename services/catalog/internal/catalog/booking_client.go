package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
)

const bookingTimeout = 2 * time.Second

// BookedSeats are the seats booking holds and has sold on a flight.
type BookedSeats struct {
	HeldSeats int32 `json:"held_seats"`
	SoldSeats int32 `json:"sold_seats"`
}

type BookingClient struct {
	baseURL string
	http    *http.Client
}

func NewBookingClient(baseURL string) *BookingClient {
	return &BookingClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		http:    &http.Client{Timeout: bookingTimeout},
	}
}

// FlightSeats asks booking for a flight's seats with the admin's own access token.
func (c *BookingClient) FlightSeats(ctx context.Context, authorization, flightID string) (seats BookedSeats, found bool, err error) {
	// The host comes from BOOKING_URL; the flight id only reaches the path, escaped.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/v1/flights/"+url.PathEscape(flightID)+"/seats", nil) //nolint:gosec // G704: fixed host
	if err != nil {
		return BookedSeats{}, false, fmt.Errorf("build booking request: %w", err)
	}
	req.Header.Set("Authorization", authorization)
	if requestID := httpserver.RequestIDFrom(ctx); requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}

	resp, err := c.http.Do(req) //nolint:gosec // G704: fixed host
	if err != nil {
		return BookedSeats{}, false, fmt.Errorf("%w: %w", ErrBookingUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return BookedSeats{}, false, nil
	default:
		return BookedSeats{}, false, fmt.Errorf("%w: booking answered %d", ErrBookingUnavailable, resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(&seats); err != nil {
		return BookedSeats{}, false, fmt.Errorf("%w: decode flight seats: %w", ErrBookingUnavailable, err)
	}

	return seats, true, nil
}
