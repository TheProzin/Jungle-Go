package httpapi

import (
	"io"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	sqsservice "github.com/aws/aws-sdk-go-v2/service/sqs"

	"github.com/TheProzin/Jungle-Go/internal/config"
	sqsinfra "github.com/TheProzin/Jungle-Go/internal/infra/sqs"
)

type SQSDevHandler struct {
	client   *sqsservice.Client
	queueURL string
}

func NewSQSDevHandler(client *sqsservice.Client, cfg *config.Config) *SQSDevHandler {
	return &SQSDevHandler{
		client:   client,
		queueURL: cfg.SQSTransactionsQueueURL,
	}
}

func (h *SQSDevHandler) Send(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	envelope, err := sqsinfra.ParseEnvelope(body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}
	command, err := envelope.Command()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
		return
	}

	output, err := h.client.SendMessage(r.Context(), &sqsservice.SendMessageInput{
		QueueUrl:               aws.String(h.queueURL),
		MessageBody:            aws.String(string(body)),
		MessageGroupId:         aws.String(command.WalletID.String()),
		MessageDeduplicationId: aws.String(command.IdempotencyKey),
	})
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "INFRA_UNAVAILABLE", "could not enqueue message")
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{
		"messageId":              aws.ToString(output.MessageId),
		"messageGroupId":         command.WalletID.String(),
		"messageDeduplicationId": command.IdempotencyKey,
	})
}
