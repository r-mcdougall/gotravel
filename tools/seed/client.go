package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	loginPath        = "/api/v1/auth/login"
	createFlightPath = "/api/v1/admin/flights"
	createHotelPath  = "/api/v1/admin/hotels"

	// maxBodyBytes caps how much of a response is read, every body the API sends is far smaller.
	maxBodyBytes = 1 << 20

	// maxErrorTextBytes caps the part of an unexpected body put in an error.
	maxErrorTextBytes = 200
)

// outcome is what happened to a flight the seed tried to create.
type outcome int

const (
	outcomeCreated  outcome = iota + 1 // the API stored a new flight
	outcomeExisting                    // the flight was already there
)

// client talks to the platform through the gateway as an admin.
type client struct {
	baseURL string
	http    *http.Client
	token   string
}

func newClient(baseURL string, httpClient *http.Client) *client {
	return &client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

func (c *client) login(ctx context.Context, email, password string) error {
	status, body, err := c.post(ctx, loginPath, map[string]string{"email": email, "password": password})
	if err != nil {
		return fmt.Errorf("log in as %s: %w", email, err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("log in as %s: %s", email, describe(status, body))
	}

	var response struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &response); err != nil || response.AccessToken == "" {
		return fmt.Errorf("log in as %s: the response has no access token", email)
	}

	c.token = response.AccessToken
	return nil
}

// createFlight creates one flight. A flight that already exists is not an error, so the seed can run again.
func (c *client) createFlight(ctx context.Context, spec flightSpec) (outcome, error) {
	status, body, err := c.post(ctx, createFlightPath, spec)
	if err != nil {
		return 0, fmt.Errorf("create flight %s: %w", flightLabel(spec), err)
	}

	switch {
	case status == http.StatusCreated:
		return outcomeCreated, nil
	case status == http.StatusConflict && parseAPIError(body).Code == "FLIGHT_EXISTS":
		return outcomeExisting, nil
	default:
		return 0, fmt.Errorf("create flight %s: %s", flightLabel(spec), describe(status, body))
	}
}

// createHotel creates one hotel with its room types. A hotel that already exists is not an error, so the seed can run again.
func (c *client) createHotel(ctx context.Context, spec hotelSpec) (outcome, error) {
	status, body, err := c.post(ctx, createHotelPath, spec)
	if err != nil {
		return 0, fmt.Errorf("create hotel %s: %w", hotelLabel(spec), err)
	}

	switch {
	case status == http.StatusCreated:
		return outcomeCreated, nil
	case status == http.StatusConflict && parseAPIError(body).Code == "HOTEL_EXISTS":
		return outcomeExisting, nil
	default:
		return 0, fmt.Errorf("create hotel %s: %s", hotelLabel(spec), describe(status, body))
	}
}

// post sends a JSON body and returns the status and the (capped) response body.
func (c *client) post(ctx context.Context, path string, payload any) (int, []byte, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, fmt.Errorf("encode request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		request.Header.Set("Authorization", "Bearer "+c.token)
	}

	response, err := c.http.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxBodyBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("read response: %w", err)
	}
	return response.StatusCode, body, nil
}

func flightLabel(spec flightSpec) string {
	return fmt.Sprintf("%s departing %s", spec.FlightNumber, spec.DepartureAt.Format(time.RFC3339))
}

func hotelLabel(spec hotelSpec) string {
	return fmt.Sprintf("%q in %s", spec.Name, spec.CityCode)
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details []struct {
		Field   string `json:"field"`
		Message string `json:"message"`
	} `json:"details"`
}

// parseAPIError returns the zero value when the body is not an API error.
func parseAPIError(body []byte) apiError {
	var envelope struct {
		Error apiError `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return apiError{}
	}
	return envelope.Error
}

// describe turns an unexpected response into one line for an error message.
func describe(status int, body []byte) string {
	apiErr := parseAPIError(body)
	if apiErr.Code == "" {
		text := strings.TrimSpace(string(body))
		if len(text) > maxErrorTextBytes {
			text = text[:maxErrorTextBytes] + "..."
		}
		if text == "" {
			return fmt.Sprintf("HTTP %d with no body", status)
		}
		return fmt.Sprintf("HTTP %d: %s", status, text)
	}

	var text strings.Builder
	fmt.Fprintf(&text, "HTTP %d %s: %s", status, apiErr.Code, apiErr.Message)
	for _, detail := range apiErr.Details {
		fmt.Fprintf(&text, "; %s: %s", detail.Field, detail.Message)
	}
	return text.String()
}
