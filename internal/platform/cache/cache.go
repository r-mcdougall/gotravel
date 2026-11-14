package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const operationTimeout = 100 * time.Millisecond

type Client struct {
	rdb *redis.Client
}

func New(redisURL string) (*Client, error) {
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("parse redis url: %w", err)
	}

	options.DialTimeout = operationTimeout
	options.ReadTimeout = operationTimeout
	options.WriteTimeout = operationTimeout
	options.MaxRetries = -1

	return &Client{rdb: redis.NewClient(options)}, nil
}

func (c *Client) Get(ctx context.Context, key string) (value []byte, found bool, err error) {
	value, err = c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, false, nil
		}

		return nil, false, fmt.Errorf("get %s: %w", key, err)
	}

	return value, true, nil
}

func (c *Client) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	if ttl <= 0 {
		return fmt.Errorf("set %q: ttl must be positive", key)
	}

	if err := c.rdb.Set(ctx, key, value, ttl).Err(); err != nil {
		return fmt.Errorf("set %s: %w", key, err)
	}

	return nil
}

func (c *Client) Delete(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}

	if err := c.rdb.Del(ctx, keys...).Err(); err != nil {
		return fmt.Errorf("delete %d keys: %w", len(keys), err)
	}

	return nil
}

func (c *Client) Close() error {
	return c.rdb.Close()
}
