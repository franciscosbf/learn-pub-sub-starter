package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/bootdotdev/learn-pub-sub-starter/internal/gamelogic"
	"github.com/bootdotdev/learn-pub-sub-starter/internal/pubsub"
	"github.com/bootdotdev/learn-pub-sub-starter/internal/routing"
	amqp "github.com/rabbitmq/amqp091-go"
)

func handlerPause(
	gameState *gamelogic.GameState,
) func(routing.PlayingState) pubsub.AckType {
	return func(playingState routing.PlayingState) pubsub.AckType {
		defer fmt.Print("> ")

		gameState.HandlePause(playingState)

		return pubsub.Ack
	}
}

func handlerMove(
	channel *amqp.Channel,
	gameState *gamelogic.GameState,
) func(gamelogic.ArmyMove) pubsub.AckType {
	return func(move gamelogic.ArmyMove) pubsub.AckType {
		defer fmt.Print("> ")

		switch gameState.HandleMove(move) {
		case gamelogic.MoveOutComeSafe:
			return pubsub.Ack
		case gamelogic.MoveOutcomeMakeWar:
			exchange := routing.ExchangePerilTopic
			warRecognitions := routing.WarRecognitionsPrefix
			key := warRecognitions + "." + move.Player.Username
			val := gamelogic.RecognitionOfWar{
				Attacker: move.Player,
				Defender: gameState.Player,
			}
			if err := pubsub.PublishJSON(channel, exchange, key, val); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to publish move: %v\n", err)

				return pubsub.NackRequeue
			}

			return pubsub.Ack
		case gamelogic.MoveOutcomeSamePlayer:
			return pubsub.NackDiscard
		default:
			return pubsub.NackDiscard
		}
	}
}

func handleWar(
	channel *amqp.Channel,
	gameState *gamelogic.GameState,
) func(gamelogic.RecognitionOfWar) pubsub.AckType {
	return func(
		recognitionOfWar gamelogic.RecognitionOfWar,
	) pubsub.AckType {
		defer fmt.Print("> ")

		warOutcome, winner, loser := gameState.HandleWar(recognitionOfWar)
		switch warOutcome {
		case gamelogic.WarOutcomeNotInvolved:
			return pubsub.NackRequeue
		case gamelogic.WarOutcomeNoUnits:
			return pubsub.NackDiscard
		case gamelogic.WarOutcomeOpponentWon, gamelogic.WarOutcomeYouWon, gamelogic.WarOutcomeDraw:
			exchange := routing.ExchangePerilTopic
			gameLogSlug := routing.GameLogSlug
			key := gameLogSlug + "." + recognitionOfWar.Attacker.Username
			var message string
			switch warOutcome {
			case gamelogic.WarOutcomeOpponentWon, gamelogic.WarOutcomeYouWon:
				message = fmt.Sprintf("%v won an war against %v", winner, loser)
			case gamelogic.WarOutcomeDraw:
				message = fmt.Sprintf(
					"A war between %v and %v resulted in a draw",
					winner, loser)
			}
			val := routing.GameLog{
				CurrentTime: time.Now(),
				Message:     message,
				Username:    gameState.GetUsername(),
			}
			if err := pubsub.PublishGob(channel, exchange, key, val); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to publish log: %v\n", err)

				return pubsub.NackRequeue
			}

			return pubsub.Ack
		default:
			fmt.Fprintln(os.Stderr, "Unrecognized war outcome")

			return pubsub.NackDiscard
		}
	}
}

func main() {
	fmt.Println("Starting Peril client...")

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

	username, err := gamelogic.ClientWelcome()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to prompt username: %v\n", err)

		return
	}

	gameState := gamelogic.NewGameState(username)

	{
		pauseKey := routing.PauseKey
		key := pauseKey
		queueName := pauseKey + "." + username
		exchange := routing.ExchangePerilDirect

		if err := pubsub.SubscribeJSON(
			conn, exchange, queueName, key, pubsub.TransientQueue, handlerPause(gameState),
		); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to subscribe to pausing state: %v\n", err)

			return
		}
	}

	{
		armyMoves := routing.ArmyMovesPrefix
		queueName := armyMoves + "." + username
		key := armyMoves + ".*"
		exchange := routing.ExchangePerilTopic
		if err := pubsub.SubscribeJSON(
			conn, exchange, queueName, key, pubsub.TransientQueue, handlerMove(channel, gameState),
		); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to subscribe to movces state: %v\n", err)

			return
		}
	}

	{
		exchange := routing.ExchangePerilTopic
		queueName := "war"
		warRecognitions := routing.WarRecognitionsPrefix
		key := warRecognitions + ".*"
		if err := pubsub.SubscribeJSON(
			conn, exchange, queueName, key, pubsub.DurableQueue, handleWar(channel, gameState),
		); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to subscribe to movces state: %v\n", err)

			return
		}
	}

	for {
		input := gamelogic.GetInput()
		if len(input) == 0 {
			continue
		}

		switch input[0] {
		case "spawn":
			if err := gameState.CommandSpawn(input); err != nil {
				fmt.Printf("Failed to spawn unit: %v\n", err)
			}
		case "move":
			move, err := gameState.CommandMove(input)
			if err != nil {
				fmt.Printf("Failed to issue move: %v\n", err)

				break
			}

			exchange := routing.ExchangePerilTopic
			armyMoves := routing.ArmyMovesPrefix
			key := armyMoves + "." + username
			val := move
			if err := pubsub.PublishJSON(channel, exchange, key, val); err != nil {
				fmt.Fprintf(os.Stderr, "Failed to publish move: %v\n", err)

				return
			}

			fmt.Println("Move published with success")
			_ = move
		case "status":
			gameState.CommandStatus()
		case "spam":
			if len(input) < 2 {
				fmt.Println("usage: move <n>")

				break
			}

			n, err := strconv.Atoi(input[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "invalid number: %v\n", err)

				break
			} else if n < 0 {
				fmt.Fprintf(os.Stderr, "number must be greater than zero\n")

				break
			}

			exchange := routing.ExchangePerilTopic
			gameLogSlug := routing.GameLogSlug
			key := gameLogSlug + "." + gameState.GetUsername()
			for range n {
				val := routing.GameLog{
					CurrentTime: time.Now(),
					Message:     gamelogic.GetMaliciousLog(),
					Username:    gameState.GetUsername(),
				}
				if err := pubsub.PublishGob(channel, exchange, key, val); err != nil {
					fmt.Fprintf(os.Stderr, "Failed to publish log: %v\n", err)

					return
				}
			}
		case "help":
			gamelogic.PrintClientHelp()
		case "quit":
			gamelogic.PrintQuit()

			fmt.Println("Stopping Peril client...")

			return
		default:
			fmt.Println("I do not understand gibberish")
		}
	}
}
