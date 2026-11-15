package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
)

func TestOutcomeOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want outcome
	}{
		{name: "success acks", err: nil, want: outcomeAck},
		{name: "permanent failure rejects", err: ErrPermanent, want: outcomeReject},
		{name: "wrapped permanent failure rejects", err: fmt.Errorf("decode flight.changed: %w", ErrPermanent), want: outcomeReject},
		{name: "other failure requeues", err: errors.New("database unavailable"), want: outcomeRequeue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, outcomeOf(tt.err))
		})
	}
}

func TestConsume_returnsWhenTheContextIsCanceledWhileReconnecting(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Consume(ctx, unreachableURL, Subscription{Exchange: "test.events", RoutingKey: "flight.changed", Queue: "test.queue"},
			func(context.Context, Delivery) error { return nil }, slog.New(slog.DiscardHandler))
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Consume did not return after the context was canceled")
	}
}

func TestConsume_deliversPublishedMessagesToTheHandler(t *testing.T) {
	url := rabbitmqURL(t)
	sub := Subscription{Exchange: "test.consumer-deliver", RoutingKey: "flight.changed", Queue: "test.consumer-deliver"}
	declareTestSubscription(t, url, sub)

	received := make(chan Delivery, 1)
	startConsumer(t, url, sub, func(_ context.Context, d Delivery) error {
		received <- d
		return nil
	})
	publish(t, url, sub.Exchange, Message{ID: "m1", Type: "flight.changed", Body: []byte(`{"flight_number":"GA402"}`)})

	select {
	case d := <-received:
		require.Equal(t, "m1", d.ID)
		require.Equal(t, "flight.changed", d.Type)
		require.JSONEq(t, `{"flight_number":"GA402"}`, string(d.Body))
	case <-time.After(5 * time.Second):
		t.Fatal("handler did not receive the message")
	}
}

func TestConsume_movesPermanentFailuresToTheDeadLetterQueue(t *testing.T) {
	url := rabbitmqURL(t)
	sub := Subscription{Exchange: "test.consumer-dlq", RoutingKey: "flight.changed", Queue: "test.consumer-dlq"}
	ch := declareTestSubscription(t, url, sub)

	startConsumer(t, url, sub, func(context.Context, Delivery) error {
		return fmt.Errorf("decode flight.changed: %w", ErrPermanent)
	})
	publish(t, url, sub.Exchange, Message{ID: "m1", Type: "flight.changed", Body: []byte(`not json`)})

	require.Eventually(t, func() bool {
		d, ok, err := ch.Get(sub.Queue+".dlq", true)
		return err == nil && ok && d.MessageId == "m1"
	}, 5*time.Second, 50*time.Millisecond)
}

func TestConsume_redeliversAfterATemporaryFailure(t *testing.T) {
	url := rabbitmqURL(t)
	sub := Subscription{Exchange: "test.consumer-retry", RoutingKey: "flight.changed", Queue: "test.consumer-retry"}
	declareTestSubscription(t, url, sub)

	var attempts atomic.Int32
	handled := make(chan struct{})
	startConsumer(t, url, sub, func(context.Context, Delivery) error {
		if attempts.Add(1) == 1 {
			return errors.New("database unavailable")
		}
		close(handled)
		return nil
	})
	publish(t, url, sub.Exchange, Message{ID: "m1", Type: "flight.changed", Body: []byte(`{}`)})

	select {
	case <-handled:
		require.Equal(t, int32(2), attempts.Load())
	case <-time.After(10 * time.Second):
		t.Fatalf("message was not redelivered, attempts: %d", attempts.Load())
	}
}

func declareTestSubscription(t *testing.T, url string, sub Subscription) *amqp.Channel {
	t.Helper()

	conn, err := dial(url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ch, err := conn.Channel()
	require.NoError(t, err)
	require.NoError(t, declareSubscription(ch, sub))
	_, err = ch.QueuePurge(sub.Queue, false)
	require.NoError(t, err)
	_, err = ch.QueuePurge(sub.Queue+".dlq", false)
	require.NoError(t, err)
	return ch
}

func startConsumer(t *testing.T, url string, sub Subscription, handle Handler) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		Consume(ctx, url, sub, handle, slog.New(slog.DiscardHandler))
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func publish(t *testing.T, url, exchange string, msgs ...Message) {
	t.Helper()

	p := NewPublisher(url, exchange)
	t.Cleanup(func() { _ = p.Close() })
	require.NoError(t, p.Publish(t.Context(), msgs))
}
