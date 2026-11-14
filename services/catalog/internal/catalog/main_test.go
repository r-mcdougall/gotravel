package catalog

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/database"
	"github.com/r-mcdougall/gotravel/internal/platform/dbtest"
	"github.com/r-mcdougall/gotravel/services/catalog/migrations"
)

// The PostgreSQL container starts on the first integration test that needs it,
// so unit tests run without Docker. `go test -short` skips integration tests.
var integrationDB struct {
	once      sync.Once
	url       string
	err       error
	terminate func()
}

func TestMain(m *testing.M) {
	code := m.Run()
	if integrationDB.terminate != nil {
		integrationDB.terminate()
	}
	os.Exit(code)
}

func databaseURL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}

	integrationDB.once.Do(func() {
		ctx := context.Background()
		url, terminate, err := dbtest.Start(ctx)
		if err != nil {
			integrationDB.err = err
			return
		}
		integrationDB.terminate = terminate

		if err := database.Migrate(ctx, slog.New(slog.DiscardHandler), url, migrations.FS); err != nil {
			integrationDB.err = err
			return
		}
		integrationDB.url = url
	})
	require.NoError(t, integrationDB.err)
	return integrationDB.url
}

type env struct {
	service   Service
	pgStore   *PgStore
	pool      *pgxpool.Pool
	reference *Reference
}

// newEnv connects to the shared database and empties the tables the tests
// write to. Airlines and airports hold migrated reference data and stay.
func newEnv(t *testing.T) *env {
	t.Helper()

	pool, err := database.Connect(t.Context(), databaseURL(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(t.Context(), "TRUNCATE flights, outbox")
	require.NoError(t, err)

	pgStore := NewPgStore(pool)
	reference, err := LoadReference(t.Context(), pgStore)
	require.NoError(t, err)

	return &env{
		service:   NewService(pgStore, reference, nil),
		pgStore:   pgStore,
		pool:      pool,
		reference: reference,
	}
}

type flightRow struct {
	Version        int32
	Capacity       int32
	AvailableSeats int32
}

func (e *env) flightRow(t *testing.T, id string) flightRow {
	t.Helper()
	var row flightRow
	err := e.pool.QueryRow(t.Context(),
		"SELECT version, capacity, available_seats FROM flights WHERE id = $1", id,
	).Scan(&row.Version, &row.Capacity, &row.AvailableSeats)
	require.NoError(t, err)
	return row
}

// setAvailableSeats stands in for the booking service holding seats.
func (e *env) setAvailableSeats(t *testing.T, id string, seats int32) {
	t.Helper()
	_, err := e.pool.Exec(t.Context(), "UPDATE flights SET available_seats = $1 WHERE id = $2", seats, id)
	require.NoError(t, err)
}

func (e *env) countFlights(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, e.pool.QueryRow(t.Context(), "SELECT count(*) FROM flights").Scan(&n))
	return n
}

func (e *env) countOutbox(t *testing.T) int {
	t.Helper()
	var n int
	require.NoError(t, e.pool.QueryRow(t.Context(), "SELECT count(*) FROM outbox").Scan(&n))
	return n
}

// outboxEvents returns the payload of every outbox row, oldest first.
func (e *env) outboxEvents(t *testing.T) []flightChangedData {
	t.Helper()

	rows, err := e.pool.Query(t.Context(), "SELECT event_type, payload FROM outbox ORDER BY created_at, id")
	require.NoError(t, err)
	defer rows.Close()

	var events []flightChangedData
	for rows.Next() {
		var (
			eventType string
			payload   []byte
		)
		require.NoError(t, rows.Scan(&eventType, &payload))
		require.Equal(t, eventFlightChanged, eventType)

		var data flightChangedData
		require.NoError(t, json.Unmarshal(payload, &data))
		events = append(events, data)
	}
	require.NoError(t, rows.Err())
	return events
}

// createFlight creates validCreateRequest after applying mutate, if any.
func (e *env) createFlight(t *testing.T, mutate func(r *CreateFlightRequest)) AdminFlightResponse {
	t.Helper()

	request := validCreateRequest()
	if mutate != nil {
		mutate(&request)
	}
	response, err := e.service.CreateFlight(t.Context(), request)
	require.NoError(t, err)
	return response
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

func ptr[T any](v T) *T {
	return &v
}

func invalidFields(t *testing.T, err error) []string {
	t.Helper()

	var validationErr *ValidationError
	require.ErrorAs(t, err, &validationErr)

	fields := make([]string, len(validationErr.Details))
	for i, detail := range validationErr.Details {
		fields[i] = detail.Field
	}
	return fields
}
