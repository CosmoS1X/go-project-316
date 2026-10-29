package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const retryDelay = 100 * time.Millisecond

func (c *crawler) fetch(
	ctx context.Context,
	rawURL string,
	readBody bool,
) (statusCode int, body []byte, err error) {
	for attemptIndex := 0; ; attemptIndex++ {
		var retryable bool
		statusCode, body, retryable, err = c.attempt(ctx, rawURL, readBody)

		if !retryable || ctx.Err() != nil || attemptIndex >= c.opts.Retries {
			return statusCode, body, err
		}

		if waitErr := waitForRetry(ctx); waitErr != nil {
			return statusCode, nil, waitErr
		}
	}
}

func (c *crawler) attempt(
	ctx context.Context,
	rawURL string,
	readBody bool,
) (statusCode int, body []byte, retryable bool, err error) {
	if c.limiter != nil {
		if waitErr := c.limiter.Wait(ctx); waitErr != nil {
			return 0, nil, false, waitErr
		}
	}

	reqCtx := ctx
	if c.opts.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.opts.Timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return 0, nil, false, fmt.Errorf("build request: %w", err)
	}
	if c.opts.UserAgent != "" {
		req.Header.Set("User-Agent", c.opts.UserAgent)
	}

	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, true, err
	}
	defer resp.Body.Close()

	if !readBody {
		return resp.StatusCode, nil, isTransientStatus(resp.StatusCode), nil
	}

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, true, fmt.Errorf("read body: %w", err)
	}
	return resp.StatusCode, body, isTransientStatus(resp.StatusCode), nil
}

func isTransientStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError
}

func waitForRetry(ctx context.Context) error {
	timer := time.NewTimer(retryDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
