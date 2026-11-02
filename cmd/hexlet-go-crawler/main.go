package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"

	"code/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cmd := app.New(&http.Client{})
	if err := cmd.Run(ctx, os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
