package httpserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
)

type payload struct {
	Name string `json:"name"`
}

func decode(t *testing.T, body string) (payload, error) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(body))
	var got payload
	err := httpserver.DecodeJSON(httptest.NewRecorder(), req, &got)
	return got, err
}

func TestDecodeJSON_readsValidBody(t *testing.T) {
	got, err := decode(t, `{"name":"x"}`)

	require.NoError(t, err)
	require.Equal(t, payload{Name: "x"}, got)
}

func TestDecodeJSON_rejectsBadBodies(t *testing.T) {
	tests := map[string]string{
		"unknown field":   `{"name":"x","role":"admin"}`,
		"malformed":       `{"name":`,
		"trailing value":  `{"name":"x"}{"name":"y"}`,
		"trailing text":   `{"name":"x"} nope`,
		"empty":           ``,
		"wrong type":      `{"name":1}`,
		"larger than 1MB": `{"name":"` + strings.Repeat("a", 1<<20) + `"}`,
	}

	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := decode(t, body)

			require.Error(t, err)
		})
	}
}

func TestWriteValidationError_listsFields(t *testing.T) {
	rec := httptest.NewRecorder()

	httpserver.WriteValidationError(rec, []httpserver.ErrorDetail{{Field: "email", Message: "must be a valid email address"}})

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"error":{"code":"VALIDATION_ERROR","message":"request validation failed","details":[{"field":"email","message":"must be a valid email address"}]}}`, rec.Body.String())
}

func TestWriteJSON_setsStatusAndContentType(t *testing.T) {
	rec := httptest.NewRecorder()

	httpserver.WriteJSON(rec, http.StatusCreated, map[string]string{"ok": "yes"})

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
	require.JSONEq(t, `{"ok":"yes"}`, rec.Body.String())
}
