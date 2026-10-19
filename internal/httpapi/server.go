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
	"github.com/TheProzin/Jungle-Go/internal/observability"
)

func NewMux(health *Health, wallets *WalletHandler, wagering *WageringHandler, auth *Auth, sqsDev *SQSDevHandler) *http.ServeMux {
	mux := http.NewServeMux()
	metricsHandler := observability.Default.Handler()

	mux.HandleFunc("GET /health/live", observe("GET /health/live", health.Live))
	mux.HandleFunc("GET /health/ready", observe("GET /health/ready", health.Ready))
	mux.HandleFunc("POST /wallets", observe("POST /wallets", auth.Internal(wallets.Create)))
	mux.HandleFunc("GET /wallets/{walletId}", observe("GET /wallets/{walletId}", auth.Internal(wallets.Get)))
	mux.HandleFunc("GET /wallets/{walletId}/ledger", observe("GET /wallets/{walletId}/ledger", auth.Internal(wallets.Ledger)))
	mux.HandleFunc("POST /wallets/{walletId}/reconciliation", observe("POST /wallets/{walletId}/reconciliation", auth.Internal(wallets.Reconcile)))
	mux.HandleFunc("POST /wagering/transactions", observe("POST /wagering/transactions", auth.Provider(wagering.Process)))
	mux.HandleFunc("GET /wagering/transactions/{transactionId}", observe("GET /wagering/transactions/{transactionId}", auth.Authenticated(wagering.Get)))
	mux.HandleFunc("GET /providers/{providerId}/wagering/transactions/{externalTransactionId}", observe("GET /providers/{providerId}/wagering/transactions/{externalTransactionId}", auth.Authenticated(wagering.GetByProvider)))
	mux.HandleFunc("GET /metrics", observe("GET /metrics", auth.Internal(metricsHandler.ServeHTTP)))
	mux.HandleFunc("POST /dev/sqs/send", observe("POST /dev/sqs/send", auth.Internal(sqsDev.Send)))
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
