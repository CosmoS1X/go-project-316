package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"code/internal/app"
)

func main() {
	cmd := app.New(&http.Client{})
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
