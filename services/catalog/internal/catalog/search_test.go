package catalog

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

// All flights fly CGK to DPS on 20 November 2026. Times below are local
// Jakarta time (WIB).
var searchNow = time.Date(2026, 11, 20, 6, 0, 0, 0, wib)

func searchFlight(id, airline string, departure time.Time, price int64, seats int32) store.Flight {
	return store.Flight{
		ID:              id,
		FlightNumber:    airline + "100",
		AirlineCode:     airline,
		OriginCode:      "CGK",
		DestinationCode: "DPS",
		DepartureAt:     departure.UTC(),
		ArrivalAt:       departure.Add(2 * time.Hour).UTC(),
		Price:           price,
		Capacity:        180,
		AvailableSeats:  seats,
	}
}

func wibAt(hour, minute int) time.Time {
	return time.Date(2026, 11, 20, hour, minute, 0, 0, wib)
}

// searchFixture returns six flights in no particular order. Only morning,
// evening and late can be booked by one passenger after searchNow.
func searchFixture() []store.Flight {
	return []store.Flight{
		searchFlight("evening", "QG", wibAt(18, 45), 900_000, 50),
		searchFlight("departed", "GA", wibAt(4, 0), 1_000_000, 5),
		searchFlight("late", "ID", wibAt(23, 30), 700_000, 9),
		searchFlight("at-now", "GA", wibAt(6, 0), 1_200_000, 5),
		searchFlight("noon-sold-out", "GA", wibAt(12, 15), 1_100_000, 0),
		searchFlight("morning", "JT", wibAt(8, 0), 900_000, 3),
	}
}

func newSearchRequest(mutate func(r *SearchFlightsRequest)) *SearchFlightsRequest {
	request := validSearchRequest()
	if mutate != nil {
		mutate(&request)
	}
	return &request
}

func searchIDs(flights []store.Flight) []string {
	ids := make([]string, len(flights))
	for i, flight := range flights {
		ids[i] = flight.ID
	}
	return ids
}

func TestSearchFlights_returnsOnlyBookableUpcomingFlightsByDepartureTime(t *testing.T) {
	page, total := searchFlights(searchFixture(), newSearchRequest(nil), wib, searchNow)

	require.Equal(t, []string{"morning", "evening", "late"}, searchIDs(page))
	require.Equal(t, 3, total)
}

func TestSearchFlights_excludesAFlightThatDepartsExactlyNow(t *testing.T) {
	flights := []store.Flight{searchFlight("at-now", "GA", searchNow, 900_000, 5)}

	page, total := searchFlights(flights, newSearchRequest(nil), wib, searchNow)

	require.Empty(t, page)
	require.Equal(t, 0, total)
}

func TestSearchFlights_filtersByPassengersAirlinesAndPrice(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *SearchFlightsRequest)
		want   []string
	}{
		{"four passengers need four seats", func(r *SearchFlightsRequest) { r.Passengers = 4 }, []string{"evening", "late"}},
		{"exactly as many passengers as seats", func(r *SearchFlightsRequest) { r.Passengers = 9 }, []string{"evening", "late"}},
		{"more passengers than any flight has seats", func(r *SearchFlightsRequest) { r.Passengers = 51 }, []string{}},
		{"two airlines", func(r *SearchFlightsRequest) { r.Airlines = []string{"JT", "QG"} }, []string{"morning", "evening"}},
		{"an airline with no flights", func(r *SearchFlightsRequest) { r.Airlines = []string{"XX"} }, []string{}},
		{"minimum price is inclusive", func(r *SearchFlightsRequest) { r.MinPrice = ptr(int64(900_000)) }, []string{"morning", "evening"}},
		{"maximum price is inclusive", func(r *SearchFlightsRequest) { r.MaxPrice = ptr(int64(900_000)) }, []string{"morning", "evening", "late"}},
		{"price range", func(r *SearchFlightsRequest) {
			r.MinPrice = ptr(int64(800_000))
			r.MaxPrice = ptr(int64(950_000))
		}, []string{"morning", "evening"}},
		{"price range with no flight in it", func(r *SearchFlightsRequest) {
			r.MinPrice = ptr(int64(1))
			r.MaxPrice = ptr(int64(600_000))
		}, []string{}},
		{"filters combine", func(r *SearchFlightsRequest) {
			r.Passengers = 4
			r.Airlines = []string{"QG", "ID"}
			r.MaxPrice = ptr(int64(800_000))
		}, []string{"late"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			page, total := searchFlights(searchFixture(), newSearchRequest(tc.mutate), wib, searchNow)

			require.Equal(t, tc.want, searchIDs(page))
			require.Equal(t, len(tc.want), total)
		})
	}
}

func TestSearchFlights_filtersByLocalDepartureTime(t *testing.T) {
	tests := []struct {
		name     string
		location *time.Location
		from, to *time.Duration
		want     []string
	}{
		{"window includes both ends", wib, ptr(8 * time.Hour), ptr(18*time.Hour + 45*time.Minute), []string{"morning", "evening"}},
		{"earliest departure only", wib, ptr(18*time.Hour + 46*time.Minute), nil, []string{"late"}},
		{"latest departure only", wib, nil, ptr(7*time.Hour + 59*time.Minute), []string{}},
		{"window of a single minute", wib, ptr(23*time.Hour + 30*time.Minute), ptr(23*time.Hour + 30*time.Minute), []string{"late"}},
		// 08:00 in Jakarta is 09:00 in Makassar, so the same instant falls in a
		// different window depending on the origin airport's time zone.
		{"origin zone ahead of Jakarta", wita, ptr(9 * time.Hour), ptr(9 * time.Hour), []string{"morning"}},
		{"origin zone Jakarta", wib, ptr(9 * time.Hour), ptr(9 * time.Hour), []string{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := newSearchRequest(func(r *SearchFlightsRequest) {
				r.DepartureFrom = tc.from
				r.DepartureTo = tc.to
			})

			page, total := searchFlights(searchFixture(), request, tc.location, searchNow)

			require.Equal(t, tc.want, searchIDs(page))
			require.Equal(t, len(tc.want), total)
		})
	}
}

func TestSearchFlights_ignoresSecondsWhenComparingDepartureTimes(t *testing.T) {
	flights := []store.Flight{searchFlight("slightly-late", "GA", time.Date(2026, 11, 20, 9, 0, 30, 0, wib), 900_000, 5)}
	request := newSearchRequest(func(r *SearchFlightsRequest) {
		r.DepartureFrom = ptr(9 * time.Hour)
		r.DepartureTo = ptr(9 * time.Hour)
	})

	page, _ := searchFlights(flights, request, wib, searchNow)

	require.Equal(t, []string{"slightly-late"}, searchIDs(page))
}

func TestSearchFlights_sortsByTheRequestedOrder(t *testing.T) {
	tests := []struct {
		sort string
		want []string
	}{
		{sortDepartureAsc, []string{"morning", "evening", "late"}},
		{sortDepartureDesc, []string{"late", "evening", "morning"}},
		{sortPriceAsc, []string{"late", "morning", "evening"}},
		{sortPriceDesc, []string{"morning", "evening", "late"}},
	}

	for _, tc := range tests {
		t.Run(tc.sort, func(t *testing.T) {
			request := newSearchRequest(func(r *SearchFlightsRequest) { r.Sort = tc.sort })

			page, _ := searchFlights(searchFixture(), request, wib, searchNow)

			require.Equal(t, tc.want, searchIDs(page))
		})
	}
}

func TestSearchFlights_breaksTiesByDepartureThenID(t *testing.T) {
	flights := []store.Flight{
		searchFlight("b", "GA", wibAt(10, 0), 900_000, 5),
		searchFlight("c", "GA", wibAt(9, 0), 900_000, 5),
		searchFlight("a", "GA", wibAt(10, 0), 900_000, 5),
	}

	for _, sort := range []string{sortPriceAsc, sortPriceDesc} {
		t.Run(sort, func(t *testing.T) {
			request := newSearchRequest(func(r *SearchFlightsRequest) { r.Sort = sort })

			page, _ := searchFlights(flights, request, wib, searchNow)

			require.Equal(t, []string{"c", "a", "b"}, searchIDs(page))
		})
	}

	t.Run(sortDepartureDesc, func(t *testing.T) {
		request := newSearchRequest(func(r *SearchFlightsRequest) { r.Sort = sortDepartureDesc })

		page, _ := searchFlights(flights, request, wib, searchNow)

		require.Equal(t, []string{"a", "b", "c"}, searchIDs(page))
	})
}

func TestSearchFlights_returnsTheRequestedPageAndTheTotalAcrossPages(t *testing.T) {
	tests := []struct {
		name     string
		page     int32
		pageSize int32
		want     []string
	}{
		{"first page", 1, 2, []string{"morning", "evening"}},
		{"last page is shorter", 2, 2, []string{"late"}},
		{"page past the end", 3, 2, []string{}},
		{"everything on one page", 1, 100, []string{"morning", "evening", "late"}},
		{"page of one", 3, 1, []string{"late"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			request := newSearchRequest(func(r *SearchFlightsRequest) {
				r.Page = tc.page
				r.PageSize = tc.pageSize
			})

			page, total := searchFlights(searchFixture(), request, wib, searchNow)

			require.Equal(t, tc.want, searchIDs(page))
			require.Equal(t, 3, total)
		})
	}
}

func TestSearchFlights_handlesTheLargestPageWithoutOverflow(t *testing.T) {
	request := newSearchRequest(func(r *SearchFlightsRequest) {
		r.Page = maxPage
		r.PageSize = maxPageSize
	})

	page, total := searchFlights(searchFixture(), request, wib, searchNow)

	require.Empty(t, page)
	require.Equal(t, 3, total)
}

func TestSearchFlights_doesNotModifyTheFlightsItIsGiven(t *testing.T) {
	flights := searchFixture()
	before := searchIDs(flights)
	request := newSearchRequest(func(r *SearchFlightsRequest) { r.Sort = sortPriceDesc })

	searchFlights(flights, request, wib, searchNow)

	require.Equal(t, before, searchIDs(flights))
}

func TestSearchFlights_returnsNothingForNoFlights(t *testing.T) {
	page, total := searchFlights(nil, newSearchRequest(nil), wib, searchNow)

	require.Empty(t, page)
	require.Equal(t, 0, total)
}
