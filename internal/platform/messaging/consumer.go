package messaging

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	prefetch   = 10
	retryDelay = time.Second
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

type Subscription struct {
	Exchange   string
	RoutingKey string
	Queue      string
}

type Handler func(ctx context.Context, d Delivery) error

func Consume(ctx context.Context, url string, sub Subscription, handle Handler, logger *slog.Logger) {
	logger = logger.With("queue", sub.Queue)
	backoff := minBackoff

	for {
		connected, err := consumeSession(ctx, url, sub, handle, logger)
		if ctx.Err() != nil {
			return
		}
		if connected {
			backoff = minBackoff
		}

		logger.Warn("rabbitmq consumer disconnected", "error", err, "retry_in", backoff)
		if !sleep(ctx, backoff) {
			return
		}

		backoff = min(backoff*2, maxBackoff)
	}
}

func consumeSession(ctx context.Context, url string, sub Subscription, handle Handler, logger *slog.Logger) (connected bool, err error) {
	conn, err := dial(url)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()

	ch, err := conn.Channel()
	if err != nil {
		return false, fmt.Errorf("open channel: %w", err)
	}

	if err := declareSubscription(ch, sub); err != nil {
		return false, err
	}

	if err := ch.Qos(prefetch, 0, false); err != nil {
		return false, fmt.Errorf("set prefetch: %w", err)
	}

	deliveries, err := ch.ConsumeWithContext(ctx, sub.Queue, "", false, false, false, false, nil)
	if err != nil {
		return false, fmt.Errorf("consume %s: %w", sub.Queue, err)
	}

	logger.Info("rabbitmq consumer started")
	for {
		select {
		case <-ctx.Done():
			return true, ctx.Err()
		case d, ok := <-deliveries:
			if !ok {
				return true, fmt.Errorf("delivery channel closed")
			}
			if err := process(ctx, d, handle, logger); err != nil {
				return true, err
			}
		}
	}
}

type outcome int

const (
	outcomeAck outcome = iota
	outcomeReject
	outcomeRequeue
)

func process(ctx context.Context, d amqp.Delivery, handle Handler, logger *slog.Logger) error {
	err := handle(ctx, Delivery{ID: d.MessageId, Type: d.Type, Body: d.Body})
	if ctx.Err() != nil {
		return nil
	}

	logger = logger.With("message_id", d.MessageId, "type", d.Type)
	switch outcomeOf(err) {
	case outcomeAck:
		if err := d.Ack(false); err != nil {
			return fmt.Errorf("ack message: %w", err)
		}
	case outcomeReject:
		logger.Error("message rejected to dead-letter queue", "error", err)
		if err := d.Reject(false); err != nil {
			return fmt.Errorf("reject message: %w", err)
		}
	case outcomeRequeue:
		logger.Warn("message handling failed, requeueing", "error", err)
		if !sleep(ctx, retryDelay) {
			return nil
		}
		if err := d.Nack(false, true); err != nil {
			return fmt.Errorf("nack message: %w", err)
		}
	}
	return nil
}

func outcomeOf(err error) outcome {
	switch {
	case err == nil:
		return outcomeAck
	case errors.Is(err, ErrPermanent):
		return outcomeReject
	default:
		return outcomeRequeue
	}
}

func declareSubscription(ch *amqp.Channel, sub Subscription) error {
	if err := declareExchange(ch, sub.Exchange); err != nil {
		return err
	}

	deadLetterQueue := sub.Queue + ".dlq"
	if _, err := ch.QueueDeclare(deadLetterQueue, true, false, false, false, amqp.Table{
		amqp.QueueTypeArg: amqp.QueueTypeQuorum,
	}); err != nil {
		return fmt.Errorf("declare queue %s: %w", deadLetterQueue, err)
	}

	if _, err := ch.QueueDeclare(sub.Queue, true, false, false, false, amqp.Table{
		amqp.QueueTypeArg:           amqp.QueueTypeQuorum,
		"x-dead-letter-exchange":    "",
		"x-dead-letter-routing-key": deadLetterQueue,
	}); err != nil {
		return fmt.Errorf("declare queue %s: %w", sub.Queue, err)
	}

	if err := ch.QueueBind(sub.Queue, sub.RoutingKey, sub.Exchange, false, nil); err != nil {
		return fmt.Errorf("bind queue %s: %w", sub.Queue, err)
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
