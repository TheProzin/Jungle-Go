package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env      string
	Port     int
	LogLevel string

	DatabaseURL string

	AWSRegion          string
	AWSEndpoint        string
	AWSAccessKeyID     string
	AWSSecretAccessKey string

	SQSTransactionsQueueURL   string
	SQSTransactionsDLQURL     string
	SQSEventsQueueURL         string
	SQSConsumerMaxMessages    int
	SQSConsumerWaitTime       time.Duration
	SQSConsumerInitialBackoff time.Duration
	SQSConsumerMaxBackoff     time.Duration
	AllowedProviders          []string

	KeycloakIssuer   string
	KeycloakJWKSURL  string
	KeycloakAudience string

	ShutdownTimeout  time.Duration
	ReadinessTimeout time.Duration

	ReferenceMaxAttempts    int
	ReferenceTTL            time.Duration
	ReferenceInitialBackoff time.Duration
	ReferenceMaxBackoff     time.Duration
	ReferenceWorkerInterval time.Duration

	OutboxBatchSize         int
	OutboxLease             time.Duration
	OutboxInitialBackoff    time.Duration
	OutboxMaxBackoff        time.Duration
	OutboxPublisherInterval time.Duration
}

func Load() (*Config, error) {
	cfg := &Config{
		Env:                       env("APP_ENV", "local"),
		Port:                      envInt("APP_PORT", 8080),
		LogLevel:                  env("LOG_LEVEL", "info"),
		DatabaseURL:               os.Getenv("DATABASE_URL"),
		AWSRegion:                 env("AWS_REGION", "us-east-1"),
		AWSEndpoint:               os.Getenv("AWS_ENDPOINT_URL"),
		AWSAccessKeyID:            os.Getenv("AWS_ACCESS_KEY_ID"),
		AWSSecretAccessKey:        os.Getenv("AWS_SECRET_ACCESS_KEY"),
		SQSTransactionsQueueURL:   os.Getenv("SQS_TRANSACTIONS_QUEUE_URL"),
		SQSTransactionsDLQURL:     os.Getenv("SQS_TRANSACTIONS_DLQ_URL"),
		SQSEventsQueueURL:         os.Getenv("SQS_EVENTS_QUEUE_URL"),
		SQSConsumerMaxMessages:    envInt("SQS_CONSUMER_MAX_MESSAGES", 10),
		SQSConsumerWaitTime:       envDuration("SQS_CONSUMER_WAIT_TIME", 20*time.Second),
		SQSConsumerInitialBackoff: envDuration("SQS_CONSUMER_INITIAL_BACKOFF", time.Second),
		SQSConsumerMaxBackoff:     envDuration("SQS_CONSUMER_MAX_BACKOFF", 5*time.Minute),
		AllowedProviders:          envList("ALLOWED_PROVIDERS", "provider-a,provider-b"),
		KeycloakIssuer:            os.Getenv("KEYCLOAK_ISSUER"),
		KeycloakJWKSURL:           os.Getenv("KEYCLOAK_JWKS_URL"),
		KeycloakAudience:          os.Getenv("KEYCLOAK_AUDIENCE"),
		ShutdownTimeout:           envDuration("SHUTDOWN_TIMEOUT", 20*time.Second),
		ReadinessTimeout:          envDuration("READINESS_TIMEOUT", 2*time.Second),
		ReferenceMaxAttempts:      envInt("REFERENCE_MAX_ATTEMPTS", 10),
		ReferenceTTL:              envDuration("REFERENCE_TTL", time.Hour),
		ReferenceInitialBackoff:   envDuration("REFERENCE_INITIAL_BACKOFF", time.Second),
		ReferenceMaxBackoff:       envDuration("REFERENCE_MAX_BACKOFF", 5*time.Minute),
		ReferenceWorkerInterval:   envDuration("REFERENCE_WORKER_INTERVAL", time.Second),
		OutboxBatchSize:           envInt("OUTBOX_BATCH_SIZE", 20),
		OutboxLease:               envDuration("OUTBOX_LEASE", 30*time.Second),
		OutboxInitialBackoff:      envDuration("OUTBOX_INITIAL_BACKOFF", time.Second),
		OutboxMaxBackoff:          envDuration("OUTBOX_MAX_BACKOFF", 5*time.Minute),
		OutboxPublisherInterval:   envDuration("OUTBOX_PUBLISHER_INTERVAL", time.Second),
	}

	var errs []error
	if cfg.Port < 1 || cfg.Port > 65535 {
		errs = append(errs, fmt.Errorf("APP_PORT must be between 1 and 65535"))
	}
	if cfg.DatabaseURL == "" {
		errs = append(errs, fmt.Errorf("DATABASE_URL is required"))
	}
	if cfg.AWSRegion == "" {
		errs = append(errs, fmt.Errorf("AWS_REGION is required"))
	}
	if cfg.SQSTransactionsQueueURL == "" {
		errs = append(errs, fmt.Errorf("SQS_TRANSACTIONS_QUEUE_URL is required"))
	}
	if cfg.SQSTransactionsDLQURL == "" {
		errs = append(errs, fmt.Errorf("SQS_TRANSACTIONS_DLQ_URL is required"))
	}
	if cfg.SQSEventsQueueURL == "" {
		errs = append(errs, fmt.Errorf("SQS_EVENTS_QUEUE_URL is required"))
	}
	if cfg.KeycloakIssuer == "" {
		errs = append(errs, fmt.Errorf("KEYCLOAK_ISSUER is required"))
	}
	if cfg.KeycloakJWKSURL == "" {
		errs = append(errs, fmt.Errorf("KEYCLOAK_JWKS_URL is required"))
	}
	if cfg.KeycloakAudience == "" {
		errs = append(errs, fmt.Errorf("KEYCLOAK_AUDIENCE is required"))
	}
	if len(cfg.AllowedProviders) == 0 {
		errs = append(errs, fmt.Errorf("ALLOWED_PROVIDERS must contain at least one provider"))
	}
	if cfg.SQSConsumerMaxMessages < 1 || cfg.SQSConsumerMaxMessages > 10 {
		errs = append(errs, fmt.Errorf("SQS_CONSUMER_MAX_MESSAGES must be between 1 and 10"))
	}
	if cfg.SQSConsumerWaitTime < 0 {
		errs = append(errs, fmt.Errorf("SQS_CONSUMER_WAIT_TIME cannot be negative"))
	}
	if cfg.SQSConsumerInitialBackoff <= 0 {
		errs = append(errs, fmt.Errorf("SQS_CONSUMER_INITIAL_BACKOFF must be positive"))
	}
	if cfg.SQSConsumerMaxBackoff < cfg.SQSConsumerInitialBackoff {
		errs = append(errs, fmt.Errorf("SQS_CONSUMER_MAX_BACKOFF must be at least SQS_CONSUMER_INITIAL_BACKOFF"))
	}
	if cfg.ShutdownTimeout <= 0 {
		errs = append(errs, fmt.Errorf("SHUTDOWN_TIMEOUT must be positive"))
	}
	if cfg.ReadinessTimeout <= 0 {
		errs = append(errs, fmt.Errorf("READINESS_TIMEOUT must be positive"))
	}
	if cfg.ReferenceMaxAttempts < 1 {
		errs = append(errs, fmt.Errorf("REFERENCE_MAX_ATTEMPTS must be at least 1"))
	}
	if cfg.ReferenceTTL <= 0 {
		errs = append(errs, fmt.Errorf("REFERENCE_TTL must be positive"))
	}
	if cfg.ReferenceInitialBackoff <= 0 {
		errs = append(errs, fmt.Errorf("REFERENCE_INITIAL_BACKOFF must be positive"))
	}
	if cfg.ReferenceMaxBackoff < cfg.ReferenceInitialBackoff {
		errs = append(errs, fmt.Errorf("REFERENCE_MAX_BACKOFF must be at least REFERENCE_INITIAL_BACKOFF"))
	}
	if cfg.ReferenceWorkerInterval <= 0 {
		errs = append(errs, fmt.Errorf("REFERENCE_WORKER_INTERVAL must be positive"))
	}
	if cfg.OutboxBatchSize < 1 || cfg.OutboxBatchSize > 100 {
		errs = append(errs, fmt.Errorf("OUTBOX_BATCH_SIZE must be between 1 and 100"))
	}
	if cfg.OutboxLease <= 0 {
		errs = append(errs, fmt.Errorf("OUTBOX_LEASE must be positive"))
	}
	if cfg.OutboxInitialBackoff <= 0 {
		errs = append(errs, fmt.Errorf("OUTBOX_INITIAL_BACKOFF must be positive"))
	}
	if cfg.OutboxMaxBackoff < cfg.OutboxInitialBackoff {
		errs = append(errs, fmt.Errorf("OUTBOX_MAX_BACKOFF must be at least OUTBOX_INITIAL_BACKOFF"))
	}
	if cfg.OutboxPublisherInterval <= 0 {
		errs = append(errs, fmt.Errorf("OUTBOX_PUBLISHER_INTERVAL must be positive"))
	}

	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envList(key, fallback string) []string {
	value := os.Getenv(key)
	if value == "" {
		value = fallback
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}
