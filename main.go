package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	slogmulti "github.com/samber/slog-multi"
	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel"
)

const name = "github.com/jake-walker/matterbridge-to-webhook"
const logFatal = slog.Level(13)

var (
	meter   = otel.Meter(name)
	metrics Metrics
)

// message object received from matterbridge
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

		// if a message prefix is set, and the message doesn't begin with it, stop processing
		if messagePrefix != "" && !strings.HasPrefix(msg.Text, messagePrefix) {
			metrics.messageDropped.Add(context.Background(), 1)
			slog.Debug("skipping message without prefix", "message", msg)
			continue
		}

		// parse the message
		msgBytes, err := json.Marshal([]Message{msg})
		if err != nil {
			metrics.processingError.Add(context.Background(), 1)
			slog.Warn("failed to marshal message", "message", msg, slog.Any("error", err))
			continue
		}

		// build a post request to the output webhook
		req, err := http.NewRequest("POST", webhookUrl, bytes.NewBuffer(msgBytes))
		if err != nil {
			metrics.processingError.Add(context.Background(), 1)
			slog.Warn("failed to build request", "message", msg, slog.Any("error", err))
			continue
		}

		req.Header.Set("Content-Type", "application/json")

		// perform request to webhook
		_, err = http.DefaultClient.Do(req)
		if err != nil {
			metrics.processingError.Add(context.Background(), 1)
			slog.Warn("failed to send webhook", "message", msg, slog.Any("error", err))
			continue
		}

		slog.Debug("forwarded message successfully")
		metrics.messageForwarded.Add(context.Background(), 1)
	}
}

func getMessages(apiUrl string, username string, password string, b backoff.BackOff, c chan Message) error {
	// create a request to the matterbridge api
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

	// loop over any messages received
	reader := bufio.NewReader(res.Body)
	for {
		line, err := reader.ReadBytes('\n')

		if err != nil {
			return fmt.Errorf("failed to read messages: %v", err)
		}

		msg := Message{}
		err = json.Unmarshal(line, &msg)

		if err != nil {
			metrics.processingError.Add(context.Background(), 1)
			slog.Warn("failed to unmarshal message, skipping", "message", string(line), "error", err)
			continue
		}

		if msg.Event != "" {
			slog.Info(fmt.Sprintf("received %s event", msg.Event))
			continue
		}

		slog.Debug("received message", "message", msg)
		// send the message to the channel to get sent to webhook
		c <- msg
		metrics.messageReceived.Add(context.Background(), 1)
		// reset the backoff function if we receive a proper message
		b.Reset()
	}
}

func main() {
	// setup logger to forward logs to stdout and opentelemetry
	slog.SetDefault(slog.New(slogmulti.Fanout(
		otelslog.NewHandler("main"),
		slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelInfo,
		}),
	)))

	if err := run(); err != nil {
		slog.Log(context.Background(), logFatal, "failed to run", "error", err)
		os.Exit(1)
	}
}

func init() {
	// setup opentelemetry metrics
	var err error
	metrics, err = initMetrics(meter)
	if err != nil {
		slog.Log(context.Background(), logFatal, "failed to create metrics", "error", err)
		os.Exit(1)
	}
}

func run() (err error) {
	apiUrl := os.Getenv("MATTERBRIDGE_API_URL")
	username := os.Getenv("MATTERBRIDGE_API_USERNAME")
	password := os.Getenv("MATTERBRIDGE_API_PASSWORD")
	webhookUrl := os.Getenv("WEBHOOK_URL")
	messagePrefix := os.Getenv("MESSAGE_PREFIX")
	enableTelemetry := os.Getenv("ENABLE_TELEMETRY") == "yes"

	if apiUrl == "" || webhookUrl == "" {
		err = errors.Join(err, fmt.Errorf("the api and webhook urls must be set"))
		return
	}

	ctx := context.Background()

	// initialize opentelemetry sdk
	if enableTelemetry {
		slog.Debug("setting up telemetry...")
		otelShutdown, err := setupOTelSdk(ctx)
		if err != nil {
			return err
		}
		defer func() {
			err = errors.Join(err, otelShutdown(context.Background()))
		}()
	}

	messages := make(chan Message)

	// start processing messages from the channel in the background
	go processMessages(webhookUrl, messagePrefix, messages)

	b := backoff.NewExponentialBackOff()

	// retry loop for listening for messages from matterbridge
	backoffErr := backoff.RetryNotify(func() error {
		return getMessages(apiUrl, username, password, b, messages)
	}, b, func(err error, d time.Duration) {
		slog.Warn("get messages failed", "error", err, "retry", d.String())
	})

	if backoffErr != nil {
		err = errors.Join(err, fmt.Errorf("failed to get messages: %v", backoffErr))
	}
	return
}
