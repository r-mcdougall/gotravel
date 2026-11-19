package booking

import (
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestAnalyticsRequestNormalize(t *testing.T) {
	// 02:00 on 20 November in Jakarta is still 19 November in UTC.
	today := time.Date(2026, 11, 20, 2, 0, 0, 0, time.FixedZone("WIB", 7*60*60))

	tests := []struct {
		name    string
		request AnalyticsRequest
		want    AnalyticsRequest
	}{
		{name: "defaults to the 30 days up to today in UTC", want: AnalyticsRequest{From: "2026-10-21", To: "2026-11-19"}},
		{name: "30 days up to the given end", request: AnalyticsRequest{To: "2026-03-01"}, want: AnalyticsRequest{From: "2026-01-31", To: "2026-03-01"}},
		{name: "from the given start up to today", request: AnalyticsRequest{From: " 2026-11-01 "}, want: AnalyticsRequest{From: "2026-11-01", To: "2026-11-19"}},
		{name: "keeps an invalid end for validation", request: AnalyticsRequest{To: "tomorrow"}, want: AnalyticsRequest{To: "tomorrow"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := tt.request
			request.normalize(today)
			require.Equal(t, tt.want, request)
		})
	}
}

func TestAnalyticsRequestValidate(t *testing.T) {
	tests := []struct {
		name       string
		request    AnalyticsRequest
		wantFields []string
	}{
		{name: "one day", request: AnalyticsRequest{From: "2026-11-19", To: "2026-11-19"}},
		{name: "366 days", request: AnalyticsRequest{From: "2026-01-01", To: "2027-01-01"}},
		{name: "367 days", request: AnalyticsRequest{From: "2026-01-01", To: "2027-01-02"}, wantFields: []string{"to"}},
		{name: "end before start", request: AnalyticsRequest{From: "2026-11-19", To: "2026-11-18"}, wantFields: []string{"to"}},
		{name: "invalid dates", request: AnalyticsRequest{From: "2026-02-30", To: "19/11/2026"}, wantFields: []string{"from", "to"}},
		{name: "invalid end without a start", request: AnalyticsRequest{To: "tomorrow"}, wantFields: []string{"to"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()

			if tt.wantFields == nil {
				require.NoError(t, err)
				return
			}
			var validationErr *ValidationError
			require.ErrorAs(t, err, &validationErr)
			var fields []string
			for _, d := range validationErr.Details {
				fields = append(fields, d.Field)
			}
			require.Equal(t, tt.wantFields, fields)
		})
	}
}

func TestServiceAnalytics_rejectsAnInvalidPeriodBeforeQuerying(t *testing.T) {
	svc := newService(unusedStore{t}, 15*time.Minute, time.Now)

	_, err := svc.Analytics(t.Context(), AnalyticsRequest{From: "2026-11-19", To: "2026-11-01"})

	var validationErr *ValidationError
	require.ErrorAs(t, err, &validationErr)
}

func TestServiceAnalytics_summarizesTheBookingsCreatedInThePeriod(t *testing.T) {
	_, pool, _ := newTestService(t)
	today := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	svc := newService(NewPgStore(pool), 15*time.Minute, func() time.Time { return today })

	cgkDps := addRoute(t, pool, "CGK", "DPS")
	cgkKno := addRoute(t, pool, "CGK", "KNO")
	subJog := addRoute(t, pool, "SUB", "JOG")
	inPeriod := time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	// Routes rank by confirmed bookings first: CGK-KNO sells more seats in fewer bookings.

	addBooking(t, pool, cgkDps, "CONFIRMED", 2, 1_000_000, inPeriod)
	addBooking(t, pool, cgkDps, "CONFIRMED", 1, 1_000_000, inPeriod)
	addBooking(t, pool, cgkKno, "CONFIRMED", 6, 500_000, inPeriod)
	addBooking(t, pool, subJog, "CONFIRMED", 1, 700_000, inPeriod)
	addBooking(t, pool, subJog, "HELD", 3, 700_000, inPeriod)
	addBooking(t, pool, subJog, "EXPIRED", 9, 700_000, inPeriod)
	addBooking(t, pool, subJog, "CANCELLED", 2, 700_000, inPeriod)
	// The period is whole UTC days, from 00:00 of from until 24:00 of to.
	addBooking(t, pool, cgkDps, "CONFIRMED", 1, 1_000_000, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	addBooking(t, pool, cgkDps, "CONFIRMED", 1, 1_000_000, time.Date(2026, 11, 19, 23, 59, 59, 0, time.UTC))
	addBooking(t, pool, cgkKno, "CONFIRMED", 9, 500_000, time.Date(2026, 10, 31, 23, 59, 59, 0, time.UTC))
	addBooking(t, pool, cgkKno, "CONFIRMED", 9, 500_000, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))

	got, err := svc.Analytics(t.Context(), AnalyticsRequest{From: "2026-11-01"})
	require.NoError(t, err)

	require.Equal(t, Analytics{
		From:             "2026-11-01",
		To:               "2026-11-19",
		BookingsByStatus: StatusCounts{Held: 1, Confirmed: 6, Expired: 1, Cancelled: 1},
		Revenue:          5*1_000_000 + 6*500_000 + 700_000,
		TopRoutes: []RouteSummary{
			{OriginCode: "CGK", DestinationCode: "DPS", Bookings: 4, Seats: 5, Revenue: 5_000_000},
			{OriginCode: "CGK", DestinationCode: "KNO", Bookings: 1, Seats: 6, Revenue: 3_000_000},
			{OriginCode: "SUB", DestinationCode: "JOG", Bookings: 1, Seats: 1, Revenue: 700_000},
		},
	}, got)
}

func TestServiceAnalytics_listsAtMostTenRoutes(t *testing.T) {
	svc, pool, now := newTestService(t)
	for i := range maxTopRoutes + 1 {
		addBooking(t, pool, addRoute(t, pool, "CGK", fmt.Sprintf("X%02d", i)), "CONFIRMED", 1, 100, now)
	}

	got, err := svc.Analytics(t.Context(), AnalyticsRequest{})
	require.NoError(t, err)

	require.Len(t, got.TopRoutes, maxTopRoutes)
	require.Equal(t, int64(maxTopRoutes+1), got.BookingsByStatus.Confirmed)
}

func TestServiceAnalytics_reportsAnEmptyPeriodWithZeros(t *testing.T) {
	svc, _, now := newTestService(t)

	got, err := svc.Analytics(t.Context(), AnalyticsRequest{})
	require.NoError(t, err)

	require.Equal(t, Analytics{
		From:      now.AddDate(0, 0, -29).Format(time.DateOnly),
		To:        now.Format(time.DateOnly),
		TopRoutes: []RouteSummary{},
	}, got)
}

var routeFlights int

// addRoute adds a flight on the route to the inventory and returns its id.
func addRoute(t *testing.T, pool *pgxpool.Pool, origin, destination string) string {
	t.Helper()
	routeFlights++
	id := fmt.Sprintf("00000000-0000-4000-8000-%012d", routeFlights)

	_, err := pool.Exec(t.Context(), `
		INSERT INTO flight_inventories (flight_id, version, flight_number, airline_code, origin_code, destination_code,
			departure_at, arrival_at, price, capacity)
		VALUES ($1, 1, 'GA410', 'GA', $2, $3, now() + interval '7 days', now() + interval '7 days 2 hours', 1000000, 180)`,
		id, origin, destination)
	require.NoError(t, err)
	return id
}

// addBooking stores a booking as it would be after its lifecycle, without touching the seat counters.
func addBooking(t *testing.T, pool *pgxpool.Pool, flightID, status string, passengers int32, seatPrice int64, createdAt time.Time) {
	t.Helper()

	_, err := pool.Exec(t.Context(), `
		INSERT INTO bookings (user_id, flight_id, status, passenger_count, seat_price, expires_at, created_at, payment_id)
		VALUES ($1, $2, $3, $4, $5, $6, $6,
			CASE WHEN $3 = 'CONFIRMED' THEN gen_random_uuid() END)`,
		johnID, flightID, status, passengers, seatPrice, createdAt)
	require.NoError(t, err)
}
