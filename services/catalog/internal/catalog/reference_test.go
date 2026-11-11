package catalog

import (
	"context"
	"errors"
	"testing"
	"time"
	_ "time/tzdata" // keeps the tests independent of the host's time zone database

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/services/catalog/internal/store"
)

var errBoom = errors.New("boom")

type fakeReferenceQueries struct {
	t            *testing.T
	listAirlines func(ctx context.Context) ([]store.Airline, error)
	listAirports func(ctx context.Context) ([]store.Airport, error)
}

func (f *fakeReferenceQueries) ListAirlines(ctx context.Context) ([]store.Airline, error) {
	f.t.Helper()
	if f.listAirlines == nil {
		f.t.Fatal("unexpected call to ListAirlines")
	}
	return f.listAirlines(ctx)
}

func (f *fakeReferenceQueries) ListAirports(ctx context.Context) ([]store.Airport, error) {
	f.t.Helper()
	if f.listAirports == nil {
		f.t.Fatal("unexpected call to ListAirports")
	}
	return f.listAirports(ctx)
}

func sampleReferenceQueries(t *testing.T) *fakeReferenceQueries {
	t.Helper()
	return &fakeReferenceQueries{
		t: t,
		listAirlines: func(context.Context) ([]store.Airline, error) {
			return []store.Airline{
				{Code: "GA", Name: "Garuda Indonesia"},
				{Code: "JT", Name: "Lion Air"},
			}, nil
		},
		listAirports: func(context.Context) ([]store.Airport, error) {
			return []store.Airport{
				{Code: "CGK", Name: "Soekarno-Hatta International Airport", City: "Jakarta", Timezone: "Asia/Jakarta"},
				{Code: "DPS", Name: "I Gusti Ngurah Rai International Airport", City: "Denpasar", Timezone: "Asia/Makassar"},
			}, nil
		},
	}
}

func utcOffsetSeconds(t *testing.T, location *time.Location) int {
	t.Helper()
	_, offset := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC).In(location).Zone()
	return offset
}

func TestLoadReference_resolvesAirlinesByCode(t *testing.T) {
	reference, err := LoadReference(t.Context(), sampleReferenceQueries(t))
	require.NoError(t, err)

	airline, ok := reference.Airline("GA")

	require.True(t, ok)
	require.Equal(t, AirlineResponse{Code: "GA", Name: "Garuda Indonesia"}, airline)
}

func TestLoadReference_resolvesAirportsWithTheirOwnTimeZone(t *testing.T) {
	reference, err := LoadReference(t.Context(), sampleReferenceQueries(t))
	require.NoError(t, err)

	jakarta, ok := reference.Airport("CGK")
	require.True(t, ok)
	require.Equal(t, AirportResponse{Code: "CGK", Name: "Soekarno-Hatta International Airport", City: "Jakarta"}, jakarta.AirportResponse)
	require.Equal(t, 7*60*60, utcOffsetSeconds(t, jakarta.Location))

	denpasar, ok := reference.Airport("DPS")
	require.True(t, ok)
	require.Equal(t, 8*60*60, utcOffsetSeconds(t, denpasar.Location))
}

func TestLoadReference_doesNotKnowUnlistedCodes(t *testing.T) {
	reference, err := LoadReference(t.Context(), sampleReferenceQueries(t))
	require.NoError(t, err)

	_, knownAirline := reference.Airline("XX")
	_, knownAirport := reference.Airport("XXX")
	_, lowerCaseAirport := reference.Airport("cgk")

	require.False(t, knownAirline)
	require.False(t, knownAirport)
	require.False(t, lowerCaseAirport)
}

func TestLoadReference_failsOnAnInvalidTimeZone(t *testing.T) {
	queries := sampleReferenceQueries(t)
	queries.listAirports = func(context.Context) ([]store.Airport, error) {
		return []store.Airport{{Code: "XYZ", Name: "Nowhere", City: "Nowhere", Timezone: "Mars/Olympus"}}, nil
	}

	_, err := LoadReference(t.Context(), queries)

	require.ErrorContains(t, err, "airport XYZ")
	require.ErrorContains(t, err, "load time zone")
}

func TestLoadReference_failsWhenListingAirlinesFails(t *testing.T) {
	queries := &fakeReferenceQueries{
		t: t,
		listAirlines: func(context.Context) ([]store.Airline, error) {
			return nil, errBoom
		},
	}

	_, err := LoadReference(t.Context(), queries)

	require.ErrorIs(t, err, errBoom)
	require.ErrorContains(t, err, "list airlines")
}

func TestLoadReference_failsWhenListingAirportsFails(t *testing.T) {
	queries := sampleReferenceQueries(t)
	queries.listAirports = func(context.Context) ([]store.Airport, error) {
		return nil, errBoom
	}

	_, err := LoadReference(t.Context(), queries)

	require.ErrorIs(t, err, errBoom)
	require.ErrorContains(t, err, "list airports")
}
