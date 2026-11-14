package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

// failingOutboxStore is a PgStore whose outbox inserts fail inside transactions.
type failingOutboxStore struct {
	*PgStore
}

func (s failingOutboxStore) InTx(ctx context.Context, fn func(queries) error) error {
	return s.PgStore.InTx(ctx, func(q queries) error {
		return fn(failingOutbox{queries: q})
	})
}

type failingOutbox struct {
	queries
}

func (failingOutbox) InsertOutboxEvent(context.Context, store.InsertOutboxEventParams) error {
	return errBoom
}

func flightIDs(response AdminFlightListResponse) []string {
	ids := make([]string, len(response.Data))
	for i, flight := range response.Data {
		ids[i] = flight.ID
	}
	return ids
}

func TestPgStoreInTx_commitsWhatTheCallbackWrote(t *testing.T) {
	e := newEnv(t)

	err := e.pgStore.InTx(t.Context(), func(q queries) error {
		return q.InsertOutboxEvent(t.Context(), store.InsertOutboxEventParams{EventType: "test.event", Payload: []byte(`{}`)})
	})

	require.NoError(t, err)
	require.Equal(t, 1, e.countOutbox(t))
}

func TestPgStoreInTx_rollsBackWhenTheCallbackFails(t *testing.T) {
	e := newEnv(t)

	err := e.pgStore.InTx(t.Context(), func(q queries) error {
		if err := q.InsertOutboxEvent(t.Context(), store.InsertOutboxEventParams{EventType: "test.event", Payload: []byte(`{}`)}); err != nil {
			return err
		}
		return errBoom
	})

	require.ErrorIs(t, err, errBoom)
	require.Equal(t, 0, e.countOutbox(t))
}

func TestLoadReference_loadsEveryMigratedAirlineAndAirport(t *testing.T) {
	e := newEnv(t)

	for _, code := range []string{"GA", "QG", "JT", "ID", "IU", "QZ", "SQ", "NH"} {
		_, ok := e.reference.Airline(code)
		require.True(t, ok, code)
	}

	offsetHours := map[string]int{
		"CGK": 7, "SUB": 7, "KNO": 7, "YIA": 7,
		"DPS": 8, "UPG": 8, "BPN": 8, "SIN": 8, "KUL": 8,
		"DJJ": 9, "HND": 9,
	}
	for code, hours := range offsetHours {
		airport, ok := e.reference.Airport(code)
		require.True(t, ok, code)
		require.Equal(t, hours*60*60, utcOffsetSeconds(t, airport.Location), code)
	}
}

func TestFlightsInPostgres_createStoresTheFlightAndExactlyOneOutboxEvent(t *testing.T) {
	e := newEnv(t)

	created := e.createFlight(t, nil)

	require.True(t, validUUID(created.ID))
	require.True(t, validCreateRequest().DepartureAt.Equal(created.DepartureAt))
	require.Equal(t, int32(180), created.AvailableSeats)

	fetched, err := e.service.GetFlight(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, toJSON(t, created), toJSON(t, fetched))

	events := e.outboxEvents(t)
	require.Len(t, events, 1)
	require.Equal(t, created.ID, events[0].FlightID)
	require.Equal(t, int32(1), events[0].Version)
	require.Equal(t, int32(180), events[0].Capacity)
}

func TestFlightsInPostgres_createRejectsADuplicateWithoutWritingAnotherEvent(t *testing.T) {
	e := newEnv(t)
	e.createFlight(t, nil)

	_, err := e.service.CreateFlight(t.Context(), validCreateRequest())

	require.ErrorIs(t, err, ErrFlightExists)
	require.Equal(t, 1, e.countFlights(t))
	require.Equal(t, 1, e.countOutbox(t))
}

func TestFlightsInPostgres_createRollsBackTheFlightWhenTheOutboxInsertFails(t *testing.T) {
	e := newEnv(t)
	svc := NewService(failingOutboxStore{PgStore: e.pgStore}, e.reference, nil)

	_, err := svc.CreateFlight(t.Context(), validCreateRequest())

	require.ErrorIs(t, err, errBoom)
	require.Equal(t, 0, e.countFlights(t))
	require.Equal(t, 0, e.countOutbox(t))
}

func TestFlightsInPostgres_updateRollsBackTheChangeWhenTheOutboxInsertFails(t *testing.T) {
	e := newEnv(t)
	created := e.createFlight(t, nil)
	svc := NewService(failingOutboxStore{PgStore: e.pgStore}, e.reference, nil)

	_, err := svc.UpdateFlight(t.Context(), created.ID, UpdateFlightRequest{Price: ptr(int64(900_000))})

	require.ErrorIs(t, err, errBoom)
	require.Equal(t, flightRow{Version: 1, Capacity: 180, AvailableSeats: 180}, e.flightRow(t, created.ID))
	fetched, err := e.service.GetFlight(t.Context(), created.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1_250_000), fetched.Price)
}

func TestFlightsInPostgres_updateBumpsTheVersionAndRecordsASecondEvent(t *testing.T) {
	e := newEnv(t)
	created := e.createFlight(t, nil)

	updated, err := e.service.UpdateFlight(t.Context(), created.ID, UpdateFlightRequest{Price: ptr(int64(900_000))})
	require.NoError(t, err)

	require.Equal(t, int64(900_000), updated.Price)
	require.True(t, updated.UpdatedAt.After(created.UpdatedAt))
	require.Equal(t, flightRow{Version: 2, Capacity: 180, AvailableSeats: 180}, e.flightRow(t, created.ID))

	events := e.outboxEvents(t)
	require.Len(t, events, 2)
	require.Equal(t, int32(2), events[1].Version)
	require.Equal(t, int64(900_000), events[1].Price)
}

func TestFlightsInPostgres_updateKeepsSoldSeatsSoldWhenCapacityChanges(t *testing.T) {
	e := newEnv(t)
	created := e.createFlight(t, nil)
	e.setAvailableSeats(t, created.ID, 42) // 138 of 180 seats sold

	updated, err := e.service.UpdateFlight(t.Context(), created.ID, UpdateFlightRequest{Capacity: ptr(int32(150))})
	require.NoError(t, err)

	require.Equal(t, int32(12), updated.AvailableSeats)
	require.Equal(t, flightRow{Version: 2, Capacity: 150, AvailableSeats: 12}, e.flightRow(t, created.ID))
}

func TestFlightsInPostgres_updateRejectsCapacityBelowTheSoldSeatsAndChangesNothing(t *testing.T) {
	e := newEnv(t)
	created := e.createFlight(t, nil)
	e.setAvailableSeats(t, created.ID, 42) // 138 of 180 seats sold

	_, err := e.service.UpdateFlight(t.Context(), created.ID, UpdateFlightRequest{Capacity: ptr(int32(137))})

	require.ErrorIs(t, err, ErrCapacityBelowSold)
	require.Equal(t, flightRow{Version: 1, Capacity: 180, AvailableSeats: 42}, e.flightRow(t, created.ID))
	require.Equal(t, 1, e.countOutbox(t))
}

func TestFlightsInPostgres_updateReportsAScheduleThatCollidesWithAnotherFlight(t *testing.T) {
	e := newEnv(t)
	first := e.createFlight(t, nil)
	second := e.createFlight(t, func(r *CreateFlightRequest) {
		r.DepartureAt = time.Date(2026, 11, 21, 6, 0, 0, 0, wib)
		r.ArrivalAt = time.Date(2026, 11, 21, 8, 55, 0, 0, wita)
	})

	_, err := e.service.UpdateFlight(t.Context(), second.ID, UpdateFlightRequest{
		DepartureAt: &first.DepartureAt,
		ArrivalAt:   &first.ArrivalAt,
	})

	require.ErrorIs(t, err, ErrFlightExists)
	require.Equal(t, int32(1), e.flightRow(t, second.ID).Version)
	require.Equal(t, 2, e.countOutbox(t))
}

func TestFlightsInPostgres_getReportsAMissingFlight(t *testing.T) {
	e := newEnv(t)

	_, err := e.service.GetFlight(t.Context(), "00000000-0000-4000-8000-000000000000")

	require.ErrorIs(t, err, ErrFlightNotFound)
}

func TestFlightsInPostgres_listFiltersByRouteAndByTheLocalDateAtTheOrigin(t *testing.T) {
	e := newEnv(t)
	// 01:00 on the 21st in Jakarta is still the 20th in UTC.
	jakartaPastMidnight := e.createFlight(t, func(r *CreateFlightRequest) {
		r.FlightNumber = "GA412"
		r.DepartureAt = time.Date(2026, 11, 21, 1, 0, 0, 0, wib)
		r.ArrivalAt = time.Date(2026, 11, 21, 3, 55, 0, 0, wita)
	})
	jakartaMorning := e.createFlight(t, func(r *CreateFlightRequest) {
		r.FlightNumber = "GA414"
		r.DepartureAt = time.Date(2026, 11, 20, 10, 0, 0, 0, wib)
		r.ArrivalAt = time.Date(2026, 11, 20, 12, 55, 0, 0, wita)
	})
	denpasarMorning := e.createFlight(t, func(r *CreateFlightRequest) {
		r.FlightNumber = "GA411"
		r.OriginCode = "DPS"
		r.DestinationCode = "CGK"
		r.DepartureAt = time.Date(2026, 11, 20, 10, 0, 0, 0, wita)
		r.ArrivalAt = time.Date(2026, 11, 20, 10, 55, 0, 0, wib)
	})
	day20 := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	day21 := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		request ListFlightsRequest
		want    []string
		total   int64
	}{
		{"no filters, latest departure first", ListFlightsRequest{Page: 1, PageSize: 10},
			[]string{jakartaPastMidnight.ID, jakartaMorning.ID, denpasarMorning.ID}, 3},
		{"origin and local date", ListFlightsRequest{Origin: "CGK", Date: &day20, Page: 1, PageSize: 10},
			[]string{jakartaMorning.ID}, 1},
		{"local date after midnight in Jakarta", ListFlightsRequest{Origin: "CGK", Date: &day21, Page: 1, PageSize: 10},
			[]string{jakartaPastMidnight.ID}, 1},
		{"date only uses each flight's own origin zone", ListFlightsRequest{Date: &day20, Page: 1, PageSize: 10},
			[]string{jakartaMorning.ID, denpasarMorning.ID}, 2},
		{"destination", ListFlightsRequest{Destination: "CGK", Page: 1, PageSize: 10},
			[]string{denpasarMorning.ID}, 1},
		{"first page", ListFlightsRequest{Page: 1, PageSize: 2},
			[]string{jakartaPastMidnight.ID, jakartaMorning.ID}, 3},
		{"second page", ListFlightsRequest{Page: 2, PageSize: 2},
			[]string{denpasarMorning.ID}, 3},
		{"no match", ListFlightsRequest{Origin: "SIN", Page: 1, PageSize: 10},
			[]string{}, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			response, err := e.service.ListFlights(t.Context(), tc.request)
			require.NoError(t, err)

			require.Equal(t, tc.want, flightIDs(response))
			require.Equal(t, tc.total, response.Meta.Total)
			require.Equal(t, tc.request.Page, response.Meta.Page)
			require.Equal(t, tc.request.PageSize, response.Meta.PageSize)
		})
	}
}

func TestFlightsInPostgres_searchFindsFlightsByTheLocalDateAtTheOrigin(t *testing.T) {
	e := newEnv(t)
	// 01:00 on the 21st in Jakarta is still the 20th in UTC: it belongs to the 21st.
	jakartaPastMidnight := e.createFlight(t, func(r *CreateFlightRequest) {
		r.FlightNumber = "GA412"
		r.DepartureAt = time.Date(2026, 11, 21, 1, 0, 0, 0, wib)
		r.ArrivalAt = time.Date(2026, 11, 21, 3, 55, 0, 0, wita)
	})
	jakartaLateEvening := e.createFlight(t, func(r *CreateFlightRequest) {
		r.FlightNumber = "GA416"
		r.DepartureAt = time.Date(2026, 11, 20, 23, 30, 0, 0, wib)
		r.ArrivalAt = time.Date(2026, 11, 21, 2, 25, 0, 0, wita)
	})
	jakartaMorning := e.createFlight(t, func(r *CreateFlightRequest) {
		r.FlightNumber = "GA414"
		r.DepartureAt = time.Date(2026, 11, 20, 10, 0, 0, 0, wib)
		r.ArrivalAt = time.Date(2026, 11, 20, 12, 55, 0, 0, wita)
		r.Price = 900_000
	})
	e.createFlight(t, func(r *CreateFlightRequest) {
		r.FlightNumber = "GA411"
		r.OriginCode = "DPS"
		r.DestinationCode = "CGK"
		r.DepartureAt = time.Date(2026, 11, 20, 10, 0, 0, 0, wita)
		r.ArrivalAt = time.Date(2026, 11, 20, 10, 55, 0, 0, wib)
	})
	// The clock is pinned so the test keeps working once these dates are in the past.
	service := newService(e.pgStore, e.reference, nil, func() time.Time {
		return time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	})

	search := func(day int, mutate func(r *SearchFlightsRequest)) FlightListResponse {
		request := validSearchRequest()
		request.Date = time.Date(2026, 11, day, 0, 0, 0, 0, time.UTC)
		if mutate != nil {
			mutate(&request)
		}
		response, err := service.SearchFlights(t.Context(), request)
		require.NoError(t, err)
		return response
	}

	t.Run("the 20th holds the morning and late evening flights", func(t *testing.T) {
		response := search(20, nil)

		require.Equal(t, []string{jakartaMorning.ID, jakartaLateEvening.ID}, responseIDs(response))
		require.Equal(t, int64(2), response.Meta.Total)
	})
	t.Run("the 21st holds the flight that leaves after local midnight", func(t *testing.T) {
		response := search(21, nil)

		require.Equal(t, []string{jakartaPastMidnight.ID}, responseIDs(response))
	})
	t.Run("filters and sorting run on the day's flights", func(t *testing.T) {
		response := search(20, func(r *SearchFlightsRequest) {
			r.MaxPrice = ptr(int64(1_000_000))
			r.Sort = sortPriceAsc
		})

		require.Equal(t, []string{jakartaMorning.ID}, responseIDs(response))
	})
	t.Run("the reverse route has no flights on the 21st", func(t *testing.T) {
		response := search(21, func(r *SearchFlightsRequest) {
			r.Origin, r.Destination = "DPS", "CGK"
		})

		require.Empty(t, response.Data)
	})
}

func TestFlightsInPostgres_searchHidesFlightsThatHaveDepartedButDetailStillShowsThem(t *testing.T) {
	e := newEnv(t)
	flight := e.createFlight(t, nil) // departs 2026-11-20 06:00 WIB
	afterDeparture := func() time.Time { return time.Date(2026, 11, 20, 6, 0, 0, 0, wib) }
	service := newService(e.pgStore, e.reference, nil, afterDeparture)

	response, err := service.SearchFlights(t.Context(), validSearchRequest())
	require.NoError(t, err)
	require.Empty(t, response.Data)

	detail, err := service.GetFlightDetail(t.Context(), flight.ID)
	require.NoError(t, err)
	require.Equal(t, flight.ID, detail.ID)
}
