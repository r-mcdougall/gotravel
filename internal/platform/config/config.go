package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

type Loader struct {
	errs []error
}

func (l *Loader) String(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func (l *Loader) Required(key string) string {
	value := os.Getenv(key)
	if value == "" {
		l.errs = append(l.errs, fmt.Errorf("%s is required", key))
	}
	return value
}

func (l *Loader) Duration(key string, fallback time.Duration) time.Duration {
	raw := os.Getenv(key)
	if raw == "" {
		return fallback
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("%s: invalid duration %q", key, raw))
		return fallback
	}

	return d
}

func (l *Loader) Err() error {
	return errors.Join(l.errs...)
}

type Base struct {
	HTTPAddr        string
	LogLevel        string
	ShutdownTimeout time.Duration
}

func LoadBase(l *Loader) Base {
	return Base{
		HTTPAddr:        l.String("HTTP_ADDR", ":8080"),
		LogLevel:        l.String("LOG_LEVEL", "info"),
		ShutdownTimeout: l.Duration("SHUTDOWN_TIMEOUT", 10*time.Second),
	}
}
