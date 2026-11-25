package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

type referenceQueries interface {
	ListAirlines(ctx context.Context) ([]store.Airline, error)
	ListAirports(ctx context.Context) ([]store.Airport, error)
	ListCities(ctx context.Context) ([]store.City, error)
}

var _ referenceQueries = (*store.Queries)(nil)

type Airport struct {
	AirportResponse
	Location *time.Location
}

type Reference struct {
	airlines map[string]AirlineResponse
	airports map[string]Airport
	cities   map[string]CityResponse
}

func LoadReference(ctx context.Context, q referenceQueries) (*Reference, error) {
	airlines, err := q.ListAirlines(ctx)
	if err != nil {
		return nil, fmt.Errorf("list airlines: %w", err)
	}
	airports, err := q.ListAirports(ctx)
	if err != nil {
		return nil, fmt.Errorf("list airports: %w", err)
	}
	cities, err := q.ListCities(ctx)
	if err != nil {
		return nil, fmt.Errorf("list cities: %w", err)
	}

	reference := &Reference{
		airlines: make(map[string]AirlineResponse, len(airlines)),
		airports: make(map[string]Airport, len(airports)),
		cities:   make(map[string]CityResponse, len(cities)),
	}

	for _, airline := range airlines {
		reference.airlines[airline.Code] = AirlineResponse{Code: airline.Code, Name: airline.Name}
	}
	for _, airport := range airports {
		a, err := newAirport(airport)
		if err != nil {
			return nil, err
		}
		reference.airports[airport.Code] = a
	}
	for _, city := range cities {
		reference.cities[city.Code] = CityResponse{Code: city.Code, Name: city.Name}
	}

	return reference, nil
}

func (r *Reference) Airline(code string) (AirlineResponse, bool) {
	airline, ok := r.airlines[code]
	return airline, ok
}

func (r *Reference) Airport(code string) (Airport, bool) {
	airport, ok := r.airports[code]
	return airport, ok
}

func (r *Reference) City(code string) (CityResponse, bool) {
	city, ok := r.cities[code]
	return city, ok
}

func newAirport(airport store.Airport) (Airport, error) {
	location, err := time.LoadLocation(airport.Timezone)
	if err != nil {
		return Airport{}, fmt.Errorf("airport %s: load time zone %q: %w", airport.Code, airport.Timezone, err)
	}

	return Airport{
		AirportResponse: AirportResponse{Code: airport.Code, Name: airport.Name, City: airport.City},
		Location:        location,
	}, nil
}
