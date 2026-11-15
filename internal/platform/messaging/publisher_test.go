package messaging

import (
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"
)

const unreachableURL = "amqp://guest:guest@127.0.0.1:1/"

func TestPublisherPublish_emptyBatchDoesNotConnect(t *testing.T) {
	p := NewPublisher(unreachableURL, "test.events")

	require.NoError(t, p.Publish(t.Context(), nil))
	require.Nil(t, p.conn)
}

func TestPublisherPublish_returnsErrorWhenTheBrokerIsUnreachable(t *testing.T) {
	p := NewPublisher(unreachableURL, "test.events")

	err := p.Publish(t.Context(), []Message{{ID: "m1", Type: "flight.changed", Body: []byte(`{}`)}})

	require.ErrorContains(t, err, "dial rabbitmq")
	require.Nil(t, p.conn)
}

func TestPublisherPublish_deliversConfirmedMessagesWithProperties(t *testing.T) {
	url := rabbitmqURL(t)
	ch, queue := bindTestQueue(t, url, "test.publisher-properties", "flight.changed")
	p := NewPublisher(url, "test.publisher-properties")
	t.Cleanup(func() { _ = p.Close() })

	occurredAt := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	msgs := []Message{
		{ID: "m1", Type: "flight.changed", Timestamp: occurredAt, Body: []byte(`{"flight_number":"GA402"}`)},
		{ID: "m2", Type: "flight.changed", Timestamp: occurredAt, Body: []byte(`{"flight_number":"GA403"}`)},
	}
	require.NoError(t, p.Publish(t.Context(), msgs))

	for _, want := range msgs {
		got := getMessage(t, ch, queue)
		require.Equal(t, want.ID, got.MessageId)
		require.Equal(t, want.Type, got.Type)
		require.Equal(t, "application/json", got.ContentType)
		require.Equal(t, amqp.Persistent, got.DeliveryMode)
		require.True(t, want.Timestamp.Equal(got.Timestamp))
		require.JSONEq(t, string(want.Body), string(got.Body))
	}
}

func TestPublisherPublish_reconnectsAfterTheConnectionIsLost(t *testing.T) {
	url := rabbitmqURL(t)
	ch, queue := bindTestQueue(t, url, "test.publisher-reconnect", "flight.changed")
	p := NewPublisher(url, "test.publisher-reconnect")
	t.Cleanup(func() { _ = p.Close() })

	require.NoError(t, p.Publish(t.Context(), []Message{{ID: "m1", Type: "flight.changed", Body: []byte(`{}`)}}))
	require.NoError(t, p.conn.Close())
	require.NoError(t, p.Publish(t.Context(), []Message{{ID: "m2", Type: "flight.changed", Body: []byte(`{}`)}}))

	require.Equal(t, "m1", getMessage(t, ch, queue).MessageId)
	require.Equal(t, "m2", getMessage(t, ch, queue).MessageId)
}
