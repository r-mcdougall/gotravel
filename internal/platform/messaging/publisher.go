package messaging

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const confirmTimeout = 5 * time.Second

type Publisher struct {
	url      string
	exchange string

	mu   sync.Mutex
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewPublisher(url, exchange string) *Publisher {
	return &Publisher{url: url, exchange: exchange}
}

func (p *Publisher) Publish(ctx context.Context, messages []Message) error {
	if len(messages) == 0 {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if err := p.publish(ctx, messages); err != nil {
		_ = p.closeLocked()
		return err
	}

	return nil
}

func (p *Publisher) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closeLocked()
}

func (p *Publisher) publish(ctx context.Context, messages []Message) error {
	ch, err := p.channel()
	if err != nil {
		return err
	}

	confirmations := make([]*amqp.DeferredConfirmation, 0, len(messages))
	for _, msg := range messages {
		publishing := amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			MessageId:    msg.ID,
			Type:         msg.Type,
			Timestamp:    msg.Timestamp,
			Body:         msg.Body,
		}
		confirmation, err := ch.PublishWithDeferredConfirmWithContext(ctx, p.exchange, msg.Type, false, false, publishing)
		if err != nil {
			return fmt.Errorf("publish message %s: %w", msg.ID, err)
		}

		confirmations = append(confirmations, confirmation)
	}

	ctx, cancel := context.WithTimeout(ctx, confirmTimeout)
	defer cancel()

	for i, confirmation := range confirmations {
		acked, err := confirmation.WaitContext(ctx)
		if err != nil {
			return fmt.Errorf("wait for confirmation of message %s: %w", messages[i].ID, err)
		}
		if !acked {
			return fmt.Errorf("message %s was not confirmed by the broker", messages[i].ID)
		}
	}
	return nil
}

func (p *Publisher) channel() (*amqp.Channel, error) {
	if p.ch != nil && !p.ch.IsClosed() {
		return p.ch, nil
	}
	_ = p.closeLocked()

	conn, err := dial(p.url)
	if err != nil {
		return nil, err
	}

	ch, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("open channel: %w", err)
	}

	if err := declareExchange(ch, p.exchange); err != nil {
		_ = conn.Close()
		return nil, err
	}

	if err := ch.Confirm(false); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("enable publisher confirms: %w", err)
	}

	p.conn, p.ch = conn, ch
	return ch, nil
}

func (p *Publisher) closeLocked() error {
	if p.conn == nil {
		return nil
	}

	err := p.conn.Close()
	p.conn, p.ch = nil, nil
	if err != nil && !errors.Is(err, amqp.ErrClosed) {
		return fmt.Errorf("close rabbitmq connection: %w", err)
	}

	return nil
}
