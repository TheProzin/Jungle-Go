package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sqsservice "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/TheProzin/Jungle-Go/internal/config"
)

type Health struct {
	pool     *pgxpool.Pool
	sqs      *sqsservice.Client
	queueURL string
	timeout  time.Duration
	logger   *slog.Logger
}

func NewHealth(
	pool *pgxpool.Pool,
	sqsClient *sqsservice.Client,
	cfg *config.Config,
	logger *slog.Logger,
) *Health {
	return &Health{
		pool:     pool,
		sqs:      sqsClient,
		queueURL: cfg.SQSTransactionsQueueURL,
		timeout:  cfg.ReadinessTimeout,
		logger:   logger,
	}
}

func (h *Health) Live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Health) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), h.timeout)
	defer cancel()

	checks := map[string]string{
		"postgres": "ok",
		"sqs":      "ok",
	}
	status := http.StatusOK

	var mu sync.Mutex
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if err := h.pool.Ping(ctx); err != nil {
			mu.Lock()
			checks["postgres"] = "unavailable"
			status = http.StatusServiceUnavailable
			mu.Unlock()
			h.logger.Warn("readiness check failed", "dependency", "postgres", "error", err)
		}
	}()

	go func() {
		defer wg.Done()
		_, err := h.sqs.GetQueueAttributes(ctx, &sqsservice.GetQueueAttributesInput{
			QueueUrl:       aws.String(h.queueURL),
			AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameApproximateNumberOfMessages},
		})
		if err != nil {
			mu.Lock()
			checks["sqs"] = "unavailable"
			status = http.StatusServiceUnavailable
			mu.Unlock()
			h.logger.Warn("readiness check failed", "dependency", "sqs", "error", err)
		}
	}()

	wg.Wait()

	body := map[string]any{
		"status": "ready",
		"checks": checks,
	}
	if status != http.StatusOK {
		body["status"] = "unavailable"
	}
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Default().Error("encode response", "error", err)
	}
}
