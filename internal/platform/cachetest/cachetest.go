package cachetest

import (
	"context"
	"fmt"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/redis"
)

const image = "redis:8.10.3-alpine"

// Start runs a throwaway Redis and returns its redis:// URL.
func Start(ctx context.Context) (url string, terminate func(), err error) {
	ctr, err := redis.Run(ctx, image)
	terminate = func() { _ = testcontainers.TerminateContainer(ctr) }
	if err != nil {
		terminate()
		return "", nil, fmt.Errorf("start redis container: %w", err)
	}

	url, err = ctr.ConnectionString(ctx)
	if err != nil {
		terminate()
		return "", nil, fmt.Errorf("get connection string: %w", err)
	}
	return url, terminate, nil
}
