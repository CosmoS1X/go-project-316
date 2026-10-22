package crawler

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

type crawler struct {
	opts Options
}

func newCrawler(opts Options) (*crawler, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	return &crawler{opts: opts}, nil
}

func (c *crawler) Run(ctx context.Context) *report {
	generatedAt := time.Now().UTC().Truncate(time.Second)

	return &report{
		RootURL:     c.opts.URL,
		Depth:       c.opts.Depth,
		GeneratedAt: generatedAt,
		Pages:       []page{c.visit(ctx, c.opts.URL, 0)},
	}
}

func (c *crawler) visit(ctx context.Context, rawURL string, depth int) page {
	statusCode, err := c.fetch(ctx, rawURL)

	result := page{
		URL:        rawURL,
		Depth:      depth,
		HTTPStatus: statusCode,
		Status:     statusOK,
	}
	switch {
	case err != nil:
		result.Status = statusError
		result.Error = err.Error()
	case statusCode >= http.StatusBadRequest:
		result.Status = statusError
		result.Error = "unexpected http status: " + strconv.Itoa(statusCode)
	}
	return result
}

func (c *crawler) fetch(ctx context.Context, rawURL string) (int, error) {
	reqCtx := ctx
	if c.opts.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.opts.Timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return 0, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("fetch %s: %w", rawURL, err)
	}
	defer resp.Body.Close()

	return resp.StatusCode, nil
}
