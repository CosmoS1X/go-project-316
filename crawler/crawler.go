package crawler

import (
	"context"
	"fmt"
	"io"
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
	discoveredAt := time.Now().UTC().Truncate(time.Second)
	statusCode, body, err := c.fetch(ctx, rawURL, true)

	result := page{
		URL:          rawURL,
		Depth:        depth,
		HTTPStatus:   statusCode,
		Status:       statusOK,
		BrokenLinks:  make([]brokenLink, 0),
		DiscoveredAt: discoveredAt,
	}
	switch {
	case err != nil:
		result.Status = statusError
		result.Error = err.Error()
	case statusCode >= http.StatusBadRequest:
		result.Status = statusError
		result.Error = "unexpected http status: " + strconv.Itoa(statusCode)
	default:
		result.BrokenLinks = c.findBrokenLinks(ctx, rawURL, body)
	}
	return result
}

func (c *crawler) fetch(
	ctx context.Context,
	rawURL string,
	readBody bool,
) (statusCode int, body []byte, err error) {
	reqCtx := ctx
	if c.opts.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.opts.Timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	if !readBody {
		return resp.StatusCode, nil, nil
	}

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read body: %w", err)
	}
	return resp.StatusCode, body, nil
}
