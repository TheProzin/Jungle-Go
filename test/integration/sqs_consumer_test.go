//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sqsservice "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
	"github.com/TheProzin/Jungle-Go/internal/config"
	"github.com/TheProzin/Jungle-Go/internal/infra/postgres"
	sqsinfra "github.com/TheProzin/Jungle-Go/internal/infra/sqs"
)

func TestSQSConsumerProcessesAndDeduplicates(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	unitOfWork := postgres.NewUnitOfWork(pool)
	walletService := walletapp.NewService(unitOfWork)
	wageringService := wageringapp.NewServiceWithConfig(unitOfWork, &config.Config{
		ReferenceInitialBackoff: 100 * time.Millisecond,
		ReferenceMaxBackoff:     time.Second,
		ReferenceMaxAttempts:    3,
		ReferenceTTL:            time.Hour,
	})
	playerID := uuid.New()
	wallet := createTestWallet(t, ctx, walletService, playerID, "100.00")

	cfg := localStackConfig()
	client, err := sqsinfra.NewClient(cfg)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	queueURL, dlqURL := newTestQueue(t, client)
	cfg.SQSTransactionsQueueURL = queueURL
	cfg.SQSTransactionsDLQURL = dlqURL

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	consumer := sqsinfra.NewConsumer(client, cfg, wageringService, logger)

	externalID := "sqs-bet-" + uuid.NewString()
	idempotencyKey := "sqs-key-" + uuid.NewString()
	messageID := "sqs-msg-" + uuid.NewString()
	body := envelopeJSON(t, messageID, "provider-a", externalID, idempotencyKey, playerID.String(), wallet.ID().String(), "round-1", "game-1", "BET", "10.00", "")

	sendSQSMessage(t, client, queueURL, wallet.ID().String(), idempotencyKey, body)
	processUntil(t, consumer, 10*time.Second)
	assertProcessedWallet(t, ctx, pool, wallet.ID().String(), 9000)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE external_transaction_id = '`+externalID+`'`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM inbox_messages WHERE status = 'COMPLETED'`, 1)

	sendSQSMessage(t, client, queueURL, wallet.ID().String(), idempotencyKey+"-redelivery", body)
	processUntil(t, consumer, 10*time.Second)
	assertProcessedWallet(t, ctx, pool, wallet.ID().String(), 9000)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE external_transaction_id = '`+externalID+`'`, 1)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wallet_ledger_entries WHERE transaction_id IN (SELECT id FROM wager_transactions WHERE external_transaction_id = '`+externalID+`')`, 1)

	mismatchedBody := envelopeJSON(t, messageID, "provider-a", externalID+"-different", idempotencyKey+"-different", playerID.String(), wallet.ID().String(), "round-1", "game-1", "BET", "11.00", "")
	sendSQSMessage(t, client, queueURL, wallet.ID().String(), idempotencyKey+"-mismatch", mismatchedBody)
	processUntil(t, consumer, 10*time.Second)
	assertProcessedWallet(t, ctx, pool, wallet.ID().String(), 9000)
	assertDLQContains(t, client, dlqURL, externalID+"-different")

	invalidBody := "{invalid-json"
	sendSQSMessage(t, client, queueURL, wallet.ID().String(), idempotencyKey+"-invalid", invalidBody)
	processUntil(t, consumer, 10*time.Second)
	assertDLQContains(t, client, dlqURL, invalidBody)

	secondPlayer := uuid.New()
	secondWallet := createTestWallet(t, ctx, walletService, secondPlayer, "10.00")
	insufficientBody := envelopeJSON(t, "sqs-msg-"+uuid.NewString(), "provider-a", "sqs-insufficient-"+uuid.NewString(), "sqs-insufficient-key-"+uuid.NewString(), secondPlayer.String(), secondWallet.ID().String(), "round-1", "game-1", "BET", "20.00", "")
	sendSQSMessage(t, client, queueURL, secondWallet.ID().String(), "sqs-insufficient-"+uuid.NewString(), insufficientBody)
	processUntil(t, consumer, 10*time.Second)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE status = 'REJECTED' AND provider_id = 'provider-a'`, 1)
	assertProcessedWallet(t, ctx, pool, secondWallet.ID().String(), 1000)

	pendingBody := envelopeJSON(t, "sqs-msg-"+uuid.NewString(), "provider-a", "sqs-pending-"+uuid.NewString(), "sqs-pending-key-"+uuid.NewString(), playerID.String(), wallet.ID().String(), "round-1", "game-1", "REFUND", "10.00", "missing-bet")
	sendSQSMessage(t, client, queueURL, wallet.ID().String(), "sqs-pending-"+uuid.NewString(), pendingBody)
	processUntil(t, consumer, 10*time.Second)
	assertCount(t, ctx, pool, `SELECT COUNT(*) FROM wager_transactions WHERE status = 'PENDING_REFERENCE'`, 1)
	assertProcessedWallet(t, ctx, pool, wallet.ID().String(), 9000)
}

func localStackConfig() *config.Config {
	return &config.Config{
		AWSRegion:                 "us-east-1",
		AWSEndpoint:               "http://localhost:4566",
		AWSAccessKeyID:            "test",
		AWSSecretAccessKey:        "test",
		SQSConsumerMaxMessages:    10,
		SQSConsumerWaitTime:       time.Second,
		SQSConsumerInitialBackoff: 100 * time.Millisecond,
		SQSConsumerMaxBackoff:     time.Second,
	}
}

func newTestQueue(t *testing.T, client *sqsservice.Client) (string, string) {
	t.Helper()
	ctx := context.Background()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]

	dlqName := "test-dlq-" + suffix + ".fifo"
	if _, err := client.CreateQueue(ctx, &sqsservice.CreateQueueInput{
		QueueName:  aws.String(dlqName),
		Attributes: map[string]string{"FifoQueue": "true"},
	}); err != nil {
		t.Fatalf("create dlq: %v", err)
	}
	dlqURL := "http://localhost:4566/000000000000/" + dlqName

	attributes, err := client.GetQueueAttributes(ctx, &sqsservice.GetQueueAttributesInput{
		QueueUrl:       aws.String(dlqURL),
		AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("get dlq arn: %v", err)
	}

	mainName := "test-main-" + suffix + ".fifo"
	redrive := fmt.Sprintf(`{"deadLetterTargetArn":"%s","maxReceiveCount":"5"}`, attributes.Attributes["QueueArn"])
	if _, err := client.CreateQueue(ctx, &sqsservice.CreateQueueInput{
		QueueName: aws.String(mainName),
		Attributes: map[string]string{
			"FifoQueue":                 "true",
			"ContentBasedDeduplication": "false",
			"VisibilityTimeout":         "5",
			"RedrivePolicy":             redrive,
		},
	}); err != nil {
		t.Fatalf("create main queue: %v", err)
	}
	queueURL := "http://localhost:4566/000000000000/" + mainName

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = client.DeleteQueue(cleanupCtx, &sqsservice.DeleteQueueInput{QueueUrl: aws.String(queueURL)})
		_, _ = client.DeleteQueue(cleanupCtx, &sqsservice.DeleteQueueInput{QueueUrl: aws.String(dlqURL)})
	})
	return queueURL, dlqURL
}

func sendSQSMessage(t *testing.T, client *sqsservice.Client, queueURL, groupID, deduplicationID, body string) {
	t.Helper()
	_, err := client.SendMessage(context.Background(), &sqsservice.SendMessageInput{
		QueueUrl:               aws.String(queueURL),
		MessageBody:            aws.String(body),
		MessageGroupId:         aws.String(groupID),
		MessageDeduplicationId: aws.String(deduplicationID),
	})
	if err != nil {
		t.Fatalf("send message: %v", err)
	}
}

func processUntil(t *testing.T, consumer *sqsinfra.Consumer, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		count, err := consumer.ProcessBatch(context.Background())
		if err != nil {
			t.Fatalf("ProcessBatch() error = %v", err)
		}
		if count > 0 {
			return
		}
	}
	t.Fatal("timed out waiting for sqs message")
}

func assertDLQContains(t *testing.T, client *sqsservice.Client, dlqURL, marker string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		output, err := client.ReceiveMessage(context.Background(), &sqsservice.ReceiveMessageInput{
			QueueUrl:            aws.String(dlqURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     1,
		})
		if err != nil {
			t.Fatalf("receive dlq: %v", err)
		}
		for _, message := range output.Messages {
			if strings.Contains(aws.ToString(message.Body), marker) {
				_, _ = client.DeleteMessage(context.Background(), &sqsservice.DeleteMessageInput{
					QueueUrl:      aws.String(dlqURL),
					ReceiptHandle: message.ReceiptHandle,
				})
				return
			}
		}
	}
	t.Fatalf("dlq does not contain marker %q", marker)
}

func envelopeJSON(
	t *testing.T,
	messageID, providerID, externalID, idempotencyKey, playerID, walletID,
	roundID, gameID, kind, amount, reference string,
) string {
	t.Helper()
	data := map[string]any{
		"providerId":            providerID,
		"externalTransactionId": externalID,
		"idempotencyKey":        idempotencyKey,
		"playerId":              playerID,
		"walletId":              walletID,
		"roundId":               roundID,
		"gameId":                gameID,
		"kind":                  kind,
		"money":                 map[string]string{"amount": amount, "currency": "BRL"},
	}
	if reference != "" {
		data["referenceExternalTransactionId"] = reference
	}

	body, err := json.Marshal(map[string]any{
		"messageId":  messageID,
		"type":       "WagerTransactionRequested",
		"occurredAt": time.Now().UTC().Format(time.RFC3339Nano),
		"data":       data,
	})
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return string(body)
}

func assertProcessedWallet(t *testing.T, ctx context.Context, pool *pgxpool.Pool, walletID string, want int64) {
	t.Helper()
	var balance int64
	if err := pool.QueryRow(ctx, `SELECT balance_minor FROM wallets WHERE id = $1`, walletID).Scan(&balance); err != nil {
		t.Fatalf("query wallet: %v", err)
	}
	if balance != want {
		t.Fatalf("balance = %d, want %d", balance, want)
	}
}
