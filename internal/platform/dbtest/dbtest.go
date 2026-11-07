package dbtest

import (
	"context"
	"fmt"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

const image = "postgres:17.11-alpine"

func Start(ctx context.Context) (url string, terminate func(), err error) {
	ctr, err := postgres.Run(ctx, image,
		postgres.WithDatabase("test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		postgres.BasicWaitStrategies(),
	)
	terminate = func() { _ = testcontainers.TerminateContainer(ctr) }
	if err != nil {
		terminate()
		return "", nil, fmt.Errorf("start postgres container: %w", err)
	}

	url, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		terminate()
		return "", nil, fmt.Errorf("get connection string: %w", err)
	}
	return url, terminate, nil
}
