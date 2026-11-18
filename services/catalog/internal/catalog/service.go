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
	ListFlightsByRouteDay(ctx context.Context, arg store.ListFlightsByRouteDayParams) ([]store.Flight, error)
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
	cache     *RouteDayCache
	now       func() time.Time
}

func NewService(db flightStore, reference *Reference, cache *RouteDayCache) Service {
	return newService(db, reference, cache, time.Now)
}

func newService(db flightStore, reference *Reference, cache *RouteDayCache, now func() time.Time) *service {
	return &service{
		db:        db,
		reference: reference,
		cache:     cache,
		now:       now,
	}
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
	s.invalidateRouteDays(ctx, created)

	return s.toAdminFlightResponse(created)
}

func (s *service) UpdateFlight(ctx context.Context, id string, request UpdateFlightRequest) (AdminFlightResponse, error) {
	if err := request.Validate(); err != nil {
		return AdminFlightResponse{}, fmt.Errorf("validate request: %w", err)
	}
	if !validUUID(id) {
		return AdminFlightResponse{}, ErrFlightNotFound
	}

	var before, updated store.Flight
	fn := func(q queries) error {
		current, err := q.GetFlightForUpdate(ctx, id)
		if err != nil {
			return flightQueryError("get flight for update", err)
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

		before, updated = current, flight
		return nil
	}

	if err := s.db.InTx(ctx, fn); err != nil {
		return AdminFlightResponse{}, err
	}
	// A new schedule can move the flight to another day, so both days go stale.
	s.invalidateRouteDays(ctx, before, updated)

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

	flights, err := s.db.ListFlights(ctx, store.ListFlightsParams{
		OriginCode:      origin,
		DestinationCode: destination,
		DepartureDate:   request.Date,
		RowOffset:       (request.Page - 1) * request.PageSize,
		RowLimit:        request.PageSize,
	})
	if err != nil {
		return AdminFlightListResponse{}, fmt.Errorf("list flights: %w", err)
	}

	total, err := s.db.CountFlights(ctx, store.CountFlightsParams{
		OriginCode:      origin,
		DestinationCode: destination,
		DepartureDate:   request.Date,
	})
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

func (s *service) GetFlightDetail(ctx context.Context, id string) (FlightResponse, error) {
	if !validUUID(id) {
		return FlightResponse{}, ErrFlightNotFound
	}

	flight, err := s.db.GetFlight(ctx, id)
	if err != nil {
		return FlightResponse{}, flightQueryError("get flight", err)
	}

	return s.toFlightResponse(flight)
}

// SearchFlights returns the bookable flights on one route and local departure day.
// An airport the catalog does not know has no flights, so it yields an empty page instead of an error and without touching the database.
func (s *service) SearchFlights(ctx context.Context, request SearchFlightsRequest) (FlightListResponse, error) {
	if err := request.Validate(); err != nil {
		return FlightListResponse{}, fmt.Errorf("validate request: %w", err)
	}

	origin, knownOrigin := s.reference.Airport(request.Origin)
	_, knownDestination := s.reference.Airport(request.Destination)
	if !knownOrigin || !knownDestination {
		return newFlightList(nil, request.Page, request.PageSize, 0), nil
	}

	flights, err := s.flightsOnRouteDay(ctx, request, origin)
	if err != nil {
		return FlightListResponse{}, err
	}

	page, total := searchFlights(flights, &request, origin.Location, s.now())

	data := make([]FlightResponse, 0, len(page))
	for _, flight := range page {
		item, err := s.toFlightResponse(flight)
		if err != nil {
			return FlightListResponse{}, err
		}
		data = append(data, item)
	}

	return newFlightList(data, request.Page, request.PageSize, int64(total)), nil
}

// flightsOnRouteDay loads every flight that departs on the requested date at the origin airport's local time.
func (s *service) flightsOnRouteDay(ctx context.Context, request SearchFlightsRequest, origin Airport) ([]store.Flight, error) {
	day := routeDay{
		Origin:      request.Origin,
		Destination: request.Destination,
		Date:        request.Date.Format(time.DateOnly),
	}

	return s.cache.load(ctx, day, func(ctx context.Context) ([]store.Flight, error) {
		year, month, dayOfMonth := request.Date.Date()
		dayStart := time.Date(year, month, dayOfMonth, 0, 0, 0, 0, origin.Location)
		dayEnd := time.Date(year, month, dayOfMonth+1, 0, 0, 0, 0, origin.Location)

		flights, err := s.db.ListFlightsByRouteDay(ctx, store.ListFlightsByRouteDayParams{
			OriginCode:      request.Origin,
			DestinationCode: request.Destination,
			DayStart:        dayStart,
			DayEnd:          dayEnd,
		})
		if err != nil {
			return nil, fmt.Errorf("list flights by route day: %w", err)
		}

		return flights, nil
	})
}

// invalidateRouteDays forgets the cached route days of the flights a committed write touched.
func (s *service) invalidateRouteDays(ctx context.Context, flights ...store.Flight) {
	s.cache.forget(ctx, s.reference, flights...)
}

func newFlightList(data []FlightResponse, page, pageSize int32, total int64) FlightListResponse {
	if data == nil {
		data = []FlightResponse{}
	}
	return FlightListResponse{
		Data: data,
		Meta: PageMeta{Page: page, PageSize: pageSize, Total: total},
	}
}

func (s *service) checkReferences(request CreateFlightRequest) error {
	var v ValidationError

	_, knownAirline := s.reference.Airline(request.AirlineCode)
	_, knownOrigin := s.reference.Airport(request.OriginCode)
	_, knownDestination := s.reference.Airport(request.DestinationCode)

	v.check(knownAirline, "airline_code", "unknown airline")
	v.check(knownOrigin, "origin_code", "unknown airport")
	v.check(knownDestination, "destination_code", "unknown airport")

	return v.err()
}

// applyUpdate merges the request into the locked row. Capacity moves available seats by the same amount, and may not push them below zero.
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

func (s *service) toFlightResponse(flight store.Flight) (FlightResponse, error) {
	airline, origin, destination, err := s.resolve(flight)
	if err != nil {
		return FlightResponse{}, err
	}

	return FlightResponse{
		ID:              flight.ID,
		FlightNumber:    flight.FlightNumber,
		Airline:         airline,
		Origin:          origin.AirportResponse,
		Destination:     destination.AirportResponse,
		DepartureAt:     flight.DepartureAt.In(origin.Location),
		ArrivalAt:       flight.ArrivalAt.In(destination.Location),
		DurationMinutes: durationMinutes(flight),
		Price:           flight.Price,
		AvailableSeats:  flight.AvailableSeats,
	}, nil
}

func (s *service) toAdminFlightResponse(flight store.Flight) (AdminFlightResponse, error) {
	airline, origin, destination, err := s.resolve(flight)
	if err != nil {
		return AdminFlightResponse{}, err
	}

	return AdminFlightResponse{
		ID:              flight.ID,
		FlightNumber:    flight.FlightNumber,
		Airline:         airline,
		Origin:          origin.AirportResponse,
		Destination:     destination.AirportResponse,
		DepartureAt:     flight.DepartureAt.In(origin.Location),
		ArrivalAt:       flight.ArrivalAt.In(destination.Location),
		DurationMinutes: durationMinutes(flight),
		Price:           flight.Price,
		Capacity:        flight.Capacity,
		AvailableSeats:  flight.AvailableSeats,
		CreatedAt:       flight.CreatedAt.UTC(),
		UpdatedAt:       flight.UpdatedAt.UTC(),
	}, nil
}

// resolve looks up the reference data a flight row points at.
func (s *service) resolve(flight store.Flight) (AirlineResponse, Airport, Airport, error) {
	airline, ok := s.reference.Airline(flight.AirlineCode)
	if !ok {
		return AirlineResponse{}, Airport{}, Airport{}, fmt.Errorf("flight %s: unknown airline %q", flight.ID, flight.AirlineCode)
	}
	origin, ok := s.reference.Airport(flight.OriginCode)
	if !ok {
		return AirlineResponse{}, Airport{}, Airport{}, fmt.Errorf("flight %s: unknown airport %q", flight.ID, flight.OriginCode)
	}
	destination, ok := s.reference.Airport(flight.DestinationCode)
	if !ok {
		return AirlineResponse{}, Airport{}, Airport{}, fmt.Errorf("flight %s: unknown airport %q", flight.ID, flight.DestinationCode)
	}

	return airline, origin, destination, nil
}

func durationMinutes(flight store.Flight) int {
	return int(flight.ArrivalAt.Sub(flight.DepartureAt) / time.Minute)
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
