package catalog

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

var denpasar = CityResponse{Code: "DPS", Name: "Denpasar"}

func hotelRow(hotelID, hotelName, roomID string, maxGuests int32, price int64) store.ListRoomTypesByCityRow {
	return store.ListRoomTypesByCityRow{
		Hotel: store.Hotel{ID: hotelID, Name: hotelName, CityCode: "DPS", Address: hotelName + " address", StarRating: 4},
		RoomType: store.RoomType{
			ID: roomID, HotelID: hotelID, Name: roomID, MaxGuests: maxGuests, TotalRooms: 10, PricePerNight: price,
		},
	}
}

// hotelSearchFixture returns four room types of three hotels, deliberately out of order.
func hotelSearchFixture() []store.ListRoomTypesByCityRow {
	return []store.ListRoomTypesByCityRow{
		hotelRow("garden", "Seminyak Garden Hotel", "garden-family", 4, 1_600_000),
		hotelRow("villa", "Ubud Villa", "villa-suite", 4, 2_500_000),
		hotelRow("budget", "Kuta Budget Inn", "budget-twin", 2, 450_000),
		hotelRow("garden", "Seminyak Garden Hotel", "garden-deluxe", 2, 850_000),
	}
}

func newHotelSearchRequest(mutate func(r *SearchHotelsRequest)) *SearchHotelsRequest {
	request := validSearchHotelsRequest()
	if mutate != nil {
		mutate(&request)
	}
	return &request
}

func hotelIDs(hotels []HotelResponse) []string {
	ids := make([]string, len(hotels))
	for i, hotel := range hotels {
		ids[i] = hotel.ID
	}
	return ids
}

func roomTypeIDs(hotel HotelResponse) []string {
	ids := make([]string, len(hotel.RoomTypes))
	for i, room := range hotel.RoomTypes {
		ids[i] = room.ID
	}
	return ids
}

func TestSearchHotels_groupsRoomTypesIntoTheirHotelsCheapestFirst(t *testing.T) {
	page, total := searchHotels(hotelSearchFixture(), newHotelSearchRequest(nil), denpasar)

	require.Equal(t, []string{"budget", "garden", "villa"}, hotelIDs(page))
	require.Equal(t, 3, total)

	garden := page[1]
	require.Equal(t, "Seminyak Garden Hotel", garden.Name)
	require.Equal(t, denpasar, garden.City)
	require.Equal(t, "Seminyak Garden Hotel address", garden.Address)
	require.Equal(t, int32(4), garden.StarRating)
	require.Equal(t, int64(850_000), garden.LowestPricePerNight)
	require.Equal(t, []RoomTypeResponse{
		{ID: "garden-deluxe", Name: "garden-deluxe", MaxGuests: 2, PricePerNight: 850_000, TotalPrice: 1_700_000},
		{ID: "garden-family", Name: "garden-family", MaxGuests: 4, PricePerNight: 1_600_000, TotalPrice: 3_200_000},
	}, garden.RoomTypes)
}

func TestSearchHotels_pricesTheWholeStay(t *testing.T) {
	request := newHotelSearchRequest(func(r *SearchHotelsRequest) { r.CheckOut = r.CheckIn.AddDate(0, 0, maxNights) })

	page, _ := searchHotels(hotelSearchFixture(), request, denpasar)

	require.Equal(t, int64(450_000*14), page[0].RoomTypes[0].TotalPrice)
	require.Equal(t, int64(450_000), page[0].RoomTypes[0].PricePerNight)
}

func TestSearchHotels_filtersRoomTypesByPricePerNight(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *SearchHotelsRequest)
		hotels []string
		rooms  map[string][]string
	}{
		{"minimum price is inclusive", func(r *SearchHotelsRequest) { r.MinPrice = ptr(int64(850_000)) },
			[]string{"garden", "villa"}, map[string][]string{"garden": {"garden-deluxe", "garden-family"}}},
		{"maximum price is inclusive and drops a hotel's dearer rooms", func(r *SearchHotelsRequest) { r.MaxPrice = ptr(int64(850_000)) },
			[]string{"budget", "garden"}, map[string][]string{"garden": {"garden-deluxe"}}},
		{"a range that keeps only a hotel's dearer room ranks the hotel by it", func(r *SearchHotelsRequest) {
			r.MinPrice = ptr(int64(900_000))
			r.MaxPrice = ptr(int64(2_000_000))
		}, []string{"garden"}, map[string][]string{"garden": {"garden-family"}}},
		{"a range with no room in it", func(r *SearchHotelsRequest) {
			r.MinPrice = ptr(int64(1))
			r.MaxPrice = ptr(int64(400_000))
		}, []string{}, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, total := searchHotels(hotelSearchFixture(), newHotelSearchRequest(tc.mutate), denpasar)

			require.Equal(t, tc.hotels, hotelIDs(page))
			require.Equal(t, len(tc.hotels), total)
			for _, hotel := range page {
				if want, ok := tc.rooms[hotel.ID]; ok {
					require.Equal(t, want, roomTypeIDs(hotel))
					require.Equal(t, hotel.RoomTypes[0].PricePerNight, hotel.LowestPricePerNight)
				}
			}
		})
	}
}

func TestSearchHotels_sortsHotelsByTheirLowestPrice(t *testing.T) {
	tests := []struct {
		sort string
		want []string
	}{
		{sortPriceAsc, []string{"budget", "garden", "villa"}},
		{sortPriceDesc, []string{"villa", "garden", "budget"}},
	}

	for _, tc := range tests {
		t.Run(tc.sort, func(t *testing.T) {
			request := newHotelSearchRequest(func(r *SearchHotelsRequest) { r.Sort = tc.sort })

			page, _ := searchHotels(hotelSearchFixture(), request, denpasar)

			require.Equal(t, tc.want, hotelIDs(page))
		})
	}
}

func TestSearchHotels_breaksTiesByNameThenID(t *testing.T) {
	rows := []store.ListRoomTypesByCityRow{
		hotelRow("b", "Same Name", "b-room", 2, 500_000),
		hotelRow("c", "Another Name", "c-room", 2, 500_000),
		hotelRow("a", "Same Name", "a-room", 2, 500_000),
	}

	for _, sort := range []string{sortPriceAsc, sortPriceDesc} {
		t.Run(sort, func(t *testing.T) {
			request := newHotelSearchRequest(func(r *SearchHotelsRequest) { r.Sort = sort })

			page, _ := searchHotels(rows, request, denpasar)

			require.Equal(t, []string{"c", "a", "b"}, hotelIDs(page))
		})
	}
}

func TestSearchHotels_breaksTiesBetweenRoomTypesByID(t *testing.T) {
	rows := []store.ListRoomTypesByCityRow{
		hotelRow("garden", "Seminyak Garden Hotel", "room-b", 2, 500_000),
		hotelRow("garden", "Seminyak Garden Hotel", "room-a", 2, 500_000),
	}

	page, _ := searchHotels(rows, newHotelSearchRequest(nil), denpasar)

	require.Equal(t, []string{"room-a", "room-b"}, roomTypeIDs(page[0]))
}

func TestSearchHotels_returnsTheRequestedPageAndTheTotalAcrossPages(t *testing.T) {
	tests := []struct {
		name     string
		page     int32
		pageSize int32
		want     []string
	}{
		{"first page", 1, 2, []string{"budget", "garden"}},
		{"last page is shorter", 2, 2, []string{"villa"}},
		{"page past the end", 3, 2, []string{}},
		{"largest page", maxPage, maxPageSize, []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := newHotelSearchRequest(func(r *SearchHotelsRequest) {
				r.Page = tc.page
				r.PageSize = tc.pageSize
			})

			page, total := searchHotels(hotelSearchFixture(), request, denpasar)

			require.NotNil(t, page)
			require.Equal(t, tc.want, hotelIDs(page))
			require.Equal(t, 3, total)
		})
	}
}

func TestSearchHotels_returnsNothingForNoRoomTypes(t *testing.T) {
	page, total := searchHotels(nil, newHotelSearchRequest(nil), denpasar)

	require.NotNil(t, page)
	require.Empty(t, page)
	require.Equal(t, 0, total)
}
