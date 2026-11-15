package outbox

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/r-mcdougall/gotravel/internal/platform/messaging"
)

const (
	pollInterval = 500 * time.Millisecond
	batchSize    = 100
	maxBackoff   = 30 * time.Second
)

// The relay expects every service to own an outbox table with this shape:
// id uuid, event_type text, payload jsonb, created_at timestamptz, published_at timestamptz null.
const (
	selectBatchSQL = `SELECT id, event_type, payload, created_at
FROM outbox
WHERE published_at IS NULL
ORDER BY created_at, id
LIMIT $1
FOR UPDATE SKIP LOCKED`

	markPublishedSQL = `UPDATE outbox SET published_at = now() WHERE id = ANY($1::uuid[])`
)

type Publisher interface {
	Publish(ctx context.Context, msgs []messaging.Message) error
}

type Relay struct {
	pool      *pgxpool.Pool
	publisher Publisher
	logger    *slog.Logger
}

type envelope struct {
	EventID    string          `json:"event_id"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

func NewRelay(pool *pgxpool.Pool, publisher Publisher, logger *slog.Logger) *Relay {
	return &Relay{pool: pool, publisher: publisher, logger: logger}
}

// Run relays outbox rows until ctx is canceled. A full batch is followed
// immediately by the next one; failures back off up to maxBackoff.
func (r *Relay) Run(ctx context.Context) {
	wait := pollInterval

	for {
		n, err := r.relayBatch(ctx)
		if ctx.Err() != nil {
			return
		}

		if err != nil {
			r.logger.Warn("outbox relay failed", "error", err, "retry_in", wait)
			if !sleep(ctx, wait) {
				return
			}
			wait = min(wait*2, maxBackoff)
			continue
		}

		wait = pollInterval
		if n == batchSize {
			continue
		}
		if !sleep(ctx, pollInterval) {
			return
		}
	}
}

func (r *Relay) relayBatch(ctx context.Context) (int, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	msgs, ids, err := selectBatch(ctx, tx)
	if err != nil {
		return 0, err
	}
	if len(msgs) == 0 {
		return 0, nil
	}

	if err := r.publisher.Publish(ctx, msgs); err != nil {
		return 0, fmt.Errorf("publish %d outbox events: %w", len(msgs), err)
	}
	if _, err := tx.Exec(ctx, markPublishedSQL, ids); err != nil {
		return 0, fmt.Errorf("mark outbox events published: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit transaction: %w", err)
	}

	return len(msgs), nil
}

func selectBatch(ctx context.Context, tx pgx.Tx) ([]messaging.Message, []string, error) {
	rows, err := tx.Query(ctx, selectBatchSQL, batchSize)
	if err != nil {
		return nil, nil, fmt.Errorf("select outbox events: %w", err)
	}
	defer rows.Close()

	var (
		msgs []messaging.Message
		ids  []string
	)
	for rows.Next() {
		var e envelope
		if err := rows.Scan(&e.EventID, &e.Type, &e.Data, &e.OccurredAt); err != nil {
			return nil, nil, fmt.Errorf("scan outbox event: %w", err)
		}
		e.OccurredAt = e.OccurredAt.UTC()

		body, err := json.Marshal(e)
		if err != nil {
			return nil, nil, fmt.Errorf("encode outbox event %s: %w", e.EventID, err)
		}

		msgs = append(msgs, messaging.Message{ID: e.EventID, Type: e.Type, Timestamp: e.OccurredAt, Body: body})
		ids = append(ids, e.EventID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("read outbox events: %w", err)
	}

	return msgs, ids, nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
