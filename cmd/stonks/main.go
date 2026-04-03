package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"stonks/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
