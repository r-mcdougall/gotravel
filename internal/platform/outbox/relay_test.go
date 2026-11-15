package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/dbtest"
	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
)

// Same shape as the outbox migration of every service.
const outboxDDL = `CREATE TABLE outbox (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type   TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);
CREATE INDEX outbox_unpublished_idx ON outbox (created_at, id) WHERE published_at IS NULL;`

var (
	databaseOnce      sync.Once
	databaseURLValue  string
	databaseErr       error
	terminateDatabase func()
)

func TestMain(m *testing.M) {
	code := m.Run()
	if terminateDatabase != nil {
		terminateDatabase()
	}
	os.Exit(code)
}

type fakePublisher struct {
	mu        sync.Mutex
	err       error
	published [][]messaging.Message
}

func (f *fakePublisher) Publish(_ context.Context, msgs []messaging.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return f.err
	}
	f.published = append(f.published, msgs)
	return nil
}

func (f *fakePublisher) batches() [][]messaging.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.published
}

func TestRelayRun_returnsWhenTheContextIsCanceled(t *testing.T) {
	pool, err := pgxpool.New(t.Context(), "postgres://test:test@127.0.0.1:1/test?connect_timeout=1")
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	relay := NewRelay(pool, &fakePublisher{}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the context was canceled")
	}
}

func TestRelayRelayBatch_publishesEnvelopesInCreationOrderAndMarksThemPublished(t *testing.T) {
	pool := newTestPool(t)
	publisher := &fakePublisher{}
	relay := NewRelay(pool, publisher, slog.New(slog.DiscardHandler))

	first := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	secondID := insertEvent(t, pool, `{"flight_number":"GA403"}`, first.Add(time.Second))
	firstID := insertEvent(t, pool, `{"flight_number":"GA402"}`, first)

	n, err := relay.relayBatch(t.Context())
	require.NoError(t, err)
	require.Equal(t, 2, n)

	batches := publisher.batches()
	require.Len(t, batches, 1)
	require.Len(t, batches[0], 2)
	require.Equal(t, firstID, batches[0][0].ID)
	require.Equal(t, secondID, batches[0][1].ID)

	msg := batches[0][0]
	require.Equal(t, "flight.changed", msg.Type)
	require.True(t, first.Equal(msg.Timestamp))

	var got envelope
	require.NoError(t, json.Unmarshal(msg.Body, &got))
	require.Equal(t, firstID, got.EventID)
	require.Equal(t, "flight.changed", got.Type)
	require.True(t, first.Equal(got.OccurredAt))
	require.JSONEq(t, `{"flight_number":"GA402"}`, string(got.Data))

	require.Zero(t, unpublishedCount(t, pool))

	n, err = relay.relayBatch(t.Context())
	require.NoError(t, err)
	require.Zero(t, n)
	require.Len(t, publisher.batches(), 1)
}

func TestRelayRelayBatch_keepsEventsUnpublishedWhenPublishingFails(t *testing.T) {
	pool := newTestPool(t)
	relay := NewRelay(pool, &fakePublisher{err: errors.New("broker unavailable")}, slog.New(slog.DiscardHandler))
	insertEvent(t, pool, `{}`, time.Now())

	_, err := relay.relayBatch(t.Context())

	require.ErrorContains(t, err, "broker unavailable")
	require.Equal(t, 1, unpublishedCount(t, pool))
}

func TestRelayRelayBatch_publishesAtMostOneBatchPerCall(t *testing.T) {
	pool := newTestPool(t)
	relay := NewRelay(pool, &fakePublisher{}, slog.New(slog.DiscardHandler))
	_, err := pool.Exec(t.Context(),
		`INSERT INTO outbox (event_type, payload) SELECT 'flight.changed', '{}' FROM generate_series(1, $1)`, batchSize+1)
	require.NoError(t, err)

	n, err := relay.relayBatch(t.Context())
	require.NoError(t, err)
	require.Equal(t, batchSize, n)

	n, err = relay.relayBatch(t.Context())
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestRelayRelayBatch_skipsEventsLockedByAnotherRelay(t *testing.T) {
	pool := newTestPool(t)
	publisher := &fakePublisher{}
	relay := NewRelay(pool, publisher, slog.New(slog.DiscardHandler))
	now := time.Now()
	lockedID := insertEvent(t, pool, `{}`, now)
	freeID := insertEvent(t, pool, `{}`, now.Add(time.Second))

	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	_, err = tx.Exec(t.Context(), `SELECT id FROM outbox WHERE id = $1 FOR UPDATE`, lockedID)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	n, err := relay.relayBatch(ctx)

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, freeID, publisher.batches()[0][0].ID)
}

func TestRelayRun_relaysEventsAsTheyArrive(t *testing.T) {
	pool := newTestPool(t)
	relay := NewRelay(pool, &fakePublisher{}, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		relay.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	insertEvent(t, pool, `{}`, time.Now())

	require.Eventually(t, func() bool {
		var count int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&count)
		return err == nil && count == 0
	}, 5*time.Second, 50*time.Millisecond)
}

func databaseURL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	databaseOnce.Do(func() {
		databaseURLValue, terminateDatabase, databaseErr = dbtest.Start(context.Background())
	})
	require.NoError(t, databaseErr)
	return databaseURLValue
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := pgxpool.New(context.Background(), databaseURL(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	_, err = pool.Exec(t.Context(), "DROP TABLE IF EXISTS outbox;\n"+outboxDDL)
	require.NoError(t, err)
	return pool
}

func insertEvent(t *testing.T, pool *pgxpool.Pool, payload string, createdAt time.Time) string {
	t.Helper()

	var id string
	err := pool.QueryRow(t.Context(),
		`INSERT INTO outbox (event_type, payload, created_at) VALUES ('flight.changed', $1, $2) RETURNING id`,
		json.RawMessage(payload), createdAt,
	).Scan(&id)
	require.NoError(t, err)
	return id
}

func unpublishedCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()

	var count int
	err := pool.QueryRow(t.Context(), `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&count)
	require.NoError(t, err)
	return count
}
