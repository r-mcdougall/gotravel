package catalog

import (
	"context"
	"fmt"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

const hotelNameKey = "hotels_name_key"

func (s *service) CreateHotel(ctx context.Context, request CreateHotelRequest) (AdminHotelResponse, error) {
	request.normalize()
	if err := request.Validate(); err != nil {
		return AdminHotelResponse{}, fmt.Errorf("validate request: %w", err)
	}

	city, known := s.reference.City(request.CityCode)
	if !known {
		var v ValidationError
		v.check(false, "city_code", "unknown city")
		return AdminHotelResponse{}, fmt.Errorf("validate request: %w", v.err())
	}

	var (
		hotel     store.Hotel
		roomTypes []store.RoomType
	)
	fn := func(q queries) error {
		created, err := q.CreateHotel(ctx, store.CreateHotelParams{
			Name:       request.Name,
			CityCode:   request.CityCode,
			Address:    request.Address,
			StarRating: request.StarRating,
		})
		if err != nil {
			return hotelWriteError("create hotel", err)
		}

		rooms := make([]store.RoomType, 0, len(request.RoomTypes))
		for _, room := range request.RoomTypes {
			roomType, err := q.CreateRoomType(ctx, store.CreateRoomTypeParams{
				HotelID:       created.ID,
				Name:          room.Name,
				MaxGuests:     room.MaxGuests,
				TotalRooms:    room.TotalRooms,
				PricePerNight: room.PricePerNight,
			})
			if err != nil {
				return fmt.Errorf("create room type: %w", err)
			}
			if err = recordRoomTypeCreated(ctx, q, created, roomType); err != nil {
				return err
			}
			rooms = append(rooms, roomType)
		}

		hotel, roomTypes = created, rooms
		return nil
	}

	if err := s.db.InTx(ctx, fn); err != nil {
		return AdminHotelResponse{}, err
	}

	return toAdminHotelResponse(hotel, city, roomTypes), nil
}

func recordRoomTypeCreated(ctx context.Context, q queries, hotel store.Hotel, roomType store.RoomType) error {
	event, err := newRoomTypeCreatedEvent(hotel, roomType)
	if err != nil {
		return err
	}
	if err = q.InsertOutboxEvent(ctx, event); err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}

	return nil
}

func toAdminHotelResponse(hotel store.Hotel, city CityResponse, roomTypes []store.RoomType) AdminHotelResponse {
	rooms := make([]AdminRoomTypeResponse, len(roomTypes))
	for i, roomType := range roomTypes {
		rooms[i] = AdminRoomTypeResponse{
			ID:            roomType.ID,
			Name:          roomType.Name,
			MaxGuests:     roomType.MaxGuests,
			TotalRooms:    roomType.TotalRooms,
			PricePerNight: roomType.PricePerNight,
		}
	}

	return AdminHotelResponse{
		ID:         hotel.ID,
		Name:       hotel.Name,
		City:       city,
		Address:    hotel.Address,
		StarRating: hotel.StarRating,
		RoomTypes:  rooms,
		CreatedAt:  hotel.CreatedAt.UTC(),
	}
}

func hotelWriteError(op string, err error) error {
	if isUniqueViolation(err, hotelNameKey) {
		return ErrHotelExists
	}
	return fmt.Errorf("%s: %w", op, err)
}
