package crawler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/net/html/charset"
)

const (
	retryDelay   = 100 * time.Millisecond
	maxPageBytes = 10 << 20
)

type fetchMode int

const (
	fetchStatusOnly fetchMode = iota
	fetchFullBody
	fetchAssetMeta
)

type fetchResult struct {
	StatusCode int
	Body       []byte
	Size       int64
	Err        error
}

func (c *crawler) fetch(ctx context.Context, method, rawURL string, mode fetchMode) fetchResult {
	var result fetchResult
	for attemptIndex := 0; ; attemptIndex++ {
		var retryable bool
		result, retryable = c.doRequest(ctx, method, rawURL, mode)

		if !retryable || ctx.Err() != nil || attemptIndex >= c.opts.Retries {
			return result
		}

		if waitErr := waitForRetry(ctx); waitErr != nil {
			result.Body = nil
			result.Err = waitErr
			return result
		}
	}
}

// fetchWithGETFallback probes a URL with HEAD and repeats the request with GET
// when needsFallback reports that the HEAD response is not usable.
func (c *crawler) fetchWithGETFallback(
	ctx context.Context,
	rawURL string,
	mode fetchMode,
	needsFallback func(fetchResult) bool,
) fetchResult {
	result := c.fetch(ctx, http.MethodHead, rawURL, mode)
	if result.Err != nil || !needsFallback(result) {
		return result
	}
	return c.fetch(ctx, http.MethodGet, rawURL, mode)
}

func (c *crawler) doRequest(
	ctx context.Context,
	method, rawURL string,
	mode fetchMode,
) (fetchResult, bool) {
	if c.limiter != nil {
		if waitErr := c.limiter.Wait(ctx); waitErr != nil {
			return fetchResult{Err: waitErr}, false
		}
	}

	reqCtx := ctx
	if c.opts.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.opts.Timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, method, rawURL, http.NoBody)
	if err != nil {
		return fetchResult{Err: fmt.Errorf("build request: %w", err)}, false
	}
	if c.opts.UserAgent != "" {
		req.Header.Set("User-Agent", c.opts.UserAgent)
	}

	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return fetchResult{Err: err}, true
	}
	defer resp.Body.Close()

	result := fetchResult{StatusCode: resp.StatusCode}
	switch mode {
	case fetchStatusOnly:
	case fetchFullBody:
		reader, convErr := charset.NewReader(
			io.LimitReader(resp.Body, maxPageBytes),
			resp.Header.Get("Content-Type"),
		)
		if errors.Is(convErr, io.EOF) {
			reader = http.NoBody
		} else if convErr != nil {
			return fetchResult{
				StatusCode: resp.StatusCode,
				Err:        fmt.Errorf("read body: %w", convErr),
			}, true
		}
		body, readErr := io.ReadAll(reader)
		if readErr != nil {
			return fetchResult{StatusCode: resp.StatusCode, Err: fmt.Errorf("read body: %w", readErr)}, true
		}
		result.Body = body
		result.Size = int64(len(body))
	case fetchAssetMeta:
		result.Size = resp.ContentLength
		if method != http.MethodHead && resp.ContentLength < 0 {
			size, readErr := io.Copy(io.Discard, resp.Body)
			if readErr != nil {
				return fetchResult{StatusCode: resp.StatusCode, Err: fmt.Errorf("read body: %w", readErr)}, true
			}
			result.Size = size
		}
	}

	return result, isTransientStatus(resp.StatusCode)
}

func statusErrorText(statusCode int) string {
	if text := http.StatusText(statusCode); text != "" {
		return text
	}
	return fmt.Sprintf("unexpected http status: %d", statusCode)
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
