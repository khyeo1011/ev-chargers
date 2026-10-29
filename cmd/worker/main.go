// Command worker consumes ChargerCreated events from SQS.
//
// Flow: API -> EventBridge (default bus) -> rule -> SQS queue -> this worker.
// Messages that fail 3 times move to the dead-letter queue.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/khyeo1011/ev-chargers/internal/charger"
	"github.com/khyeo1011/ev-chargers/internal/cloud"
)

// envelope is the wrapper EventBridge puts around every event. The SQS
// message body is this JSON, and our charger lives in "detail".
type envelope struct {
	ID         string          `json:"id"`
	Source     string          `json:"source"`
	DetailType string          `json:"detail-type"`
	Time       time.Time       `json:"time"`
	Detail     json.RawMessage `json:"detail"`
}

type worker struct {
	client   *sqs.Client
	queueURL string

	// SQS is "at-least-once": the same message can arrive twice. Handlers
	// must be idempotent. This in-memory set only covers one process; a real
	// service would record processed event IDs in DynamoDB with a
	// conditional write so every replica shares it.
	mu   sync.Mutex
	seen map[string]bool
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	queueURL := os.Getenv("QUEUE_URL")
	if queueURL == "" {
		slog.Error("QUEUE_URL is required")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		slog.Error("load AWS config", "err", err)
		os.Exit(1)
	}

	w := &worker{client: sqs.NewFromConfig(cfg), queueURL: queueURL, seen: map[string]bool{}}
	slog.Info("worker started", "queue", queueURL)
	w.run(ctx)
	slog.Info("worker stopped")
}

func (w *worker) run(ctx context.Context) {
	for ctx.Err() == nil {
		// Long polling: wait up to 20s for messages instead of hammering the
		// API with empty receives. Cheaper and lower latency.
		out, err := w.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(w.queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20,
			VisibilityTimeout:   30,
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Error("receive failed, backing off", "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}

		for _, msg := range out.Messages {
			w.process(msg)
		}
	}
}

func (w *worker) process(msg sqstypes.Message) {
	log := slog.With("messageId", aws.ToString(msg.MessageId))

	if err := w.handle(aws.ToString(msg.Body)); err != nil {
		// Don't delete: the message becomes visible again after the
		// visibility timeout and is retried. After maxReceiveCount (3) SQS
		// moves it to the dead-letter queue for a human to inspect.
		log.Warn("handling failed, will retry", "err", err)
		return
	}

	// Deleting is the "ack". Use a fresh context so a shutdown signal
	// doesn't stop us from acking work we already finished.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := w.client.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl:      aws.String(w.queueURL),
		ReceiptHandle: msg.ReceiptHandle,
	}); err != nil {
		log.Error("delete failed; message will be redelivered", "err", err)
	}
}

func (w *worker) handle(body string) error {
	var env envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		return fmt.Errorf("not an EventBridge event: %w", err)
	}
	if env.DetailType != cloud.DetailTypeChargerCreated {
		slog.Info("ignoring event", "type", env.DetailType)
		return nil
	}

	w.mu.Lock()
	duplicate := w.seen[env.ID]
	w.mu.Unlock()
	if duplicate {
		slog.Info("duplicate event, skipping", "eventId", env.ID)
		return nil
	}

	var c charger.Charger
	if err := json.Unmarshal(env.Detail, &c); err != nil {
		return fmt.Errorf("bad charger payload: %w", err)
	}

	// Demo hook: create a charger named "poison" to watch a message fail,
	// retry, and land in the dead-letter queue.
	if strings.EqualFold(c.Name, "poison") {
		return errors.New("simulated processing failure")
	}

	// Real work would go here: index into OpenSearch for geo search, notify
	// nearby drivers' apps, update a cache, and so on.
	slog.Info("processed new charger",
		"eventId", env.ID, "id", c.ID, "name", c.Name, "powerKw", c.PowerKW,
		"lagMs", time.Since(env.Time).Milliseconds())

	w.mu.Lock()
	w.seen[env.ID] = true
	w.mu.Unlock()
	return nil
}
