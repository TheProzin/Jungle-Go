package main

import (
	"go.uber.org/fx"

	"github.com/TheProzin/Jungle-Go/internal/fxmodules"
)

func main() {
	fx.New(fxmodules.App).Run()
}
