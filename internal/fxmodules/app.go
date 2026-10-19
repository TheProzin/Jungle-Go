package fxmodules

import (
	"net/http"

	"go.uber.org/fx"

	walletapp "github.com/TheProzin/Jungle-Go/internal/app/wallet"
	"github.com/TheProzin/Jungle-Go/internal/config"
	"github.com/TheProzin/Jungle-Go/internal/httpapi"
	"github.com/TheProzin/Jungle-Go/internal/infra/postgres"
	"github.com/TheProzin/Jungle-Go/internal/infra/sqs"
	"github.com/TheProzin/Jungle-Go/internal/observability"
)

var App = fx.Module("app",
	fx.Provide(
		config.Load,
		observability.NewLogger,
		postgres.NewPool,
		postgres.NewUnitOfWork,
		sqs.NewClient,
		walletapp.NewService,
		httpapi.NewHealth,
		httpapi.NewWalletHandler,
		httpapi.NewMux,
		httpapi.NewServer,
	),
	fx.Invoke(func(*http.Server) {}),
)
