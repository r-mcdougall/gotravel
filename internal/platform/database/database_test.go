package database_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/database"
	"github.com/r-mcdougall/gotravel/internal/platform/dbtest"
)

var (
	serverURL string
	dbCounter atomic.Int32
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	u, terminate, err := dbtest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer terminate()

	serverURL = u
	return m.Run()
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// newDatabase creates an empty database so that tests do not share
// goose's version table, and returns its URL.
func newDatabase(t *testing.T) string {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), serverURL)
	require.NoError(t, err)
	defer func() { _ = conn.Close(t.Context()) }()

	name := fmt.Sprintf("test_%d", dbCounter.Add(1))
	_, err = conn.Exec(t.Context(), "CREATE DATABASE "+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err)

	u, err := url.Parse(serverURL)
	require.NoError(t, err)
	u.Path = "/" + name
	return u.String()
}

func tableExists(t *testing.T, databaseURL, name string) bool {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), databaseURL)
	require.NoError(t, err)
	defer func() { _ = conn.Close(t.Context()) }()

	var exists bool
	err = conn.QueryRow(t.Context(), "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists)
	require.NoError(t, err)
	return exists
}

func TestMigrate_appliesMigrationsAndIsIdempotent(t *testing.T) {
	databaseURL := newDatabase(t)
	fsys := fstest.MapFS{
		"00001_create_widgets.sql": {Data: []byte(`-- +goose Up
CREATE TABLE widgets (id int PRIMARY KEY);
-- +goose Down
DROP TABLE widgets;
`)},
	}

	require.NoError(t, database.Migrate(t.Context(), discardLogger(), databaseURL, fsys))
	require.True(t, tableExists(t, databaseURL, "widgets"))

	require.NoError(t, database.Migrate(t.Context(), discardLogger(), databaseURL, fsys))
}

func TestMigrate_failsOnBrokenSQLAndLeavesNoTable(t *testing.T) {
	databaseURL := newDatabase(t)
	fsys := fstest.MapFS{
		"00001_create_gadgets.sql": {Data: []byte(`-- +goose Up
CREATE TABLE gadgets (id int PRIMARY KEY);
SELECT * FROM does_not_exist;
-- +goose Down
DROP TABLE gadgets;
`)},
	}

	err := database.Migrate(t.Context(), discardLogger(), databaseURL, fsys)

	require.ErrorContains(t, err, "apply migrations")
	require.False(t, tableExists(t, databaseURL, "gadgets"))
}

func TestMigrate_failsWhenDatabaseIsUnreachable(t *testing.T) {
	fsys := fstest.MapFS{
		"00001_create_things.sql": {Data: []byte(`-- +goose Up
CREATE TABLE things (id int PRIMARY KEY);
-- +goose Down
DROP TABLE things;
`)},
	}

	err := database.Migrate(t.Context(), discardLogger(), "postgres://x:y@127.0.0.1:1/z", fsys)

	require.ErrorContains(t, err, "apply migrations")
}

func TestMigrate_rejectsInvalidURL(t *testing.T) {
	err := database.Migrate(t.Context(), discardLogger(), "://not a url", fstest.MapFS{})

	require.ErrorContains(t, err, "parse database url")
}
