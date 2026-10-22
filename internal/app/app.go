package app

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/urfave/cli/v3"

	"code/crawler"
)

const (
	appName        = "hexlet-go-crawler"
	appUsage       = "analyze a website structure"
	argsUsage      = "<url>"
	defaultDepth   = 1
	defaultTimeout = 15 * time.Second
)

// New builds the CLI command. The HTTP client is injected by the caller.
func New(client *http.Client) *cli.Command {
	return &cli.Command{
		Name:      appName,
		Usage:     appUsage,
		ArgsUsage: argsUsage,
		Arguments: []cli.Argument{
			&cli.StringArg{Name: "url", UsageText: argsUsage, Required: true},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			opts := crawler.Options{
				URL:        cmd.StringArg("url"),
				Depth:      defaultDepth,
				Timeout:    defaultTimeout,
				IndentJSON: true,
				HTTPClient: client,
			}

			report, err := crawler.Analyze(ctx, opts)
			if err != nil {
				return fmt.Errorf("analyze: %w", err)
			}

			_, err = fmt.Fprintln(cmd.Root().Writer, string(report))
			if err != nil {
				return fmt.Errorf("write report: %w", err)
			}
			return nil
		},
	}
}
