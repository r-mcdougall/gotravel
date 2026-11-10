package catalog

import (
	"context"
)

type Service interface {
	CreateFlight(ctx context.Context, request CreateFlightRequest) (AdminFlightResponse, error)
	UpdateFlight(ctx context.Context, id string, request UpdateFlightRequest) (AdminFlightResponse, error)
	GetFlight(ctx context.Context, id string) (AdminFlightResponse, error)
	ListFlights(ctx context.Context, request ListFlightsRequest) (AdminFlightListResponse, error)
}
