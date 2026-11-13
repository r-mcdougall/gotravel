package cache_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/cache"
	"github.com/r-mcdougall/gotravel/internal/platform/cachetest"
)

var redisURL string

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	u, terminate, err := cachetest.Start(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer terminate()

	redisURL = u
	return m.Run()
}

func newClient(t *testing.T, url string) *cache.Client {
	t.Helper()
	client, err := cache.New(url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// blackhole returns the URL of a server that accepts connections and never answers.
func blackhole(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = conn.Close() })
		}
	}()
	t.Cleanup(func() { _ = ln.Close() })

	return "redis://" + ln.Addr().String()
}

func TestNew_rejectsAnInvalidURL(t *testing.T) {
	for _, url := range []string{"://not a url", "http://localhost:6379"} {
		_, err := cache.New(url)

		require.ErrorContains(t, err, "parse redis url", url)
	}
}

func TestNew_doesNotConnectUntilTheFirstOperation(t *testing.T) {
	client, err := cache.New("redis://127.0.0.1:1")

	require.NoError(t, err)
	require.NoError(t, client.Close())
}

func TestGet_reportsAMissingKeyWithoutAnError(t *testing.T) {
	client := newClient(t, redisURL)

	value, found, err := client.Get(t.Context(), "missing:"+t.Name())

	require.NoError(t, err)
	require.False(t, found)
	require.Nil(t, value)
}

func TestSetThenGet_returnsTheStoredValue(t *testing.T) {
	client := newClient(t, redisURL)
	key := "key:" + t.Name()

	require.NoError(t, client.Set(t.Context(), key, []byte(`{"a":1}`), time.Minute))
	value, found, err := client.Get(t.Context(), key)

	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []byte(`{"a":1}`), value)
}

func TestSet_overwritesAnExistingValue(t *testing.T) {
	client := newClient(t, redisURL)
	key := "key:" + t.Name()
	require.NoError(t, client.Set(t.Context(), key, []byte("old"), time.Minute))

	require.NoError(t, client.Set(t.Context(), key, []byte("new"), time.Minute))

	value, _, err := client.Get(t.Context(), key)
	require.NoError(t, err)
	require.Equal(t, []byte("new"), value)
}

func TestSet_expiresTheKeyAfterTheTTL(t *testing.T) {
	client := newClient(t, redisURL)
	key := "key:" + t.Name()

	require.NoError(t, client.Set(t.Context(), key, []byte("x"), 200*time.Millisecond))

	require.Eventually(t, func() bool {
		_, found, err := client.Get(t.Context(), key)
		return err == nil && !found
	}, 5*time.Second, 50*time.Millisecond)
}

func TestSet_rejectsATTLThatIsNotPositiveAndStoresNothing(t *testing.T) {
	client := newClient(t, redisURL)

	for _, ttl := range []time.Duration{0, -time.Second} {
		key := fmt.Sprintf("key:%s:%d", t.Name(), ttl)

		err := client.Set(t.Context(), key, []byte("x"), ttl)

		require.ErrorContains(t, err, "ttl must be positive")
		_, found, getErr := client.Get(t.Context(), key)
		require.NoError(t, getErr)
		require.False(t, found)
	}
}

func TestDelete_removesTheKeysAndIgnoresMissingOnes(t *testing.T) {
	client := newClient(t, redisURL)
	first, second, other := "key:"+t.Name()+":1", "key:"+t.Name()+":2", "key:"+t.Name()+":other"
	for _, key := range []string{first, second, other} {
		require.NoError(t, client.Set(t.Context(), key, []byte("x"), time.Minute))
	}

	require.NoError(t, client.Delete(t.Context(), first, second, "never-existed:"+t.Name()))

	for _, key := range []string{first, second} {
		_, found, err := client.Get(t.Context(), key)
		require.NoError(t, err)
		require.False(t, found, key)
	}
	_, found, err := client.Get(t.Context(), other)
	require.NoError(t, err)
	require.True(t, found, "keys that were not named stay")
}

func TestDelete_withoutKeysDoesNothing(t *testing.T) {
	client := newClient(t, redisURL)

	require.NoError(t, client.Delete(t.Context()))
}

func TestOperations_failWhenRedisIsUnreachable(t *testing.T) {
	client := newClient(t, "redis://127.0.0.1:1")

	_, found, err := client.Get(t.Context(), "k")
	require.Error(t, err)
	require.False(t, found)
	require.Error(t, client.Set(t.Context(), "k", []byte("x"), time.Minute))
	require.Error(t, client.Delete(t.Context(), "k"))
}

func TestOperations_failFastWhenRedisStopsAnswering(t *testing.T) {
	client := newClient(t, blackhole(t))

	start := time.Now()
	_, _, err := client.Get(t.Context(), "k")
	elapsed := time.Since(start)

	require.Error(t, err)
	require.Less(t, elapsed, time.Second, "the operation timeout must bound the wait")
}
