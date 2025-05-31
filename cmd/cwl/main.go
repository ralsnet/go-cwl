package main

import (
	"context"
	"fmt"

	"github.com/ralsnet/go-cwl"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := cwl.NewApp()
	defer func() {
		if err := recover(); err != nil {
			fmt.Println(err)
		}
	}()
	if err := app.Start(ctx); err != nil {
		panic(err)
	}
}
