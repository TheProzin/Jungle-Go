package fxmodules

import (
	"context"

	"go.uber.org/fx"

	wageringapp "github.com/TheProzin/Jungle-Go/internal/app/wagering"
	sqsinfra "github.com/TheProzin/Jungle-Go/internal/infra/sqs"
)

func StartSQSConsumer(lc fx.Lifecycle, consumer *sqsinfra.Consumer) {
	var (
		cancel context.CancelFunc
		done   chan struct{}
	)

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, stop := context.WithCancel(context.Background())
			cancel = stop
			done = make(chan struct{})

			go func() {
				defer close(done)
				consumer.Run(ctx)
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}
			if done != nil {
				select {
				case <-done:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		},
	})
}

func StartReferenceWorker(lc fx.Lifecycle, worker *wageringapp.ReferenceWorker) {
	var (
		cancel context.CancelFunc
		done   chan struct{}
	)

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ctx, stop := context.WithCancel(context.Background())
			cancel = stop
			done = make(chan struct{})

			go func() {
				defer close(done)
				worker.Run(ctx)
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			if cancel != nil {
				cancel()
			}
			if done != nil {
				select {
				case <-done:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			return nil
		},
	})
}
