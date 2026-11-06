package httpserver_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
	"github.com/r-mcdougall/gotravel/internal/platform/metrics"
)

func newTestRouter(t *testing.T) (http.Handler, *bytes.Buffer) {
	t.Helper()
	log, buf := newLogger(t)
	h := httpserver.NewRouter(log, metrics.New(), func(r chi.Router) {
		r.Get("/ping", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("pong"))
		})
		r.Get("/whoami", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(httpserver.RequestIDFrom(r.Context())))
		})
		r.Get("/panic", func(http.ResponseWriter, *http.Request) {
			panic("kaboom")
		})
	})
	return h, buf
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
	return rec
}

func TestNewRouter_servesServiceAndOperationalRoutes(t *testing.T) {
	h, _ := newTestRouter(t)

	ping := get(t, h, "/ping")
	require.Equal(t, http.StatusOK, ping.Code)
	require.Equal(t, "pong", ping.Body.String())

	health := get(t, h, "/healthz")
	require.Equal(t, http.StatusOK, health.Code)
	require.JSONEq(t, `{"status":"ok"}`, health.Body.String())

	scrape := get(t, h, "/metrics")
	require.Equal(t, http.StatusOK, scrape.Code)
	require.Contains(t, scrape.Body.String(), "go_goroutines")
}

func TestNewRouter_instrumentsServiceRoutesOnly(t *testing.T) {
	h, logs := newTestRouter(t)

	get(t, h, "/ping")
	get(t, h, "/panic")
	get(t, h, "/healthz")
	body := get(t, h, "/metrics").Body.String()

	require.Contains(t, body, `http_requests_total{method="GET",route="/ping",status="200"} 1`)
	require.Contains(t, body, `http_requests_total{method="GET",route="/panic",status="500"} 1`)
	require.NotContains(t, body, `route="/healthz"`)
	require.NotContains(t, body, `route="/metrics"`)

	require.Contains(t, logs.String(), `"path":"/ping"`)
	require.NotContains(t, logs.String(), `"path":"/healthz"`)
	require.NotContains(t, logs.String(), `"path":"/metrics"`)
}

func TestNewRouter_answersUnknownPathsWithJSON404(t *testing.T) {
	h, logs := newTestRouter(t)

	rec := get(t, h, "/nope")

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"error":{"code":"NOT_FOUND","message":"route not found"}}`, rec.Body.String())
	require.Contains(t, get(t, h, "/metrics").Body.String(), `http_requests_total{method="GET",route="unmatched",status="404"} 1`)
	require.Contains(t, logs.String(), `"path":"/nope"`)
}

func TestNewRouter_givesHandlersTheRequestID(t *testing.T) {
	h, _ := newTestRouter(t)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/whoami", nil)
	req.Header.Set("X-Request-ID", "rid-7")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	require.Equal(t, "rid-7", rec.Body.String())
}
