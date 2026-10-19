package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry *prometheus.Registry

	transactions              *prometheus.CounterVec
	idempotentReplays         prometheus.Counter
	idempotencyConflicts      prometheus.Counter
	concurrencyConflicts      prometheus.Counter
	sqsMessages               *prometheus.CounterVec
	sqsRetries                prometheus.Counter
	dlqMessages               prometheus.Counter
	outboxPending             prometheus.Gauge
	outboxLatency             prometheus.Histogram
	outboxFailures            prometheus.Counter
	referencePending          prometheus.Gauge
	referenceAttempts         prometheus.Counter
	reconciliationDivergences prometheus.Counter
	requestDuration           *prometheus.HistogramVec
}

var Default = NewMetrics()

func NewMetrics() *Metrics {
	registry := prometheus.NewRegistry()

	metrics := &Metrics{
		registry: registry,
		transactions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_transactions_total",
			Help: "Total of processed wagering transactions by status and kind.",
		}, []string{"status", "kind"}),
		idempotentReplays: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_idempotent_replays_total",
			Help: "Total of idempotent replays.",
		}),
		idempotencyConflicts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_idempotency_conflicts_total",
			Help: "Total of idempotency conflicts.",
		}),
		concurrencyConflicts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_concurrency_conflicts_total",
			Help: "Total of concurrency conflicts.",
		}),
		sqsMessages: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_sqs_messages_total",
			Help: "Total of SQS messages by result.",
		}, []string{"result"}),
		sqsRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_sqs_retries_total",
			Help: "Total of SQS retries.",
		}),
		dlqMessages: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_dlq_messages_total",
			Help: "Total of messages sent to the DLQ.",
		}),
		outboxPending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wager_outbox_pending",
			Help: "Number of pending outbox events.",
		}),
		outboxLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "wager_outbox_publish_latency_seconds",
			Help:    "Latency between event occurrence and publication.",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 12),
		}),
		outboxFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_outbox_publish_failures_total",
			Help: "Total of outbox publication failures.",
		}),
		referencePending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "wager_reference_pending",
			Help: "Number of pending reference transactions.",
		}),
		referenceAttempts: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_reference_attempts_total",
			Help: "Total of reference resolution attempts.",
		}),
		reconciliationDivergences: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "wager_reconciliation_divergences_total",
			Help: "Total of reconciliation divergences.",
		}),
		requestDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "wager_request_duration_seconds",
			Help:    "HTTP request duration by method, route and status.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route", "status"}),
	}

	registry.MustRegister(
		metrics.transactions,
		metrics.idempotentReplays,
		metrics.idempotencyConflicts,
		metrics.concurrencyConflicts,
		metrics.sqsMessages,
		metrics.sqsRetries,
		metrics.dlqMessages,
		metrics.outboxPending,
		metrics.outboxLatency,
		metrics.outboxFailures,
		metrics.referencePending,
		metrics.referenceAttempts,
		metrics.reconciliationDivergences,
		metrics.requestDuration,
	)

	return metrics
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) RecordTransaction(status, kind string) {
	m.transactions.WithLabelValues(status, kind).Inc()
}

func (m *Metrics) RecordIdempotentReplay() {
	m.idempotentReplays.Inc()
}

func (m *Metrics) RecordIdempotencyConflict() {
	m.idempotencyConflicts.Inc()
}

func (m *Metrics) RecordConcurrencyConflict() {
	m.concurrencyConflicts.Inc()
}

func (m *Metrics) RecordSQSMessage(result string) {
	m.sqsMessages.WithLabelValues(result).Inc()
}

func (m *Metrics) RecordSQSRetry() {
	m.sqsRetries.Inc()
}

func (m *Metrics) RecordDLQMessage() {
	m.dlqMessages.Inc()
}

func (m *Metrics) SetOutboxPending(count int) {
	m.outboxPending.Set(float64(count))
}

func (m *Metrics) RecordOutboxPublishLatency(latency time.Duration) {
	m.outboxLatency.Observe(latency.Seconds())
}

func (m *Metrics) RecordOutboxFailure() {
	m.outboxFailures.Inc()
}

func (m *Metrics) SetReferencePending(count int) {
	m.referencePending.Set(float64(count))
}

func (m *Metrics) RecordReferenceAttempt() {
	m.referenceAttempts.Inc()
}

func (m *Metrics) RecordReconciliationDivergence() {
	m.reconciliationDivergences.Inc()
}

func (m *Metrics) RecordRequest(method, route string, status int, duration time.Duration) {
	m.requestDuration.WithLabelValues(method, route, strconv.Itoa(status)).Observe(duration.Seconds())
}
