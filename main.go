package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"time"

	"github.com/caarlos0/env/v7"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	gogpt "github.com/sashabaranov/go-gpt3"
)

var cfg struct {
	TelegramAPIToken  string  `env:"TELEGRAM_APITOKEN"`
	OpenAIAPIKey      string  `env:"OPENAI_API_KEY"`
	AllowedTelegramID []int64 `env:"ALLOWED_TELEGRAM_ID" envSeparator:","`
}

type User struct {
	TelegramID     int64
	LastActiveTime time.Time
	HistoryMessage []gogpt.ChatCompletionMessage
}

var users = make(map[int64]*User)

func main() {
	if err := env.Parse(&cfg); err != nil {
		fmt.Printf("%+v\n", err)
	}

	bot, err := tgbotapi.NewBotAPI(cfg.TelegramAPIToken)
	if err != nil {
		panic(err)
	}

	// bot.Debug = true

	log.Printf("Authorized on account %s", bot.Self.UserName)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil { // ignore any non-Message updates
			continue
		}

		_, err := bot.Send(tgbotapi.NewChatAction(update.Message.Chat.ID, tgbotapi.ChatTyping))
		if err != nil {
			log.Print(err)
		}

		if len(cfg.AllowedTelegramID) != 0 {
			var userAllowed bool
			for _, allowedID := range cfg.AllowedTelegramID {
				if allowedID == update.Message.Chat.ID {
					userAllowed = true
				}
			}
			if !userAllowed {
				_, err := bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, fmt.Sprintf("You are not allowed to use this bot. User ID: %d", update.Message.Chat.ID)))
				if err != nil {
					log.Print(err)
				}
				continue
			}
		}

		if update.Message.IsCommand() { // ignore any non-command Messages
			// Create a new MessageConfig. We don't have text yet,
			// so we leave it empty.
			msg := tgbotapi.NewMessage(update.Message.Chat.ID, "")

			// Extract the command from the Message.
			switch update.Message.Command() {
			case "start":
				msg.Text = "Welcome to ChatGPT bot! Write something to start a conversation. Use /new to clear context and start a new conversation."
			case "help":
				msg.Text = "Write something to start a conversation. Use /new to clear context and start a new conversation."
			case "new":
				resetUser(update.Message.From.ID)
				msg.Text = "OK, let's start a new conversation."
			default:
				msg.Text = "I don't know that command"
			}

			if _, err := bot.Send(msg); err != nil {
				log.Print(err)
			}
		} else {
			answerText, contextTrimmed, err := handleUserPrompt(update.Message.From.ID, update.Message.Text)
			if err != nil {
				log.Print(err)

				_, err := bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, err.Error()))
				if err != nil {
					log.Print(err)
				}
			} else {
				_, err := bot.Send(tgbotapi.NewMessage(update.Message.Chat.ID, answerText))
				if err != nil {
					log.Print(err)
				}

				if contextTrimmed {
					msg := tgbotapi.NewMessage(update.Message.Chat.ID, "Context trimmed.")
					msg.DisableNotification = true
					_, err = bot.Send(msg)
					if err != nil {
						log.Print(err)
					}
				}
			}
		}
	}
}

func handleUserPrompt(userID int64, msg string) (string, bool, error) {
	if user, ok := users[userID]; ok {
		if user.LastActiveTime.Add(15 * time.Minute).Before(time.Now()) {
			resetUser(userID)
		}
	}

	if _, ok := users[userID]; !ok {
		users[userID] = &User{
			TelegramID:     userID,
			LastActiveTime: time.Now(),
			HistoryMessage: []gogpt.ChatCompletionMessage{},
		}
	}

	users[userID].HistoryMessage = append(users[userID].HistoryMessage, gogpt.ChatCompletionMessage{
		Role:    "user",
		Content: msg,
	})
	users[userID].LastActiveTime = time.Now()

	c := gogpt.NewClient(os.Getenv("OPENAI_API_KEY"))
	ctx := context.Background()

	req := gogpt.ChatCompletionRequest{
		Model:       gogpt.GPT3Dot5Turbo,
		Temperature: 1,
		TopP:        1,
		N:           1,
		// PresencePenalty:  0.2,
		// FrequencyPenalty: 0.2,
		Messages: users[userID].HistoryMessage,
	}

	fmt.Println(req)

	resp, err := c.CreateChatCompletion(ctx, req)
	if err != nil {
		log.Print(err)
		users[userID].HistoryMessage = users[userID].HistoryMessage[:len(users[userID].HistoryMessage)-1]
		return "", false, err
	}

	answer := resp.Choices[0].Message

	users[userID].HistoryMessage = append(users[userID].HistoryMessage, answer)

	var contextTrimmed bool
	if resp.Usage.TotalTokens > 3500 {
		users[userID].HistoryMessage = users[userID].HistoryMessage[1:]
		contextTrimmed = true
	}

	return answer.Content, contextTrimmed, nil
}

func resetUser(userID int64) {
	delete(users, userID)
}
