package httpserver_test

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/httpserver"
)

func freeAddr(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// statusOf returns the response status, or -1 when the request fails.
func statusOf(ctx context.Context, url string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return -1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return -1
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

func TestNew_setsTimeouts(t *testing.T) {
	srv := httpserver.New(":8080", http.NotFoundHandler())

	require.Equal(t, ":8080", srv.Addr)
	require.NotZero(t, srv.ReadHeaderTimeout)
	require.NotZero(t, srv.ReadTimeout)
	require.NotZero(t, srv.WriteTimeout)
	require.NotZero(t, srv.IdleTimeout)
}

func TestRun_finishesInFlightRequestsBeforeStopping(t *testing.T) {
	addr := freeAddr(t)
	log, _ := newLogger(t)
	started, release := make(chan struct{}), make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusAccepted)
	})

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- httpserver.Run(ctx, log, httpserver.New(addr, mux), 5*time.Second) }()
	require.Eventually(t, func() bool {
		return statusOf(t.Context(), "http://"+addr+"/ping") == http.StatusNoContent
	}, 2*time.Second, 10*time.Millisecond)

	slow := make(chan int, 1)
	go func() { slow <- statusOf(t.Context(), "http://"+addr+"/slow") }()
	<-started
	cancel()

	select {
	case err := <-runDone:
		t.Fatalf("Run returned while a request was still in flight: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	close(release)
	require.Equal(t, http.StatusAccepted, <-slow)
	require.NoError(t, <-runDone)
}

func TestRun_givesUpWhenRequestsOutlastTheShutdownTimeout(t *testing.T) {
	addr := freeAddr(t)
	log, _ := newLogger(t)
	started, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := httpserver.New(addr, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(started)
			<-release
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() { _ = srv.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- httpserver.Run(ctx, log, srv, 50*time.Millisecond) }()
	require.Eventually(t, func() bool {
		return statusOf(t.Context(), "http://"+addr+"/") == http.StatusOK
	}, 2*time.Second, 10*time.Millisecond)

	go statusOf(t.Context(), "http://"+addr+"/slow")
	<-started
	cancel()

	require.ErrorContains(t, <-runDone, "graceful shutdown")
}

func TestRun_failsWhenAddressIsTaken(t *testing.T) {
	log, _ := newLogger(t)
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	err = httpserver.Run(t.Context(), log, httpserver.New(ln.Addr().String(), http.NotFoundHandler()), time.Second)

	require.ErrorContains(t, err, "listen on")
}
