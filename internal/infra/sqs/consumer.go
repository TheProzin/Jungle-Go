package sqs

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sqsservice "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"

	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
	"github.com/TheProzin/Jungle-Go/internal/config"
	domainwagering "github.com/TheProzin/Jungle-Go/internal/domain/wagering"
	domainwallet "github.com/TheProzin/Jungle-Go/internal/domain/wallet"
)

const consumerName = "wager-transactions-consumer"

type Consumer struct {
	client         *sqsservice.Client
	queueURL       string
	dlqURL         string
	service        *wageringapp.Service
	logger         *slog.Logger
	maxMessages    int32
	waitTime       int32
	initialBackoff time.Duration
	maxBackoff     time.Duration
}

func NewConsumer(
	client *sqsservice.Client,
	cfg *config.Config,
	service *wageringapp.Service,
	logger *slog.Logger,
) *Consumer {
	waitTime := int32(cfg.SQSConsumerWaitTime.Seconds())
	if waitTime < 0 {
		waitTime = 0
	}
	if waitTime > 20 {
		waitTime = 20
	}

	return &Consumer{
		client:         client,
		queueURL:       cfg.SQSTransactionsQueueURL,
		dlqURL:         cfg.SQSTransactionsDLQURL,
		service:        service,
		logger:         logger,
		maxMessages:    int32(cfg.SQSConsumerMaxMessages),
		waitTime:       waitTime,
		initialBackoff: cfg.SQSConsumerInitialBackoff,
		maxBackoff:     cfg.SQSConsumerMaxBackoff,
	}
}

func (c *Consumer) Run(ctx context.Context) {
	for {
		if _, err := c.ProcessBatch(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Error("sqs receive failed", "error", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}
}

func (c *Consumer) ProcessBatch(ctx context.Context) (int, error) {
	output, err := c.client.ReceiveMessage(ctx, &sqsservice.ReceiveMessageInput{
		QueueUrl:                    aws.String(c.queueURL),
		MaxNumberOfMessages:         c.maxMessages,
		WaitTimeSeconds:             c.waitTime,
		MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll},
	})
	if err != nil {
		return 0, err
	}

	for _, message := range output.Messages {
		c.handleMessage(ctx, message)
	}
	return len(output.Messages), nil
}

func (c *Consumer) handleMessage(ctx context.Context, message types.Message) {
	body := aws.ToString(message.Body)
	envelope, err := ParseEnvelope([]byte(body))
	if err != nil {
		c.logger.Error(
			"invalid sqs message",
			"sqsMessageId", aws.ToString(message.MessageId),
			"error", err,
		)
		c.sendToDLQAndDelete(message, err.Error())
		return
	}

	command, err := envelope.Command()
	if err != nil {
		c.logger.Error(
			"invalid sqs message data",
			"messageId", envelope.MessageID,
			"sqsMessageId", aws.ToString(message.MessageId),
			"error", err,
		)
		c.markFailed(envelope.MessageID, err.Error())
		c.sendToDLQAndDelete(message, err.Error())
		return
	}

	logger := c.logger.With(
		"messageId", envelope.MessageID,
		"sqsMessageId", aws.ToString(message.MessageId),
		"correlationId", command.CorrelationID,
		"providerId", command.ProviderID,
		"walletId", command.WalletID.String(),
	)

	result, err := c.service.ProcessTransactionWithInbox(
		ctx,
		command,
		wageringapp.InboxIdentity{
			ConsumerName: consumerName,
			MessageID:    envelope.MessageID,
		},
	)

	switch {
	case errors.Is(err, wageringapp.ErrDuplicateMessage):
		logger.Info("duplicate message ignored")
		c.deleteMessage(message)
		return
	case errors.Is(err, wageringapp.ErrInboxHashMismatch):
		logger.Error("inbox payload hash mismatch")
		c.markFailed(envelope.MessageID, "inbox payload hash mismatch")
		c.sendToDLQAndDelete(message, "inbox payload hash mismatch")
		return
	case err != nil:
		if ctx.Err() != nil {
			c.releaseMessage(message)
			return
		}
		if isPermanent(err) {
			logger.Error("permanent message failure", "error", err)
			c.markFailed(envelope.MessageID, err.Error())
			c.sendToDLQAndDelete(message, err.Error())
			return
		}
		logger.Error("transient message failure", "error", err)
		c.backoffMessage(message)
		return
	}

	fields := []any{
		"transactionId", result.Transaction.ID().String(),
		"status", string(result.Transaction.Status()),
	}
	if failureCode := result.Transaction.FailureCode(); failureCode != nil {
		fields = append(fields, "failureCode", *failureCode)
	}
	logger.Info("message processed", fields...)
	c.deleteMessage(message)
}

func isPermanent(err error) bool {
	switch {
	case errors.Is(err, wageringapp.ErrInvalidCommand),
		errors.Is(err, wageringapp.ErrReferenceRequired),
		errors.Is(err, domainwagering.ErrInvalidTransaction),
		errors.Is(err, domainwagering.ErrUnsupportedKind),
		errors.Is(err, domainwagering.ErrIdempotencyKeyConflict),
		errors.Is(err, domainwagering.ErrExternalTransactionConflict),
		errors.Is(err, domainwagering.ErrDuplicateReversal),
		errors.Is(err, domainwallet.ErrWalletNotFound):
		return true
	default:
		return false
	}
}

func (c *Consumer) markFailed(messageID, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := c.service.MarkInboxFailed(ctx, consumerName, messageID, reason); err != nil {
		c.logger.Error("mark inbox failed", "messageId", messageID, "error", err)
	}
}

func (c *Consumer) deleteMessage(message types.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.client.DeleteMessage(ctx, &sqsservice.DeleteMessageInput{
		QueueUrl:      aws.String(c.queueURL),
		ReceiptHandle: message.ReceiptHandle,
	})
	if err != nil {
		c.logger.Error("delete sqs message", "sqsMessageId", aws.ToString(message.MessageId), "error", err)
	}
}

func (c *Consumer) releaseMessage(message types.Message) {
	c.changeVisibility(message, 0)
}

func (c *Consumer) backoffMessage(message types.Message) {
	count := receiveCount(message)
	c.changeVisibility(message, int32(c.backoffFor(count).Seconds()))
}

func (c *Consumer) changeVisibility(message types.Message, seconds int32) {
	if seconds < 0 {
		seconds = 0
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.client.ChangeMessageVisibility(ctx, &sqsservice.ChangeMessageVisibilityInput{
		QueueUrl:          aws.String(c.queueURL),
		ReceiptHandle:     message.ReceiptHandle,
		VisibilityTimeout: seconds,
	})
	if err != nil {
		c.logger.Error("change sqs visibility", "sqsMessageId", aws.ToString(message.MessageId), "error", err)
	}
}

func (c *Consumer) sendToDLQAndDelete(message types.Message, reason string) {
	groupID := message.Attributes["MessageGroupId"]
	if groupID == "" {
		groupID = "invalid-message"
	}
	deduplicationID := aws.ToString(message.MessageId)
	if deduplicationID == "" {
		deduplicationID = uuid.NewString()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.client.SendMessage(ctx, &sqsservice.SendMessageInput{
		QueueUrl:               aws.String(c.dlqURL),
		MessageBody:            message.Body,
		MessageGroupId:         aws.String(groupID),
		MessageDeduplicationId: aws.String(deduplicationID),
	})
	if err != nil {
		c.logger.Error(
			"send message to dlq",
			"sqsMessageId", aws.ToString(message.MessageId),
			"reason", reason,
			"error", err,
		)
		c.releaseMessage(message)
		return
	}

	c.deleteMessage(message)
}

func (c *Consumer) backoffFor(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	delay := c.initialBackoff
	for i := 1; i < attempt; i++ {
		if delay >= c.maxBackoff/2 {
			return c.maxBackoff
		}
		delay *= 2
	}
	if delay > c.maxBackoff {
		return c.maxBackoff
	}
	if delay < time.Second {
		return time.Second
	}
	return delay
}

func receiveCount(message types.Message) int {
	raw := message.Attributes["ApproximateReceiveCount"]
	count, err := strconv.Atoi(raw)
	if err != nil || count < 1 {
		return 1
	}
	return count
}
