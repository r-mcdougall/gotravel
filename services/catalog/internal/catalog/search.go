package catalog

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

func searchFlights(flights []store.Flight, request *SearchFlightsRequest, origin *time.Location, now time.Time) ([]store.Flight, int) {
	matches := make([]store.Flight, 0, len(flights))
	for _, flight := range flights {
		if matchesSearch(flight, request, origin, now) {
			matches = append(matches, flight)
		}
	}

	slices.SortFunc(matches, flightComparator(request.Sort))

	start := int(request.Page-1) * int(request.PageSize)
	if start >= len(matches) {
		return []store.Flight{}, len(matches)
	}
	end := min(start+int(request.PageSize), len(matches))

	return matches[start:end], len(matches)
}

func matchesSearch(flight store.Flight, request *SearchFlightsRequest, origin *time.Location, now time.Time) bool {
	if !flight.DepartureAt.After(now) {
		return false
	}
	if flight.AvailableSeats < request.Passengers {
		return false
	}
	if len(request.Airlines) > 0 && !slices.Contains(request.Airlines, flight.AirlineCode) {
		return false
	}
	if request.MinPrice != nil && flight.Price < *request.MinPrice {
		return false
	}
	if request.MaxPrice != nil && flight.Price > *request.MaxPrice {
		return false
	}

	departure := timeOfDay(flight.DepartureAt.In(origin))
	if request.DepartureFrom != nil && departure < *request.DepartureFrom {
		return false
	}
	if request.DepartureTo != nil && departure > *request.DepartureTo {
		return false
	}

	return true
}

func timeOfDay(t time.Time) time.Duration {
	return time.Duration(t.Hour())*time.Hour + time.Duration(t.Minute())*time.Minute
}

func flightComparator(sort string) func(a, b store.Flight) int {
	return func(a, b store.Flight) int {
		var primary int

		switch sort {
		case sortDepartureDesc:
			primary = b.DepartureAt.Compare(a.DepartureAt)
		case sortPriceAsc:
			primary = cmp.Compare(a.Price, b.Price)
		case sortPriceDesc:
			primary = cmp.Compare(b.Price, a.Price)
		default:
			primary = a.DepartureAt.Compare(b.DepartureAt)
		}

		return cmp.Or(
			primary,
			a.DepartureAt.Compare(b.DepartureAt),
			strings.Compare(a.ID, b.ID),
		)
	}
}
