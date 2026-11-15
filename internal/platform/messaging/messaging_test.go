package messaging

import (
	"context"
	"os"
	"sync"
	"testing"

	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/stretchr/testify/require"

	"github.com/r-mcdougall/gotravel/internal/platform/messagingtest"
)

var (
	rabbitmqOnce      sync.Once
	rabbitmqURLValue  string
	rabbitmqErr       error
	terminateRabbitMQ func()
)

func TestMain(m *testing.M) {
	code := m.Run()
	if terminateRabbitMQ != nil {
		terminateRabbitMQ()
	}
	os.Exit(code)
}

func rabbitmqURL(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	rabbitmqOnce.Do(func() {
		rabbitmqURLValue, terminateRabbitMQ, rabbitmqErr = messagingtest.Start(context.Background())
	})
	require.NoError(t, rabbitmqErr)
	return rabbitmqURLValue
}

func bindTestQueue(t *testing.T, url, exchange, routingKey string) (*amqp.Channel, string) {
	t.Helper()

	conn, err := dial(url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ch, err := conn.Channel()
	require.NoError(t, err)
	require.NoError(t, declareExchange(ch, exchange))

	queue, err := ch.QueueDeclare("", false, true, true, false, nil)
	require.NoError(t, err)
	require.NoError(t, ch.QueueBind(queue.Name, routingKey, exchange, false, nil))
	return ch, queue.Name
}

func getMessage(t *testing.T, ch *amqp.Channel, queue string) amqp.Delivery {
	t.Helper()

	delivery, ok, err := ch.Get(queue, true)
	require.NoError(t, err)
	require.True(t, ok, "expected a message in queue %s", queue)
	return delivery
}
