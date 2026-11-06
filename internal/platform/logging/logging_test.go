package logging_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/logging"
)

func TestNew_writesJSONWithServiceName(t *testing.T) {
	var buf bytes.Buffer
	log, err := logging.New(&buf, "auth", "info")
	require.NoError(t, err)

	log.Info("user registered", "user_id", "u-1")

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	require.Equal(t, "auth", entry["service"])
	require.Equal(t, "INFO", entry["level"])
	require.Equal(t, "user registered", entry["msg"])
	require.Equal(t, "u-1", entry["user_id"])
	require.Contains(t, entry, "time")
}

func TestNew_dropsRecordsBelowLevel(t *testing.T) {
	var buf bytes.Buffer
	log, err := logging.New(&buf, "auth", "warn")
	require.NoError(t, err)

	log.Info("ignored")
	require.Empty(t, buf.String())

	log.Warn("kept")
	require.Contains(t, buf.String(), `"msg":"kept"`)
}

func TestNew_acceptsLevelsCaseInsensitively(t *testing.T) {
	for _, level := range []string{"debug", "INFO", "Warn", "error"} {
		_, err := logging.New(&bytes.Buffer{}, "auth", level)
		require.NoError(t, err, level)
	}
}

func TestNew_rejectsUnknownLevel(t *testing.T) {
	log, err := logging.New(&bytes.Buffer{}, "auth", "loud")

	require.Nil(t, log)
	require.ErrorContains(t, err, `invalid log level "loud"`)
}
