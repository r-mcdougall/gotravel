package booking

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/r-mcdougall/gotravel/services/booking/internal/store"
)

type PgStore struct {
	*store.Queries
	pool *pgxpool.Pool
}

var _ bookingStore = (*PgStore)(nil)

func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{
		Queries: store.New(pool),
		pool:    pool,
	}
}

func (s *PgStore) InTx(ctx context.Context, fn func(queries) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()

	if err = fn(store.New(tx)); err != nil {
		return err
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}

	return nil
}
