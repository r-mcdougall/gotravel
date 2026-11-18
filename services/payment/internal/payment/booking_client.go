package payment

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

func (c *BookingClient) GetBooking(ctx context.Context, authorization, id string) (Booking, error) {
	// The host comes from BOOKING_URL; the caller's id only reaches the path, escaped.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/v1/bookings/"+url.PathEscape(id), nil) //nolint:gosec // G704: fixed host
	if err != nil {
		return Booking{}, fmt.Errorf("build booking request: %w", err)
	}
	req.Header.Set("Authorization", authorization)
	if requestID := httpserver.RequestIDFrom(ctx); requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}

	resp, err := c.http.Do(req) //nolint:gosec // G704: fixed host
	if err != nil {
		return Booking{}, fmt.Errorf("%w: %w", ErrBookingUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return Booking{}, ErrBookingNotFound
	default:
		return Booking{}, fmt.Errorf("%w: booking answered %d", ErrBookingUnavailable, resp.StatusCode)
	}

	var booking Booking
	if err := json.NewDecoder(resp.Body).Decode(&booking); err != nil {
		return Booking{}, fmt.Errorf("%w: decode booking: %w", ErrBookingUnavailable, err)
	}
	return booking, nil
}
