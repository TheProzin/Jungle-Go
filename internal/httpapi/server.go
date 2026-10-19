package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"go.uber.org/fx"

	"github.com/TheProzin/Jungle-Go/internal/config"
)

func NewMux(health *Health, wallets *WalletHandler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", health.Live)
	mux.HandleFunc("GET /health/ready", health.Ready)
	mux.HandleFunc("POST /wallets", wallets.Create)
	mux.HandleFunc("GET /wallets/{walletId}", wallets.Get)
	mux.HandleFunc("GET /wallets/{walletId}/ledger", wallets.Ledger)
	mux.HandleFunc("POST /wallets/{walletId}/reconciliation", wallets.Reconcile)
	return mux
}

func NewServer(
	lc fx.Lifecycle,
	cfg *config.Config,
	logger *slog.Logger,
	mux *http.ServeMux,
) *http.Server {
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			listener, err := net.Listen("tcp", server.Addr)
			if err != nil {
				return fmt.Errorf("listen on %s: %w", server.Addr, err)
			}

			go func() {
				logger.Info("http server listening", "addr", server.Addr)
				if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.Error("http server stopped unexpectedly", "error", err)
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("shutting down http server")
			shutdownCtx, cancel := context.WithTimeout(ctx, cfg.ShutdownTimeout)
			defer cancel()
			return server.Shutdown(shutdownCtx)
		},
	})

	return server
}
