package booking

import (
	"context"
	"fmt"
	"time"

	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

// Analytics summarizes the bookings created in a period of UTC days: bookings per status,
// revenue from confirmed bookings and the routes with the most confirmed bookings.
func (s *service) Analytics(ctx context.Context, request AnalyticsRequest) (Analytics, error) {
	request.normalize(s.now())
	if err := request.Validate(); err != nil {
		return Analytics{}, fmt.Errorf("validate request: %w", err)
	}

	from, _ := time.Parse(time.DateOnly, request.From)
	to, _ := time.Parse(time.DateOnly, request.To)
	createdTo := to.AddDate(0, 0, 1)

	statuses, err := s.db.SummarizeBookingsByStatus(ctx, store.SummarizeBookingsByStatusParams{CreatedFrom: from, CreatedTo: createdTo})
	if err != nil {
		return Analytics{}, fmt.Errorf("summarize bookings by status: %w", err)
	}

	routes, err := s.db.ListTopRoutes(ctx, store.ListTopRoutesParams{CreatedFrom: from, CreatedTo: createdTo, MaxRoutes: maxTopRoutes})
	if err != nil {
		return Analytics{}, fmt.Errorf("list top routes: %w", err)
	}

	analytics := Analytics{From: request.From, To: request.To, TopRoutes: make([]RouteSummary, 0, len(routes))}

	for _, row := range statuses {
		switch row.Status {
		case "HELD":
			analytics.BookingsByStatus.Held = row.Bookings
		case "CONFIRMED":
			analytics.BookingsByStatus.Confirmed = row.Bookings
			analytics.Revenue = row.Amount
		case "EXPIRED":
			analytics.BookingsByStatus.Expired = row.Bookings
		case "CANCELLED":
			analytics.BookingsByStatus.Cancelled = row.Bookings
		}
	}

	for _, row := range routes {
		analytics.TopRoutes = append(analytics.TopRoutes, RouteSummary{
			OriginCode:      row.OriginCode,
			DestinationCode: row.DestinationCode,
			Bookings:        row.Bookings,
			Seats:           row.Seats,
			Revenue:         row.Revenue,
		})
	}

	return analytics, nil
}
