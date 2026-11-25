package catalog

import (
	"cmp"
	"slices"
	"strings"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

// searchHotels groups room types into their hotels, drops room types outside the price range and hotels left
// without any, then sorts and pages the hotels. Each hotel lists its room types from the cheapest.
func searchHotels(rows []store.ListRoomTypesByCityRow, request *SearchHotelsRequest, city CityResponse) ([]HotelResponse, int) {
	nights := request.nights()

	var hotels []HotelResponse
	index := make(map[string]int)
	for _, row := range rows {
		price := row.RoomType.PricePerNight
		if request.MinPrice != nil && price < *request.MinPrice {
			continue
		}
		if request.MaxPrice != nil && price > *request.MaxPrice {
			continue
		}

		i, seen := index[row.Hotel.ID]
		if !seen {
			i = len(hotels)
			index[row.Hotel.ID] = i
			hotels = append(hotels, HotelResponse{
				ID:         row.Hotel.ID,
				Name:       row.Hotel.Name,
				City:       city,
				Address:    row.Hotel.Address,
				StarRating: row.Hotel.StarRating,
			})
		}
		hotels[i].RoomTypes = append(hotels[i].RoomTypes, RoomTypeResponse{
			ID:            row.RoomType.ID,
			Name:          row.RoomType.Name,
			MaxGuests:     row.RoomType.MaxGuests,
			PricePerNight: price,
			TotalPrice:    price * nights,
		})
	}

	for i := range hotels {
		rooms := hotels[i].RoomTypes
		slices.SortFunc(rooms, func(a, b RoomTypeResponse) int {
			return cmp.Or(cmp.Compare(a.PricePerNight, b.PricePerNight), strings.Compare(a.ID, b.ID))
		})
		hotels[i].LowestPricePerNight = rooms[0].PricePerNight
	}
	slices.SortFunc(hotels, hotelComparator(request.Sort))

	start := int(request.Page-1) * int(request.PageSize)
	if start >= len(hotels) {
		return []HotelResponse{}, len(hotels)
	}
	end := min(start+int(request.PageSize), len(hotels))

	return hotels[start:end], len(hotels)
}

func hotelComparator(sort string) func(a, b HotelResponse) int {
	return func(a, b HotelResponse) int {
		primary := cmp.Compare(a.LowestPricePerNight, b.LowestPricePerNight)
		if sort == sortPriceDesc {
			primary = -primary
		}

		return cmp.Or(
			primary,
			strings.Compare(a.Name, b.Name),
			strings.Compare(a.ID, b.ID),
		)
	}
}
