package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

type queries interface {
	HoldSeats(ctx context.Context, arg store.HoldSeatsParams) (store.FlightInventory, error)
	ReleaseHeldSeats(ctx context.Context, arg store.ReleaseHeldSeatsParams) (store.FlightInventory, error)
	GetFlightInventory(ctx context.Context, flightID string) (store.FlightInventory, error)
	CreateBooking(ctx context.Context, arg store.CreateBookingParams) (store.Booking, error)
	CreatePassengers(ctx context.Context, arg []store.CreatePassengersParams) (int64, error)
	GetBooking(ctx context.Context, arg store.GetBookingParams) (store.GetBookingRow, error)
	ListBookings(ctx context.Context, arg store.ListBookingsParams) ([]store.ListBookingsRow, error)
	CountBookings(ctx context.Context, userID string) (int64, error)
	ListPassengers(ctx context.Context, bookingIDs []string) ([]store.Passenger, error)
	CancelBooking(ctx context.Context, arg store.CancelBookingParams) (store.Booking, error)
	InsertOutboxEvent(ctx context.Context, arg store.InsertOutboxEventParams) error
	ExpireHolds(ctx context.Context, batchSize int32) ([]store.ExpireHoldsRow, error)
	UpsertFlightInventory(ctx context.Context, arg store.UpsertFlightInventoryParams) (store.FlightInventory, error)
	ConfirmBooking(ctx context.Context, arg store.ConfirmBookingParams) (store.Booking, error)
	GetBookingPayment(ctx context.Context, id string) (store.GetBookingPaymentRow, error)
	SellHeldSeats(ctx context.Context, arg store.SellHeldSeatsParams) error
	SummarizeBookingsByStatus(ctx context.Context, arg store.SummarizeBookingsByStatusParams) ([]store.SummarizeBookingsByStatusRow, error)
	ListTopRoutes(ctx context.Context, arg store.ListTopRoutesParams) ([]store.ListTopRoutesRow, error)

	InsertRoomType(ctx context.Context, arg store.InsertRoomTypeParams) error
	GetRoomType(ctx context.Context, roomTypeID string) (store.RoomType, error)
	EnsureRoomNights(ctx context.Context, arg store.EnsureRoomNightsParams) error
	LockRoomNights(ctx context.Context, arg store.LockRoomNightsParams) ([]store.RoomNight, error)
	HoldRoomNights(ctx context.Context, arg store.HoldRoomNightsParams) error
	ReleaseRoomNights(ctx context.Context, arg store.ReleaseRoomNightsParams) error
	CreateHotelBooking(ctx context.Context, arg store.CreateHotelBookingParams) (store.HotelBooking, error)
	GetHotelBooking(ctx context.Context, arg store.GetHotelBookingParams) (store.GetHotelBookingRow, error)
	ListHotelBookings(ctx context.Context, arg store.ListHotelBookingsParams) ([]store.ListHotelBookingsRow, error)
	CountHotelBookings(ctx context.Context, userID string) (int64, error)
	CancelHotelBooking(ctx context.Context, arg store.CancelHotelBookingParams) (store.HotelBooking, error)
	ExpireHotelHold(ctx context.Context) (store.ExpireHotelHoldRow, error)
}

var _ queries = (*store.Queries)(nil)

type bookingStore interface {
	queries
	InTx(ctx context.Context, fn func(queries) error) error
}

type service struct {
	db      bookingStore
	holdTTL time.Duration
	now     func() time.Time
}

func NewService(db bookingStore, holdTTL time.Duration) Service {
	return newService(db, holdTTL, time.Now)
}

func newService(db bookingStore, holdTTL time.Duration, now func() time.Time) *service {
	return &service{db: db, holdTTL: holdTTL, now: now}
}

// Hold reserves seats for every passenger and records the booking in one transaction,
// so two holds can never share the last seat.
func (s *service) Hold(ctx context.Context, userID string, request HoldRequest) (Response, error) {
	request.normalize()
	if err := request.Validate(); err != nil {
		return Response{}, fmt.Errorf("validate request: %w", err)
	}

	seats := int32(len(request.Passengers)) //nolint:gosec // Validate caps the party at maxPassengers
	var (
		booking    store.Booking
		flight     store.FlightInventory
		passengers []store.Passenger
	)

	fn := func(q queries) error {
		var err error
		flight, err = q.HoldSeats(ctx, store.HoldSeatsParams{Seats: seats, FlightID: request.FlightID})
		if errors.Is(err, pgx.ErrNoRows) {
			return s.holdRefusal(ctx, q, request.FlightID)
		}
		if err != nil {
			return fmt.Errorf("hold seats: %w", err)
		}

		booking, err = q.CreateBooking(ctx, store.CreateBookingParams{
			UserID:         userID,
			FlightID:       flight.FlightID,
			PassengerCount: seats,
			SeatPrice:      flight.Price,
			ExpiresAt:      s.now().Add(s.holdTTL).UTC(),
		})
		if err != nil {
			return fmt.Errorf("create booking: %w", err)
		}

		passengers = newPassengers(booking.ID, request.Passengers)
		params := make([]store.CreatePassengersParams, len(passengers))
		for i, p := range passengers {
			params[i] = store.CreatePassengersParams{BookingID: p.BookingID, Seq: p.Seq, FullName: p.FullName, BirthDate: p.BirthDate}
		}
		if _, err = q.CreatePassengers(ctx, params); err != nil {
			return fmt.Errorf("create passengers: %w", err)
		}

		return recordAvailabilityChanged(ctx, q, flight)
	}

	if err := s.db.InTx(ctx, fn); err != nil {
		return Response{}, err
	}

	return toResponse(booking, flight, passengers), nil
}

// holdRefusal explains why HoldSeats changed no row.
func (s *service) holdRefusal(ctx context.Context, q queries, flightID string) error {
	flight, err := q.GetFlightInventory(ctx, flightID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrFlightNotFound
	case err != nil:
		return fmt.Errorf("get flight inventory: %w", err)
	case !flight.DepartureAt.After(s.now()):
		return ErrFlightDeparted
	default:
		return ErrNotEnoughSeats
	}
}

// FlightSeats returns the seat counters of a flight's inventory.
func (s *service) FlightSeats(ctx context.Context, flightID string) (FlightSeats, error) {
	if !validUUID(flightID) {
		return FlightSeats{}, ErrFlightNotFound
	}

	inventory, err := s.db.GetFlightInventory(ctx, flightID)
	if errors.Is(err, pgx.ErrNoRows) {
		return FlightSeats{}, ErrFlightNotFound
	}
	if err != nil {
		return FlightSeats{}, fmt.Errorf("get flight inventory: %w", err)
	}

	return FlightSeats{
		FlightID:  inventory.FlightID,
		Capacity:  inventory.Capacity,
		HeldSeats: inventory.HeldSeats,
		SoldSeats: inventory.SoldSeats,
	}, nil
}

func (s *service) GetBooking(ctx context.Context, userID, id string) (Response, error) {
	if !validUUID(id) {
		return Response{}, ErrNotFound
	}

	row, err := s.db.GetBooking(ctx, store.GetBookingParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Response{}, ErrNotFound
	}
	if err != nil {
		return Response{}, fmt.Errorf("get booking: %w", err)
	}

	passengers, err := s.db.ListPassengers(ctx, []string{id})
	if err != nil {
		return Response{}, fmt.Errorf("list passengers: %w", err)
	}

	return toResponse(row.Booking, row.FlightInventory, passengers), nil
}

func (s *service) ListBookings(ctx context.Context, userID string, request ListRequest) (ListResponse, error) {
	if err := request.Validate(); err != nil {
		return ListResponse{}, fmt.Errorf("validate request: %w", err)
	}

	rows, err := s.db.ListBookings(ctx, store.ListBookingsParams{
		UserID:    userID,
		RowOffset: (request.Page - 1) * request.PageSize,
		RowLimit:  request.PageSize,
	})
	if err != nil {
		return ListResponse{}, fmt.Errorf("list bookings: %w", err)
	}

	total, err := s.db.CountBookings(ctx, userID)
	if err != nil {
		return ListResponse{}, fmt.Errorf("count bookings: %w", err)
	}

	byBooking := map[string][]store.Passenger{}
	if len(rows) > 0 {
		ids := make([]string, len(rows))
		for i, row := range rows {
			ids[i] = row.Booking.ID
		}
		passengers, err := s.db.ListPassengers(ctx, ids)
		if err != nil {
			return ListResponse{}, fmt.Errorf("list passengers: %w", err)
		}
		for _, p := range passengers {
			byBooking[p.BookingID] = append(byBooking[p.BookingID], p)
		}
	}

	data := make([]Response, len(rows))
	for i, row := range rows {
		data[i] = toResponse(row.Booking, row.FlightInventory, byBooking[row.Booking.ID])
	}

	return ListResponse{
		Data: data,
		Meta: PageMeta{Page: request.Page, PageSize: request.PageSize, Total: total},
	}, nil
}

func (s *service) CancelBooking(ctx context.Context, userID, id string) (Response, error) {
	if !validUUID(id) {
		return Response{}, ErrNotFound
	}

	var (
		booking store.Booking
		flight  store.FlightInventory
	)

	fn := func(q queries) error {
		var err error
		booking, err = q.CancelBooking(ctx, store.CancelBookingParams{ID: id, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return cancelRefusal(ctx, q, userID, id)
		}
		if err != nil {
			return fmt.Errorf("cancel booking: %w", err)
		}

		flight, err = q.ReleaseHeldSeats(ctx, store.ReleaseHeldSeatsParams{Seats: booking.PassengerCount, FlightID: booking.FlightID})
		if err != nil {
			return fmt.Errorf("release held seats: %w", err)
		}

		return recordAvailabilityChanged(ctx, q, flight)
	}

	if err := s.db.InTx(ctx, fn); err != nil {
		return Response{}, err
	}

	passengers, err := s.db.ListPassengers(ctx, []string{id})
	if err != nil {
		return Response{}, fmt.Errorf("list passengers: %w", err)
	}

	return toResponse(booking, flight, passengers), nil
}

// cancelRefusal explains why CancelBooking changed no row.
func cancelRefusal(ctx context.Context, q queries, userID, id string) error {
	_, err := q.GetBooking(ctx, store.GetBookingParams{ID: id, UserID: userID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("get booking: %w", err)
	default:
		return ErrNotHeld
	}
}

func newPassengers(bookingID string, requests []PassengerRequest) []store.Passenger {
	passengers := make([]store.Passenger, len(requests))
	for i, p := range requests {
		birthDate, _ := time.Parse(time.DateOnly, p.BirthDate) // already validated
		seq := int16(i + 1)
		passengers[i] = store.Passenger{BookingID: bookingID, Seq: seq, FullName: p.FullName, BirthDate: birthDate}
	}
	return passengers
}

func toResponse(booking store.Booking, flight store.FlightInventory, passengers []store.Passenger) Response {
	items := make([]PassengerResponse, len(passengers))
	for i, p := range passengers {
		items[i] = PassengerResponse{FullName: p.FullName, BirthDate: p.BirthDate.Format(time.DateOnly)}
	}

	return Response{
		ID:     booking.ID,
		Status: booking.Status,
		Flight: FlightResponse{
			ID:              flight.FlightID,
			FlightNumber:    flight.FlightNumber,
			AirlineCode:     flight.AirlineCode,
			OriginCode:      flight.OriginCode,
			DestinationCode: flight.DestinationCode,
			DepartureAt:     flight.DepartureAt.UTC(),
			ArrivalAt:       flight.ArrivalAt.UTC(),
		},
		Passengers:     items,
		PassengerCount: booking.PassengerCount,
		SeatPrice:      booking.SeatPrice,
		TotalPrice:     booking.SeatPrice * int64(booking.PassengerCount),
		ExpiresAt:      booking.ExpiresAt.UTC(),
		PaymentID:      booking.PaymentID,
		CreatedAt:      booking.CreatedAt.UTC(),
		UpdatedAt:      booking.UpdatedAt.UTC(),
	}
}
