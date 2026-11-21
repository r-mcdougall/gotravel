package metrics_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/metrics"
)

func newRouter(m *metrics.Metrics) http.Handler {
	r := chi.NewRouter()
	r.Use(m.Middleware)
	r.Get("/flights/{id}", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/boom", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	r.Get("/silent", func(http.ResponseWriter, *http.Request) {})
	return r
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rec
}

func scrape(t *testing.T, m *metrics.Metrics) string {
	t.Helper()
	rec := get(t, m.Handler(), "/metrics")
	require.Equal(t, http.StatusOK, rec.Code)
	return rec.Body.String()
}

func TestMiddleware_labelsByRoutePatternAndStatus(t *testing.T) {
	m := metrics.New()
	h := newRouter(m)

	get(t, h, "/flights/1")
	get(t, h, "/flights/2")
	get(t, h, "/boom")

	body := scrape(t, m)
	require.Contains(t, body, `http_requests_total{method="GET",route="/flights/{id}",status="200"} 2`)
	require.Contains(t, body, `http_requests_total{method="GET",route="/boom",status="500"} 1`)
	require.Contains(t, body, `http_request_duration_seconds_count{method="GET",route="/flights/{id}",status="200"} 2`)
}

func TestMiddleware_treatsHandlerWithoutWriteAsOK(t *testing.T) {
	m := metrics.New()

	get(t, newRouter(m), "/silent")

	require.Contains(t, scrape(t, m), `http_requests_total{method="GET",route="/silent",status="200"} 1`)
}

func TestMiddleware_groupsUnmatchedPathsUnderOneRoute(t *testing.T) {
	m := metrics.New()
	h := newRouter(m)

	get(t, h, "/nope")
	get(t, h, "/also/nope")

	body := scrape(t, m)
	require.Contains(t, body, `http_requests_total{method="GET",route="unmatched",status="404"} 2`)
	require.NotContains(t, body, `route="/nope"`)
}

func TestMiddleware_recordsLatencyFromOneMillisecond(t *testing.T) {
	m := metrics.New()

	get(t, newRouter(m), "/flights/1")

	body := scrape(t, m)
	for _, le := range []string{"0.001", "0.0025", "0.5", "5"} {
		require.Contains(t, body, `http_request_duration_seconds_bucket{method="GET",route="/flights/{id}",status="200",le="`+le+`"}`)
	}
	require.NotContains(t, body, `le="10"`)
}

func TestHandler_exposesRuntimeMetrics(t *testing.T) {
	require.Contains(t, scrape(t, metrics.New()), "go_goroutines")
}
