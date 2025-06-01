package pubsub

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"os"

	amqp "github.com/rabbitmq/amqp091-go"
)

type QueueType int

const (
	DurableQueue QueueType = iota
	TransientQueue
)

type AckType int

const (
	Ack AckType = iota
	NackRequeue
	NackDiscard
)

func DeclareAndBind(
	conn *amqp.Connection,
	exchange, queueName, key string,
	qt QueueType,
) (*amqp.Channel, amqp.Queue, error) {
	channel, err := conn.Channel()
	if err != nil {
		return nil, amqp.Queue{}, err
	}

	durable := qt == DurableQueue
	autoDelete := !durable
	exclusive := autoDelete

	table := amqp.Table{
		"x-dead-letter-exchange": "peril_dlx",
	}
	queue, err := channel.QueueDeclare(
		queueName,
		durable, autoDelete, exclusive,
		false, table)
	if err != nil {
		return nil, amqp.Queue{}, err
	}

	if err := channel.QueueBind(
		queueName, key, exchange,
		false, nil,
	); err != nil {
		return nil, amqp.Queue{}, err
	}

	return channel, queue, nil
}

func publish(
	ch *amqp.Channel,
	contentType, exchange, key string,
	encodedVal []byte,
) error {
	toSend := amqp.Publishing{
		ContentType: contentType,
		Body:        encodedVal,
	}

	return ch.PublishWithContext(
		context.Background(),
		exchange, key,
		false, false,
		toSend)
}

func PublishJSON[T any](
	ch *amqp.Channel,
	exchange, key string,
	val T,
) error {
	encodedVal, err := json.Marshal(val)
	if err != nil {
		return err
	}

	return publish(
		ch, "application/json", exchange, key, encodedVal)
}

func PublishGob[T any](
	ch *amqp.Channel,
	exchange, key string,
	val T,
) error {
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)

	if err := enc.Encode(val); err != nil {
		return err
	}
	encodedVal := buf.Bytes()

	return publish(
		ch, "application/gob", exchange, key, encodedVal)
}

func subscribe[T any](
	conn *amqp.Connection,
	exchange, queueName, key string,
	qt QueueType,
	handler func(T) AckType,
	unmarshaller func([]byte) (T, error),
) error {
	channel, _, err := DeclareAndBind(
		conn, exchange, queueName, key, qt,
	)
	if err != nil {
		return err
	}

	deliveryCh, err := channel.Consume(
		queueName, "", false, false, false, false, nil)
	if err != nil {
		return err
	}

	go func() {
		for delivery := range deliveryCh {
			var rawT T
			rawT, err := unmarshaller(delivery.Body)
			if err != nil {
				fmt.Fprintf(os.Stderr, "failed to decode delivery: %v\n", err)

				return
			}

			switch handler(rawT) {
			case Ack:
				if err := delivery.Ack(false); err != nil {
					fmt.Fprintf(os.Stderr, "failed to acknowledge delivery: %v\n", err)

					return
				}
			case NackRequeue:
				if err := delivery.Nack(false, true); err != nil {
					fmt.Fprintf(
						os.Stderr,
						"failed to negative acknowledge and requeue delivery: %v\n", err)

					return
				}
			case NackDiscard:
				if err := delivery.Nack(false, false); err != nil {
					fmt.Fprintf(
						os.Stderr,
						"failed to negative acknowledge and discard delivery: %v\n", err)

					return
				}
			}
		}
	}()

	return nil
}

func SubscribeGlob[T any](
	conn *amqp.Connection,
	exchange, queueName, key string,
	qt QueueType,
	handler func(T) AckType,
) error {
	unmarshaller := func(body []byte) (T, error) {
		var buf bytes.Buffer
		buf.Write(body)
		enc := gob.NewDecoder(&buf)

		var rawT T
		err := enc.Decode(&rawT)

		return rawT, err
	}

	return subscribe(
		conn, exchange, queueName, key, qt, handler, unmarshaller)
}

func SubscribeJSON[T any](
	conn *amqp.Connection,
	exchange, queueName, key string,
	qt QueueType,
	handler func(T) AckType,
) error {
	unmarshaller := func(body []byte) (T, error) {
		var rawT T
		err := json.Unmarshal(body, &rawT)

		return rawT, err
	}

	return subscribe(
		conn, exchange, queueName, key, qt, handler, unmarshaller)
}
