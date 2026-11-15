package messaging

import (
	"errors"
	"fmt"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const dialTimeout = 5 * time.Second

var ErrPermanent = errors.New("permanent message failure")

type Message struct {
	ID        string
	Type      string
	Timestamp time.Time
	Body      []byte
}

type Delivery struct {
	ID   string
	Type string
	Body []byte
}

func dial(url string) (*amqp.Connection, error) {
	conn, err := amqp.DialConfig(url, amqp.Config{Dial: amqp.DefaultDial(dialTimeout)})
	if err != nil {
		return nil, fmt.Errorf("dial rabbitmq: %w", err)
	}
	return conn, nil
}

func declareExchange(ch *amqp.Channel, name string) error {
	if err := ch.ExchangeDeclare(name, amqp.ExchangeTopic, true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", name, err)
	}
	return nil
}
