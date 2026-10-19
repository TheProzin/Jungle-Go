package fxmodules

import (
	"net/http"

	"go.uber.org/fx"

	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
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
		wageringapp.NewServiceWithConfig,
		wageringapp.NewReferenceWorker,
		httpapi.NewHealth,
		httpapi.NewWalletHandler,
		httpapi.NewWageringHandler,
		httpapi.NewMux,
		httpapi.NewServer,
	),
	fx.Invoke(func(*http.Server) {}),
	fx.Invoke(StartReferenceWorker),
)
