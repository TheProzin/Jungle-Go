package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/TheProzin/Jungle-Go/internal/observability"
)

const correlationContextKey contextKey = "correlationId"

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(body)
}

func observe(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		correlationID := r.Header.Get("X-Correlation-Id")
		if correlationID == "" {
			correlationID = uuid.NewString()
		}
		w.Header().Set("X-Correlation-Id", correlationID)

		ctx := context.WithValue(r.Context(), correlationContextKey, correlationID)
		writer := &statusWriter{ResponseWriter: w}

		next(writer, r.WithContext(ctx))

		status := writer.status
		if status == 0 {
			status = http.StatusOK
		}
		duration := time.Since(start)

		observability.Default.RecordRequest(r.Method, route, status, duration)
		slog.Default().Info(
			"http request",
			"method", r.Method,
			"route", route,
			"status", status,
			"durationMs", duration.Milliseconds(),
			"correlationId", correlationID,
		)
	}
}

func CorrelationIDFromContext(ctx context.Context) string {
	value, _ := ctx.Value(correlationContextKey).(string)
	return value
}
