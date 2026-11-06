package health_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/health"
)

func newServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return srv
}

func TestHandler_reportsOK(t *testing.T) {
	rec := httptest.NewRecorder()

	health.Handler(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/healthz", nil))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"status":"ok"}`, rec.Body.String())
}

func TestProbe_succeedsAgainstHealthyService(t *testing.T) {
	paths := make(chan string, 1)
	srv := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		health.Handler(w, r)
	})

	require.NoError(t, health.Probe(t.Context(), srv.Listener.Addr().String()))
	require.Equal(t, "/healthz", <-paths)
}

func TestProbe_acceptsListenAddressWithoutHost(t *testing.T) {
	srv := newServer(t, health.Handler)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	require.NoError(t, err)

	require.NoError(t, health.Probe(t.Context(), ":"+port))
}

func TestProbe_failsOnUnhealthyStatus(t *testing.T) {
	srv := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})

	err := health.Probe(t.Context(), srv.Listener.Addr().String())

	require.ErrorContains(t, err, "unexpected status 503")
}

func TestProbe_failsWhenNothingListens(t *testing.T) {
	srv := newServer(t, health.Handler)
	addr := srv.Listener.Addr().String()
	srv.Close()

	require.Error(t, health.Probe(t.Context(), addr))
}

func TestProbe_rejectsInvalidAddress(t *testing.T) {
	err := health.Probe(t.Context(), "no-port")

	require.ErrorContains(t, err, "parse address")
}
