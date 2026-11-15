package messagingtest

import (
	"context"
	"fmt"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/rabbitmq"
)

const image = "rabbitmq:4.4.0-management-alpine"

func Start(ctx context.Context) (url string, terminate func(), err error) {
	ctr, err := rabbitmq.Run(ctx, image)
	terminate = func() { _ = testcontainers.TerminateContainer(ctr) }
	if err != nil {
		terminate()
		return "", nil, fmt.Errorf("start rabbitmq container: %w", err)
	}

	url, err = ctr.AmqpURL(ctx)
	if err != nil {
		terminate()
		return "", nil, fmt.Errorf("get amqp url: %w", err)
	}
	return url, terminate, nil
}
