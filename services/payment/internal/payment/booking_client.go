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

// GetBill reads the bill of a flight or hotel booking with the paying user's own access token.
func (c *BookingClient) GetBill(ctx context.Context, authorization, id string) (Bill, error) {
	// The host comes from BOOKING_URL; the caller's id only reaches the path, escaped.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/internal/v1/bills/"+url.PathEscape(id), nil) //nolint:gosec // G704: fixed host
	if err != nil {
		return Bill{}, fmt.Errorf("build booking request: %w", err)
	}
	req.Header.Set("Authorization", authorization)
	if requestID := httpserver.RequestIDFrom(ctx); requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}

	resp, err := c.http.Do(req) //nolint:gosec // G704: fixed host
	if err != nil {
		return Bill{}, fmt.Errorf("%w: %w", ErrBookingUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return Bill{}, ErrBookingNotFound
	default:
		return Bill{}, fmt.Errorf("%w: booking answered %d", ErrBookingUnavailable, resp.StatusCode)
	}

	var bill Bill
	if err := json.NewDecoder(resp.Body).Decode(&bill); err != nil {
		return Bill{}, fmt.Errorf("%w: decode bill: %w", ErrBookingUnavailable, err)
	}
	return bill, nil
}
