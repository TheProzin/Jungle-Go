package fxmodules

import (
	"net/http"

	"go.uber.org/fx"

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
		sqs.NewClient,
		httpapi.NewHealth,
		httpapi.NewMux,
		httpapi.NewServer,
	),
	fx.Invoke(func(*http.Server) {}),
)
