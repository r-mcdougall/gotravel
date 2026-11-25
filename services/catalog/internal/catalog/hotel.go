package catalog

import (
	"context"
	"fmt"
	"time"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

const hotelNameKey = "hotels_name_key"

// CreateHotel stores a hotel with its room types and records one room_type.created event per room type, in one transaction.
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

// SearchHotels returns the hotels in a city that have a room type for the guests.
// It does not check which rooms are free on the nights asked for: the hold in booking does.
// A city the catalog does not know has no hotels, so it yields an empty page without touching the database.
func (s *service) SearchHotels(ctx context.Context, request SearchHotelsRequest) (HotelListResponse, error) {
	if err := request.Validate(); err != nil {
		return HotelListResponse{}, fmt.Errorf("validate request: %w", err)
	}
	if err := checkCheckIn(request.CheckIn, s.now()); err != nil {
		return HotelListResponse{}, fmt.Errorf("validate request: %w", err)
	}

	city, known := s.reference.City(request.City)
	if !known {
		return newHotelList(nil, request.Page, request.PageSize, 0), nil
	}

	rows, err := s.db.ListRoomTypesByCity(ctx, store.ListRoomTypesByCityParams{
		CityCode: request.City,
		Guests:   request.Guests,
	})
	if err != nil {
		return HotelListResponse{}, fmt.Errorf("list room types by city: %w", err)
	}

	page, total := searchHotels(rows, &request, city)
	return newHotelList(page, request.Page, request.PageSize, int64(total)), nil
}

// checkCheckIn rejects a stay that starts before today. Today is the UTC date, so east of UTC a check-in
// on the local yesterday still passes for a few hours after local midnight.
func checkCheckIn(checkIn, now time.Time) error {
	year, month, day := now.UTC().Date()
	today := time.Date(year, month, day, 0, 0, 0, 0, time.UTC)

	var v ValidationError
	v.check(!checkIn.Before(today), "check_in", "must not be in the past")
	return v.err()
}

func newHotelList(data []HotelResponse, page, pageSize int32, total int64) HotelListResponse {
	if data == nil {
		data = []HotelResponse{}
	}
	return HotelListResponse{
		Data: data,
		Meta: PageMeta{Page: page, PageSize: pageSize, Total: total},
	}
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
