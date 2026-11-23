package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// hotelSpec is the body of POST /api/v1/admin/hotels.
type hotelSpec struct {
	Name       string         `json:"name"`
	CityCode   string         `json:"city_code"`
	Address    string         `json:"address"`
	StarRating int32          `json:"star_rating"`
	RoomTypes  []roomTypeSpec `json:"room_types"`
}

type roomTypeSpec struct {
	Name          string `json:"name"`
	MaxGuests     int32  `json:"max_guests"`
	TotalRooms    int32  `json:"total_rooms"`
	PricePerNight int64  `json:"price_per_night"`
}

// hotelCity is a city from the catalog's reference data with the price of an ordinary room there.
type hotelCity struct {
	Code      string
	Name      string
	BasePrice int64 // rupiah per night
}

var hotelCities = []hotelCity{
	{"JKT", "Jakarta", 800_000},
	{"SUB", "Surabaya", 600_000},
	{"MES", "Medan", 550_000},
	{"JOG", "Yogyakarta", 550_000},
	{"DPS", "Denpasar", 900_000},
	{"UPG", "Makassar", 550_000},
	{"BPN", "Balikpapan", 600_000},
	{"DJJ", "Jayapura", 650_000},
	{"SIN", "Singapore", 2_200_000},
	{"KUL", "Kuala Lumpur", 1_200_000},
	{"TYO", "Tokyo", 2_500_000},
}

// hotelTemplate is one kind of hotel every city gets.
type hotelTemplate struct {
	Suffix     string
	StarRating int32
	RoomTypes  []roomTemplate
}

type roomTemplate struct {
	Name         string
	MaxGuests    int32
	TotalRooms   int32
	PricePercent int64 // of the city's base price; whole numbers keep the rounding exact
}

var hotelTemplates = []hotelTemplate{
	{"Budget Inn", 2, []roomTemplate{
		{"Standard Single", 1, 20, 35},
		{"Standard Twin", 2, 30, 45},
	}},
	{"Central Hotel", 3, []roomTemplate{
		{"Superior Double", 2, 40, 70},
		{"Family Room", 4, 10, 120},
	}},
	{"Grand Resort", 5, []roomTemplate{
		{"Deluxe King", 2, 25, 160},
		{"Junior Suite", 3, 10, 240},
		{"Family Suite", 4, 5, 300},
	}},
}

// planHotels lists every hotel the seed creates: each template in each city. It is the same on every run.
func planHotels() []hotelSpec {
	hotels := make([]hotelSpec, 0, len(hotelCities)*len(hotelTemplates))
	for _, city := range hotelCities {
		for i, template := range hotelTemplates {
			rooms := make([]roomTypeSpec, len(template.RoomTypes))
			for j, room := range template.RoomTypes {
				rooms[j] = roomTypeSpec{
					Name:          room.Name,
					MaxGuests:     room.MaxGuests,
					TotalRooms:    room.TotalRooms,
					PricePerNight: roundToStep(city.BasePrice * room.PricePercent / 100),
				}
			}

			hotels = append(hotels, hotelSpec{
				Name:       city.Name + " " + template.Suffix,
				CityCode:   city.Code,
				Address:    fmt.Sprintf("Demo Street %d, %s", i+1, city.Name),
				StarRating: template.StarRating,
				RoomTypes:  rooms,
			})
		}
	}
	return hotels
}

// roundToStep rounds a price to the nearest priceStep, halves up.
func roundToStep(price int64) int64 {
	return (price + priceStep/2) / priceStep * priceStep
}

// hotelCreator is what seedHotels needs from the API client.
type hotelCreator interface {
	createHotel(ctx context.Context, spec hotelSpec) (outcome, error)
}

// seedHotels creates the hotels one after another; there are few, so a worker pool would add nothing.
// It stops early only when the context is cancelled, and a hotel cut short that way is not a failure.
func seedHotels(ctx context.Context, creator hotelCreator, hotels []hotelSpec, log *slog.Logger) summary {
	var s summary
	for _, spec := range hotels {
		if ctx.Err() != nil {
			break
		}

		created, err := creator.createHotel(ctx, spec)
		switch {
		case err == nil && created == outcomeCreated:
			s.created++
		case err == nil:
			s.existing++
		case errors.Is(err, context.Canceled) && ctx.Err() != nil:
			return s
		default:
			s.failed++
			log.Warn("create hotel failed", slog.Any("error", err))
		}
	}
	return s
}
