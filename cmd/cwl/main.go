package main

import (
	"context"

	"github.com/ralsnet/go-cwl"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := cwl.NewApp()
	if err := app.Start(ctx); err != nil {
		panic(err)
	}
}
