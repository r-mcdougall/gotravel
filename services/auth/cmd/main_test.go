package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/authn"
	"github.com/r-mcdougall/gotravel/internal/platform/dbtest"
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	url, terminate, err := dbtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer terminate()

	keyDir, err := os.MkdirTemp("", "auth-test-keys")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(keyDir) }()

	privatePEM, _, err := authn.GenerateKeyPair()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	keyFile := filepath.Join(keyDir, "jwt_private.pem")
	if err := os.WriteFile(keyFile, privatePEM, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	for key, value := range map[string]string{"DATABASE_URL": url, "JWT_PRIVATE_KEY_FILE": keyFile} {
		if err := os.Setenv(key, value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return m.Run()
}

func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

func fetch(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		return -1, ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1, ""
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func postJSON(t *testing.T, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	require.NoError(t, err)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(respBody)
}

// startService runs the service on a free port and waits until it is healthy.
// stop shuts it down and returns what run returned.
func startService(t *testing.T) (addr string, stop func() error) {
	t.Helper()
	addr = freeAddr(t)
	t.Setenv("HTTP_ADDR", addr)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- run(ctx, nil, io.Discard) }()

	require.Eventually(t, func() bool {
		code, _ := fetch(t, "http://"+addr+"/healthz")
		return code == http.StatusOK
	}, 10*time.Second, 10*time.Millisecond)

	return addr, func() error {
		cancel()
		return <-done
	}
}

func TestRun_servesOperationalEndpoints(t *testing.T) {
	addr, stop := startService(t)

	code, body := fetch(t, "http://"+addr+"/healthz")
	require.Equal(t, http.StatusOK, code)
	require.JSONEq(t, `{"status":"ok"}`, body)

	code, body = fetch(t, "http://"+addr+"/metrics")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, body, "go_goroutines")

	code, body = fetch(t, "http://"+addr+"/nope")
	require.Equal(t, http.StatusNotFound, code)
	require.JSONEq(t, `{"error":{"code":"NOT_FOUND","message":"route not found"}}`, body)

	require.NoError(t, stop())
}

func TestRun_healthcheckSucceedsWhileServiceRuns(t *testing.T) {
	_, stop := startService(t)

	require.NoError(t, run(t.Context(), []string{"-healthcheck"}, io.Discard))

	require.NoError(t, stop())
}

func TestRun_healthcheckFailsWhenServiceIsDown(t *testing.T) {
	t.Setenv("HTTP_ADDR", freeAddr(t))

	require.Error(t, run(t.Context(), []string{"-healthcheck"}, io.Discard))
}

func TestRun_healthcheckIgnoresUnrelatedConfigProblems(t *testing.T) {
	_, stop := startService(t)
	t.Setenv("SHUTDOWN_TIMEOUT", "soon")

	require.NoError(t, run(t.Context(), []string{"-healthcheck"}, io.Discard))

	require.NoError(t, stop())
}

func TestRun_failsOnInvalidConfig(t *testing.T) {
	t.Setenv("SHUTDOWN_TIMEOUT", "soon")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "SHUTDOWN_TIMEOUT")
}

func TestRun_appliesMigrationsOnStart(t *testing.T) {
	_, stop := startService(t)

	conn, err := pgx.Connect(t.Context(), os.Getenv("DATABASE_URL"))
	require.NoError(t, err)
	defer func() { _ = conn.Close(t.Context()) }()

	var tables int
	err = conn.QueryRow(t.Context(),
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name IN ('users', 'refresh_tokens')`,
	).Scan(&tables)
	require.NoError(t, err)
	require.Equal(t, 2, tables)

	require.NoError(t, stop())
}

func TestRun_failsWithoutDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "DATABASE_URL is required")
}

func TestRun_failsWhenDatabaseIsUnreachable(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://auth_svc:x@127.0.0.1:1/auth")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "apply migrations")
}

func TestRun_failsOnInvalidLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "loud")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "invalid log level")
}

func TestRun_failsWithoutJWTPrivateKeyFile(t *testing.T) {
	t.Setenv("JWT_PRIVATE_KEY_FILE", "")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "JWT_PRIVATE_KEY_FILE is required")
}

func TestRun_failsWhenJWTPrivateKeyFileIsMissing(t *testing.T) {
	t.Setenv("JWT_PRIVATE_KEY_FILE", filepath.Join(t.TempDir(), "missing.pem"))

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "load jwt private key")
}

func TestRun_failsWhenJWTPrivateKeyFileIsNotAKey(t *testing.T) {
	notAKey := filepath.Join(t.TempDir(), "garbage.pem")
	require.NoError(t, os.WriteFile(notAKey, []byte("not a pem file"), 0o600))
	t.Setenv("JWT_PRIVATE_KEY_FILE", notAKey)

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "load jwt private key")
}

func TestRun_failsOnNonPositiveTokenTTL(t *testing.T) {
	for _, name := range []string{"ACCESS_TOKEN_TTL", "REFRESH_TOKEN_TTL"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "0s")

			err := run(t.Context(), nil, io.Discard)

			require.ErrorContains(t, err, "must be positive")
		})
	}
}

func TestRun_failsOnInvalidTokenTTL(t *testing.T) {
	t.Setenv("ACCESS_TOKEN_TTL", "soon")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "ACCESS_TOKEN_TTL")
}

func TestRun_registersAndLogsInThroughTheRouter(t *testing.T) {
	addr, stop := startService(t)
	base := "http://" + addr
	email := fmt.Sprintf("john.doe.%d@example.com", time.Now().UnixNano())
	credentials := fmt.Sprintf(`{"email":%q,"password":"correct horse battery"}`, email)

	code, body := postJSON(t, base+"/api/v1/auth/register",
		fmt.Sprintf(`{"email":%q,"password":"correct horse battery","full_name":"John Doe"}`, email))
	require.Equal(t, http.StatusCreated, code, body)

	code, body = postJSON(t, base+"/api/v1/auth/login", credentials)
	require.Equal(t, http.StatusOK, code, body)
	var pair struct {
		AccessToken string `json:"access_token"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &pair))
	require.NotEmpty(t, pair.AccessToken)

	require.NoError(t, stop())
}

func TestRun_failsOnInvalidAdminCredentials(t *testing.T) {
	t.Setenv("ADMIN_EMAIL", "admin@example.com")
	t.Setenv("ADMIN_PASSWORD", "short")

	err := run(t.Context(), nil, io.Discard)

	require.ErrorContains(t, err, "bootstrap admin")
}

func TestRun_createsAdminFromEnvironment(t *testing.T) {
	email := fmt.Sprintf("admin.%d@example.com", time.Now().UnixNano())
	t.Setenv("ADMIN_EMAIL", email)
	t.Setenv("ADMIN_PASSWORD", "correct horse battery")
	addr, stop := startService(t)

	code, body := postJSON(t, "http://"+addr+"/api/v1/auth/login",
		fmt.Sprintf(`{"email":%q,"password":"correct horse battery"}`, email))

	require.Equal(t, http.StatusOK, code, body)
	require.NoError(t, stop())
}
