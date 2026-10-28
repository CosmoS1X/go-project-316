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
	appName          = "hexlet-go-crawler"
	appUsage         = "analyze a website structure"
	argsUsage        = "<url>"
	defaultDepth     = 10
	defaultRetries   = 1
	defaultDelay     = 0 * time.Second
	defaultTimeout   = 15 * time.Second
	defaultRPS       = 0.0
	defaultUserAgent = ""
	defaultWorkers   = 4
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
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:  "depth",
				Value: defaultDepth,
			},
			&cli.IntFlag{
				Name:  "retries",
				Value: defaultRetries,
			},
			&cli.DurationFlag{
				Name:  "delay",
				Value: defaultDelay,
			},
			&cli.DurationFlag{
				Name:  "timeout",
				Value: defaultTimeout,
			},
			&cli.FloatFlag{
				Name:  "rps",
				Value: defaultRPS,
			},
			&cli.StringFlag{
				Name:  "user-agent",
				Value: defaultUserAgent,
			},
			&cli.IntFlag{
				Name:  "workers",
				Value: defaultWorkers,
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			opts := crawler.Options{
				URL:         cmd.StringArg("url"),
				Depth:       cmd.Int("depth"),
				Retries:     cmd.Int("retries"),
				Delay:       cmd.Duration("delay"),
				Timeout:     cmd.Duration("timeout"),
				RPS:         cmd.Float("rps"),
				UserAgent:   cmd.String("user-agent"),
				Concurrency: cmd.Int("workers"),
				IndentJSON:  true,
				HTTPClient:  client,
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
