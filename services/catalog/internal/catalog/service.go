package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

const (
	uniqueViolationCode = "23505"
	flightScheduleKey   = "flights_departure_key"
)

type queries interface {
	CreateFlight(ctx context.Context, arg store.CreateFlightParams) (store.Flight, error)
	GetFlight(ctx context.Context, id string) (store.Flight, error)
	GetFlightForUpdate(ctx context.Context, id string) (store.Flight, error)
	UpdateFlight(ctx context.Context, arg store.UpdateFlightParams) (store.Flight, error)
	ListFlights(ctx context.Context, arg store.ListFlightsParams) ([]store.Flight, error)
	CountFlights(ctx context.Context, arg store.CountFlightsParams) (int64, error)
	InsertOutboxEvent(ctx context.Context, arg store.InsertOutboxEventParams) error
}

var _ queries = (*store.Queries)(nil)

type flightStore interface {
	queries
	InTx(ctx context.Context, fn func(queries) error) error
}

type service struct {
	db        flightStore
	reference *Reference
}

func NewService(db flightStore, reference *Reference) Service {
	return &service{db: db, reference: reference}
}

func (s *service) CreateFlight(ctx context.Context, request CreateFlightRequest) (AdminFlightResponse, error) {
	if err := request.Validate(); err != nil {
		return AdminFlightResponse{}, fmt.Errorf("validate request: %w", err)
	}
	if err := s.checkReferences(request); err != nil {
		return AdminFlightResponse{}, fmt.Errorf("validate request: %w", err)
	}

	params := store.CreateFlightParams{
		FlightNumber:    request.FlightNumber,
		AirlineCode:     request.AirlineCode,
		OriginCode:      request.OriginCode,
		DestinationCode: request.DestinationCode,
		DepartureAt:     request.DepartureAt,
		ArrivalAt:       request.ArrivalAt,
		Price:           request.Price,
		Capacity:        request.Capacity,
		AvailableSeats:  request.Capacity,
	}

	var created store.Flight
	fn := func(q queries) error {
		flight, err := q.CreateFlight(ctx, params)
		if err != nil {
			return flightWriteError("create flight", err)
		}
		if err = recordFlightChanged(ctx, q, flight); err != nil {
			return err
		}

		created = flight
		return nil
	}

	if err := s.db.InTx(ctx, fn); err != nil {
		return AdminFlightResponse{}, err
	}

	return s.toAdminFlightResponse(created)
}

func (s *service) UpdateFlight(ctx context.Context, id string, request UpdateFlightRequest) (AdminFlightResponse, error) {
	if err := request.Validate(); err != nil {
		return AdminFlightResponse{}, fmt.Errorf("validate request: %w", err)
	}
	if !validUUID(id) {
		return AdminFlightResponse{}, ErrFlightNotFound
	}

	var updated store.Flight
	fn := func(q queries) error {
		current, err := q.GetFlightForUpdate(ctx, id)
		if err != nil {
			return ErrFlightNotFound
		}

		params, err := applyUpdate(current, request)
		if err != nil {
			return err
		}

		flight, err := q.UpdateFlight(ctx, params)
		if err != nil {
			return flightWriteError("update flight", err)
		}
		if err = recordFlightChanged(ctx, q, flight); err != nil {
			return err
		}

		updated = flight
		return nil
	}

	if err := s.db.InTx(ctx, fn); err != nil {
		return AdminFlightResponse{}, err
	}

	return s.toAdminFlightResponse(updated)
}

func (s *service) GetFlight(ctx context.Context, id string) (AdminFlightResponse, error) {
	if !validUUID(id) {
		return AdminFlightResponse{}, ErrFlightNotFound
	}

	flight, err := s.db.GetFlight(ctx, id)
	if err != nil {
		return AdminFlightResponse{}, flightQueryError("get flight", err)
	}

	return s.toAdminFlightResponse(flight)
}

func (s *service) ListFlights(ctx context.Context, request ListFlightsRequest) (AdminFlightListResponse, error) {
	if err := request.Validate(); err != nil {
		return AdminFlightListResponse{}, fmt.Errorf("validate request: %w", err)
	}

	origin := nilIfEmpty(request.Origin)
	destination := nilIfEmpty(request.Destination)

	listFlightsParams := store.ListFlightsParams{
		OriginCode:      origin,
		DestinationCode: destination,
		DepartureDate:   request.Date,
		RowOffset:       (request.Page - 1) * request.PageSize,
		RowLimit:        request.PageSize,
	}
	flights, err := s.db.ListFlights(ctx, listFlightsParams)
	if err != nil {
		return AdminFlightListResponse{}, fmt.Errorf("list flights: %w", err)
	}

	countFlightsParams := store.CountFlightsParams{
		OriginCode:      origin,
		DestinationCode: destination,
		DepartureDate:   request.Date,
	}
	total, err := s.db.CountFlights(ctx, countFlightsParams)
	if err != nil {
		return AdminFlightListResponse{}, fmt.Errorf("count flights: %w", err)
	}

	data := make([]AdminFlightResponse, 0, len(flights))
	for _, flight := range flights {
		item, err := s.toAdminFlightResponse(flight)
		if err != nil {
			return AdminFlightListResponse{}, err
		}
		data = append(data, item)
	}

	return AdminFlightListResponse{
		Data: data,
		Meta: PageMeta{Page: request.Page, PageSize: request.PageSize, Total: total},
	}, nil
}

func (s *service) checkReferences(request CreateFlightRequest) error {
	var v ValidationError

	_, knownAirline := s.reference.airlines[request.AirlineCode]
	_, knownOrigin := s.reference.airports[request.OriginCode]
	_, knownDestination := s.reference.airports[request.DestinationCode]

	v.check(knownAirline, "airline_code", "unknown airline")
	v.check(knownOrigin, "origin_code", "unknown airport")
	v.check(knownDestination, "destination_code", "unknown airport")

	return v.err()
}

func applyUpdate(current store.Flight, request UpdateFlightRequest) (store.UpdateFlightParams, error) {
	params := store.UpdateFlightParams{
		ID:             current.ID,
		DepartureAt:    current.DepartureAt,
		ArrivalAt:      current.ArrivalAt,
		Price:          current.Price,
		Capacity:       current.Capacity,
		AvailableSeats: current.AvailableSeats,
	}

	if request.DepartureAt != nil && request.ArrivalAt != nil {
		params.DepartureAt = *request.DepartureAt
		params.ArrivalAt = *request.ArrivalAt
	}
	if request.Price != nil {
		params.Price = *request.Price
	}
	if request.Capacity != nil {
		params.Capacity = *request.Capacity
		params.AvailableSeats = current.AvailableSeats + (*request.Capacity - current.Capacity)
		if params.AvailableSeats < 0 {
			return store.UpdateFlightParams{}, ErrCapacityBelowSold
		}
	}

	return params, nil
}

func recordFlightChanged(ctx context.Context, q queries, flight store.Flight) error {
	event, err := newFlightChangedEvent(flight)
	if err != nil {
		return err
	}
	if err = q.InsertOutboxEvent(ctx, event); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}

	return nil
}

func (s *service) toAdminFlightResponse(flight store.Flight) (AdminFlightResponse, error) {
	airline, ok := s.reference.Airline(flight.AirlineCode)
	if !ok {
		return AdminFlightResponse{}, fmt.Errorf("flight %s: unknown airline %q", flight.ID, flight.AirlineCode)
	}
	origin, ok := s.reference.Airport(flight.OriginCode)
	if !ok {
		return AdminFlightResponse{}, fmt.Errorf("flight %s: unknown airport %q", flight.ID, flight.OriginCode)
	}
	destination, ok := s.reference.Airport(flight.DestinationCode)
	if !ok {
		return AdminFlightResponse{}, fmt.Errorf("flight %s: unknown airport %q", flight.ID, flight.DestinationCode)
	}

	return AdminFlightResponse{
		ID:              flight.ID,
		FlightNumber:    flight.FlightNumber,
		Airline:         airline,
		Origin:          origin.AirportResponse,
		Destination:     destination.AirportResponse,
		DepartureAt:     flight.DepartureAt.In(origin.Location),
		ArrivalAt:       flight.ArrivalAt.In(destination.Location),
		DurationMinutes: int(flight.ArrivalAt.Sub(flight.DepartureAt) / time.Minute),
		Price:           flight.Price,
		Capacity:        flight.Capacity,
		AvailableSeats:  flight.AvailableSeats,
		CreatedAt:       flight.CreatedAt.UTC(),
		UpdatedAt:       flight.UpdatedAt.UTC(),
	}, nil
}

func flightQueryError(op string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrFlightNotFound
	}
	return fmt.Errorf("%s: %w", op, err)
}

func flightWriteError(op string, err error) error {
	if isScheduleCollision(err) {
		return ErrFlightExists
	}
	return fmt.Errorf("%s: %w", op, err)
}

func isScheduleCollision(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode && pgErr.ConstraintName == flightScheduleKey
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
