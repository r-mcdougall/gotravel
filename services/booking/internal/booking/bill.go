package booking

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

// Bill returns what payment charges for a flight or hotel booking of the user. Flight bookings are looked up first.
func (s *service) Bill(ctx context.Context, userID, id string) (Bill, error) {
	if !validUUID(id) {
		return Bill{}, ErrNotFound
	}

	flight, err := s.db.GetBooking(ctx, store.GetBookingParams{ID: id, UserID: userID})
	if err == nil {
		booking := toResponse(flight.Booking, flight.FlightInventory, nil)
		return Bill{ID: booking.ID, Status: booking.Status, TotalPrice: booking.TotalPrice, ExpiresAt: booking.ExpiresAt}, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Bill{}, fmt.Errorf("get booking: %w", err)
	}

	hotel, err := s.db.GetHotelBooking(ctx, store.GetHotelBookingParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Bill{}, ErrNotFound
	}
	if err != nil {
		return Bill{}, fmt.Errorf("get hotel booking: %w", err)
	}

	booking := toHotelResponse(hotel.HotelBooking, hotel.RoomType)
	return Bill{ID: booking.ID, Status: booking.Status, TotalPrice: booking.TotalPrice, ExpiresAt: booking.ExpiresAt}, nil
}
