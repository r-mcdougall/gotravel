package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/config"
)

func TestLoadBase_defaults(t *testing.T) {
	t.Setenv("HTTP_ADDR", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("SHUTDOWN_TIMEOUT", "")

	var l config.Loader
	got := config.LoadBase(&l)

	require.NoError(t, l.Err())
	require.Equal(t, config.Base{
		HTTPAddr:        ":8080",
		LogLevel:        "info",
		ShutdownTimeout: 10 * time.Second,
	}, got)
}

func TestLoadBase_overrides(t *testing.T) {
	t.Setenv("HTTP_ADDR", ":9090")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("SHUTDOWN_TIMEOUT", "3s")

	var l config.Loader
	got := config.LoadBase(&l)

	require.NoError(t, l.Err())
	require.Equal(t, config.Base{
		HTTPAddr:        ":9090",
		LogLevel:        "debug",
		ShutdownTimeout: 3 * time.Second,
	}, got)
}

func TestLoader_reportsEveryProblemAtOnce(t *testing.T) {
	t.Setenv("DB_URL", "")
	t.Setenv("JWT_PUBLIC_KEY", "")
	t.Setenv("TOKEN_TTL", "soon")

	var l config.Loader
	l.Required("DB_URL")
	l.Required("JWT_PUBLIC_KEY")
	ttl := l.Duration("TOKEN_TTL", time.Minute)

	err := l.Err()
	require.Error(t, err)
	require.ErrorContains(t, err, "DB_URL is required")
	require.ErrorContains(t, err, "JWT_PUBLIC_KEY is required")
	require.ErrorContains(t, err, `TOKEN_TTL: invalid duration "soon"`)
	require.Equal(t, time.Minute, ttl)
}

func TestLoader_requiredReturnsValue(t *testing.T) {
	t.Setenv("DB_URL", "postgres://x")

	var l config.Loader
	got := l.Required("DB_URL")

	require.NoError(t, l.Err())
	require.Equal(t, "postgres://x", got)
}
