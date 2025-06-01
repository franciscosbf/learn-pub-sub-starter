package main

import (
	"fmt"
	"os"

	"github.com/bootdotdev/learn-pub-sub-starter/internal/gamelogic"
	"github.com/bootdotdev/learn-pub-sub-starter/internal/pubsub"
	"github.com/bootdotdev/learn-pub-sub-starter/internal/routing"
	amqp "github.com/rabbitmq/amqp091-go"
)

func handlerLog() func(routing.GameLog) pubsub.AckType {
	return func(gameLog routing.GameLog) pubsub.AckType {
		defer fmt.Print("> ")

		if err := gamelogic.WriteLog(gameLog); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to record log entry: %v\n", err)

			return pubsub.NackDiscard
		}

		return pubsub.Ack
	}
}

func main() {
	fmt.Println("Starting Peril server...")

	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to stablish connection: %v\n", err)

		return
	}
	defer conn.Close()

	fmt.Println("Connection stablished to RabbitMQ server")

	channel, err := conn.Channel()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create channel: %v\n", err)

		return
	}
	channel.Qos(10, 0, false)

	{
		exchange := routing.ExchangePerilTopic
		gameLogSlug := routing.GameLogSlug
		queueName := gameLogSlug
		key := gameLogSlug + ".*"
		if err := pubsub.SubscribeGlob(
			conn, exchange, queueName, key, pubsub.DurableQueue, handlerLog(),
		); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to subscribe to logs: %v\n", err)

			return
		}
	}

	// sigs := make(chan os.Signal, 1)
	// signal.Notify(sigs, syscall.SIGINT)
	// <-sigs
	// _ = channel

	gamelogic.PrintServerHelp()

	exchange := routing.ExchangePerilDirect
	paused := false
	for {
		input := gamelogic.GetInput()
		if len(input) == 0 {
			continue
		}

		switch input[0] {
		case "help":
			gamelogic.PrintServerHelp()

		case "pause":
			if paused {
				fmt.Println("Game is already in pause state...")

				break
			}

			fmt.Println("Pausing game...")

			key := routing.PauseKey
			val := routing.PlayingState{IsPaused: true}
			if err := pubsub.PublishJSON(channel, exchange, key, val); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to pause game: %v\n", err)

				return
			}

			paused = true
		case "resume":
			if !paused {
				fmt.Println("Game isn't paused...")

				break
			}

			fmt.Println("Resuming game...")

			key := routing.PauseKey
			val := routing.PlayingState{IsPaused: false}
			if err := pubsub.PublishJSON(channel, exchange, key, val); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to resume game: %v\n", err)

				return
			}

			paused = false
		case "quit":
			fmt.Println("Stopping Peril server...")

			return
		default:
			fmt.Println("I do not understand gibberish")
		}
	}
}
