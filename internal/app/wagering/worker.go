package wagering

import (
	"context"
	"log/slog"
	"time"

	"github.com/TheProzin/Jungle-Go/internal/config"
)

type ReferenceWorker struct {
	service  *Service
	interval time.Duration
	logger   *slog.Logger
}

func NewReferenceWorker(service *Service, cfg *config.Config, logger *slog.Logger) *ReferenceWorker {
	return &ReferenceWorker{
		service:  service,
		interval: cfg.ReferenceWorkerInterval,
		logger:   logger,
	}
}

func (w *ReferenceWorker) Run(ctx context.Context) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	for {
		for {
			processed, err := w.service.ProcessNextPendingReference(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				w.logger.Error("reference worker iteration failed", "error", err)
				break
			}
			if !processed {
				break
			}
		}
		w.service.RefreshReferenceMetrics(ctx)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
