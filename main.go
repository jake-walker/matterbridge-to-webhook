package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
)

const logFatal = slog.Level(13)

type Message struct {
	Text      string `json:"text"`
	Channel   string `json:"channel"`
	Username  string `json:"username"`
	Userid    string `json:"userid"`
	Avatar    string `json:"avatar"`
	Account   string `json:"account"`
	Event     string `json:"event"`
	Protocol  string `json:"protocol"`
	Gateway   string `json:"gateway"`
	ParentId  string `json:"parent_id"`
	Timestamp string `json:"timestamp"`
	Id        string `json:"id"`
}

func processMessages(webhookUrl string, messagePrefix string, c chan Message) {
	for {
		msg := <-c

		if messagePrefix != "" && !strings.HasPrefix(msg.Text, messagePrefix) {
			continue
		}

		slog.Debug("received message", "message", msg)

		msgBytes, err := json.Marshal([]Message{msg})
		if err != nil {
			slog.Warn("failed to marshal message", "message", msg, slog.Any("error", err))
			continue
		}

		req, err := http.NewRequest("POST", webhookUrl, bytes.NewBuffer(msgBytes))
		if err != nil {
			slog.Warn("failed to build request", "message", msg, slog.Any("error", err))
			continue
		}

		req.Header.Set("Content-Type", "application/json")

		_, err = http.DefaultClient.Do(req)
		if err != nil {
			slog.Warn("failed to send webhook", "message", msg, slog.Any("error", err))
			continue
		}
	}
}

func getMessages(apiUrl string, username string, password string, b backoff.BackOff, c chan Message) error {
	url, err := url.JoinPath(apiUrl, "/api/stream")
	if err != nil {
		return backoff.Permanent(fmt.Errorf("failed to build url: %v", err))
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return backoff.Permanent(fmt.Errorf("failed to build request: %v", err))
	}

	if username != "" && password != "" {
		req.Header.Set(
			"Authorization",
			fmt.Sprintf("Basic %s", base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("%s:%s", username, password)))),
		)
	}

	res, err := http.DefaultClient.Do(req)

	if err != nil {
		return fmt.Errorf("failed to request messages: %v", err)
	}

	slog.Info("listening for messages...")

	reader := bufio.NewReader(res.Body)
	for {
		line, err := reader.ReadBytes('\n')

		if err != nil {
			return fmt.Errorf("failed to read messages: %v", err)
		}

		msg := Message{}
		err = json.Unmarshal(line, &msg)

		if err != nil {
			slog.Warn("failed to unmarshal message, skipping", "message", string(line), "error", err)
			continue
		}

		if msg.Event != "" {
			slog.Info(fmt.Sprintf("received %s event", msg.Event))
			continue
		}

		c <- msg
		// reset the backoff function if we receive a proper message
		b.Reset()
	}
}

func main() {
	apiUrl := os.Getenv("MATTERBRIDGE_API_URL")
	username := os.Getenv("MATTERBRIDGE_API_USERNAME")
	password := os.Getenv("MATTERBRIDGE_API_PASSWORD")
	webhookUrl := os.Getenv("WEBHOOK_URL")
	messagePrefix := os.Getenv("MESSAGE_PREFIX")

	if apiUrl == "" || webhookUrl == "" {
		slog.Log(nil, logFatal, "the api and webhook urls must be set")
		os.Exit(1)
	}

	messages := make(chan Message)

	go processMessages(webhookUrl, messagePrefix, messages)

	b := backoff.NewExponentialBackOff()

	err := backoff.RetryNotify(func() error {
		return getMessages(apiUrl, username, password, b, messages)
	}, b, func(err error, d time.Duration) {
		slog.Warn("get messages failed", "error", err, "retry", d.String())
	})

	if err != nil {
		slog.Log(nil, logFatal, "failed to get messages", slog.Any("error", err))
		os.Exit(1)
	}
}
