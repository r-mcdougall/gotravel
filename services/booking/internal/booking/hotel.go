package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

// HoldRoom takes one room of the type for every night of the stay in one transaction. The nights are
// locked in date order, so holds over overlapping nights wait for each other instead of deadlocking.
func (s *service) HoldRoom(ctx context.Context, userID string, request HotelHoldRequest) (HotelBookingResponse, error) {
	request.normalize()
	if err := request.Validate(); err != nil {
		return HotelBookingResponse{}, fmt.Errorf("validate request: %w", err)
	}

	checkIn, checkOut := request.stay()
	if err := checkCheckIn(checkIn, s.now()); err != nil {
		return HotelBookingResponse{}, fmt.Errorf("validate request: %w", err)
	}

	roomType, err := s.db.GetRoomType(ctx, request.RoomTypeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return HotelBookingResponse{}, ErrRoomTypeNotFound
	}
	if err != nil {
		return HotelBookingResponse{}, fmt.Errorf("get room type: %w", err)
	}

	var v ValidationError
	v.check(request.Guests <= roomType.MaxGuests, "guests", fmt.Sprintf("must be at most %d for this room type", roomType.MaxGuests))
	if err := v.err(); err != nil {
		return HotelBookingResponse{}, fmt.Errorf("validate request: %w", err)
	}

	var booking store.HotelBooking
	fn := func(q queries) error {
		err := q.EnsureRoomNights(ctx, store.EnsureRoomNightsParams{CheckIn: checkIn, CheckOut: checkOut, RoomTypeID: roomType.RoomTypeID})
		if err != nil {
			return fmt.Errorf("ensure room nights: %w", err)
		}

		nights, err := q.LockRoomNights(ctx, store.LockRoomNightsParams{RoomTypeID: roomType.RoomTypeID, CheckIn: checkIn, CheckOut: checkOut})
		if err != nil {
			return fmt.Errorf("lock room nights: %w", err)
		}
		for _, night := range nights {
			if night.HeldRooms+night.SoldRooms >= night.TotalRooms {
				return ErrRoomsUnavailable
			}
		}

		err = q.HoldRoomNights(ctx, store.HoldRoomNightsParams{RoomTypeID: roomType.RoomTypeID, CheckIn: checkIn, CheckOut: checkOut})
		if err != nil {
			return fmt.Errorf("hold room nights: %w", err)
		}

		booking, err = q.CreateHotelBooking(ctx, store.CreateHotelBookingParams{
			UserID:        userID,
			RoomTypeID:    roomType.RoomTypeID,
			CheckIn:       checkIn,
			CheckOut:      checkOut,
			Guests:        request.Guests,
			GuestName:     request.GuestName,
			PricePerNight: roomType.PricePerNight,
			ExpiresAt:     s.now().Add(s.holdTTL).UTC(),
		})
		if err != nil {
			return fmt.Errorf("create hotel booking: %w", err)
		}
		return nil
	}

	if err := s.db.InTx(ctx, fn); err != nil {
		return HotelBookingResponse{}, err
	}

	return toHotelResponse(booking, roomType), nil
}

func (s *service) GetHotelBooking(ctx context.Context, userID, id string) (HotelBookingResponse, error) {
	if !validUUID(id) {
		return HotelBookingResponse{}, ErrNotFound
	}

	row, err := s.db.GetHotelBooking(ctx, store.GetHotelBookingParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return HotelBookingResponse{}, ErrNotFound
	}
	if err != nil {
		return HotelBookingResponse{}, fmt.Errorf("get hotel booking: %w", err)
	}

	return toHotelResponse(row.HotelBooking, row.RoomType), nil
}

func (s *service) ListHotelBookings(ctx context.Context, userID string, request ListRequest) (HotelBookingListResponse, error) {
	if err := request.Validate(); err != nil {
		return HotelBookingListResponse{}, fmt.Errorf("validate request: %w", err)
	}

	rows, err := s.db.ListHotelBookings(ctx, store.ListHotelBookingsParams{
		UserID:    userID,
		RowOffset: (request.Page - 1) * request.PageSize,
		RowLimit:  request.PageSize,
	})
	if err != nil {
		return HotelBookingListResponse{}, fmt.Errorf("list hotel bookings: %w", err)
	}

	total, err := s.db.CountHotelBookings(ctx, userID)
	if err != nil {
		return HotelBookingListResponse{}, fmt.Errorf("count hotel bookings: %w", err)
	}

	data := make([]HotelBookingResponse, len(rows))
	for i, row := range rows {
		data[i] = toHotelResponse(row.HotelBooking, row.RoomType)
	}

	return HotelBookingListResponse{
		Data: data,
		Meta: PageMeta{Page: request.Page, PageSize: request.PageSize, Total: total},
	}, nil
}

func (s *service) CancelHotelBooking(ctx context.Context, userID, id string) (HotelBookingResponse, error) {
	if !validUUID(id) {
		return HotelBookingResponse{}, ErrNotFound
	}

	var booking store.HotelBooking
	fn := func(q queries) error {
		var err error
		booking, err = q.CancelHotelBooking(ctx, store.CancelHotelBookingParams{ID: id, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return hotelCancelRefusal(ctx, q, userID, id)
		}
		if err != nil {
			return fmt.Errorf("cancel hotel booking: %w", err)
		}

		return releaseRoomNights(ctx, q, booking.RoomTypeID, booking.CheckIn, booking.CheckOut)
	}

	if err := s.db.InTx(ctx, fn); err != nil {
		return HotelBookingResponse{}, err
	}

	roomType, err := s.db.GetRoomType(ctx, booking.RoomTypeID)
	if err != nil {
		return HotelBookingResponse{}, fmt.Errorf("get room type: %w", err)
	}

	return toHotelResponse(booking, roomType), nil
}

// hotelCancelRefusal explains why CancelHotelBooking changed no row.
func hotelCancelRefusal(ctx context.Context, q queries, userID, id string) error {
	_, err := q.GetHotelBooking(ctx, store.GetHotelBookingParams{ID: id, UserID: userID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return ErrNotFound
	case err != nil:
		return fmt.Errorf("get hotel booking: %w", err)
	default:
		return ErrNotHeld
	}
}

// releaseRoomNights gives back the room of a stay. The nights are locked in date order first, like a hold
// does, because a plain UPDATE may lock them in any order.
func releaseRoomNights(ctx context.Context, q queries, roomTypeID string, checkIn, checkOut time.Time) error {
	if _, err := q.LockRoomNights(ctx, store.LockRoomNightsParams{RoomTypeID: roomTypeID, CheckIn: checkIn, CheckOut: checkOut}); err != nil {
		return fmt.Errorf("lock room nights: %w", err)
	}

	err := q.ReleaseRoomNights(ctx, store.ReleaseRoomNightsParams{RoomTypeID: roomTypeID, CheckIn: checkIn, CheckOut: checkOut})
	if err != nil {
		return fmt.Errorf("release room nights: %w", err)
	}
	return nil
}

func toHotelResponse(booking store.HotelBooking, roomType store.RoomType) HotelBookingResponse {
	stay := nights(booking.CheckIn, booking.CheckOut)

	return HotelBookingResponse{
		ID:     booking.ID,
		Status: booking.Status,
		Hotel: HotelSummary{
			ID:       roomType.HotelID,
			Name:     roomType.HotelName,
			CityCode: roomType.CityCode,
		},
		RoomType:      RoomTypeSummary{ID: roomType.RoomTypeID, Name: roomType.Name},
		CheckIn:       booking.CheckIn.Format(time.DateOnly),
		CheckOut:      booking.CheckOut.Format(time.DateOnly),
		Nights:        stay,
		Guests:        booking.Guests,
		GuestName:     booking.GuestName,
		PricePerNight: booking.PricePerNight,
		TotalPrice:    booking.PricePerNight * stay,
		ExpiresAt:     booking.ExpiresAt.UTC(),
		PaymentID:     booking.PaymentID,
		CreatedAt:     booking.CreatedAt.UTC(),
		UpdatedAt:     booking.UpdatedAt.UTC(),
	}
}
