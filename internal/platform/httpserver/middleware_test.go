package httpserver_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
	"github.com/r-mcdougall/gotravel/internal/platform/logging"
)

func newLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	log, err := logging.New(&buf, "test", "info")
	require.NoError(t, err)
	return log, &buf
}

func serve(t *testing.T, h http.Handler, requestID string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/p", nil)
	if requestID != "" {
		req.Header.Set("X-Request-ID", requestID)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRequestID(t *testing.T) {
	tests := []struct {
		name   string
		header string
		keep   bool
	}{
		{name: "keeps a valid id", header: "abc-123", keep: true},
		{name: "generates one when missing", header: ""},
		{name: "replaces an id with spaces", header: "has space"},
		{name: "replaces an id that is too long", header: strings.Repeat("a", 129)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			h := httpserver.RequestID(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				got = httpserver.RequestIDFrom(r.Context())
			}))

			serve(t, h, tt.header)

			if tt.keep {
				require.Equal(t, tt.header, got)
				return
			}
			require.Regexp(t, `^[0-9a-f]{32}$`, got)
		})
	}
}

func TestRequestIDFrom_emptyWithoutMiddleware(t *testing.T) {
	require.Empty(t, httpserver.RequestIDFrom(t.Context()))
}

func TestAccessLog_recordsRequestDetails(t *testing.T) {
	log, buf := newLogger(t)
	h := httpserver.RequestID(httpserver.AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("x"))
	})))

	serve(t, h, "rid-1")

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	require.Equal(t, "INFO", entry["level"])
	require.Equal(t, "rid-1", entry["request_id"])
	require.Equal(t, "GET", entry["method"])
	require.Equal(t, "/p", entry["path"])
	require.InDelta(t, 201, entry["status"], 0)
	require.InDelta(t, 1, entry["bytes"], 0)
	require.Contains(t, entry, "duration_ms")
}

func TestAccessLog_logsServerErrorsAtErrorLevel(t *testing.T) {
	log, buf := newLogger(t)
	h := httpserver.AccessLog(log)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))

	serve(t, h, "")

	require.Contains(t, buf.String(), `"level":"ERROR"`)
}

func TestRecoverer_returnsJSON500AndLogsThePanic(t *testing.T) {
	log, buf := newLogger(t)
	h := httpserver.Recoverer(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	}))

	rec := serve(t, h, "")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"error":{"code":"INTERNAL_ERROR","message":"internal server error"}}`, rec.Body.String())
	require.Contains(t, buf.String(), `"msg":"panic recovered"`)
	require.Contains(t, buf.String(), `"panic":"kaboom"`)
}

func TestRecoverer_letsAbortHandlerThrough(t *testing.T) {
	log, buf := newLogger(t)
	h := httpserver.Recoverer(log)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))

	require.PanicsWithValue(t, http.ErrAbortHandler, func() { serve(t, h, "") })
	require.Empty(t, buf.String())
}

func TestChain_accessLogSeesTheRecovered500(t *testing.T) {
	log, buf := newLogger(t)
	h := httpserver.RequestID(httpserver.AccessLog(log)(httpserver.Recoverer(log)(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("kaboom") }),
	)))

	rec := serve(t, h, "rid-9")

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], `"msg":"panic recovered"`)
	require.Contains(t, lines[0], `"request_id":"rid-9"`)
	require.Contains(t, lines[1], `"msg":"http request"`)
	require.Contains(t, lines[1], `"status":500`)
}
