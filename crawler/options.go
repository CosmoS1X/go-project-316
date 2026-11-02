package crawler

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"
)

var (
	ErrInvalidURL     = errors.New("invalid url")
	ErrNilHTTPClient  = errors.New("http client is required")
	ErrInvalidOptions = errors.New("invalid options")
)

type Options struct {
	URL         string
	Depth       int
	Timeout     time.Duration
	IndentJSON  bool
	HTTPClient  *http.Client
	Retries     int
	Delay       time.Duration
	UserAgent   string
	Concurrency int
	RPS         float64
}

func (o Options) validate() (*url.URL, error) {
	if o.HTTPClient == nil {
		return nil, ErrNilHTTPClient
	}
	if o.Depth < 0 {
		return nil, fmt.Errorf("%w: depth must not be negative", ErrInvalidOptions)
	}
	if o.Timeout < 0 {
		return nil, fmt.Errorf("%w: timeout must not be negative", ErrInvalidOptions)
	}
	if o.Retries < 0 {
		return nil, fmt.Errorf("%w: retries must not be negative", ErrInvalidOptions)
	}
	if o.Delay < 0 {
		return nil, fmt.Errorf("%w: delay must not be negative", ErrInvalidOptions)
	}
	if o.Concurrency < 0 {
		return nil, fmt.Errorf("%w: concurrency must not be negative", ErrInvalidOptions)
	}
	if math.IsNaN(o.RPS) || math.IsInf(o.RPS, 0) {
		return nil, fmt.Errorf("%w: rps must be finite", ErrInvalidOptions)
	}
	if o.RPS < 0 {
		return nil, fmt.Errorf("%w: rps must not be negative", ErrInvalidOptions)
	}
	return parseURL(o.URL)
}

func parseURL(rawURL string) (*url.URL, error) {
	if rawURL == "" {
		return nil, fmt.Errorf("%w: empty url", ErrInvalidURL)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidURL, err)
	}
	if parsed.Scheme != schemeHTTP && parsed.Scheme != schemeHTTPS {
		return nil, fmt.Errorf("%w: unsupported scheme %q", ErrInvalidURL, parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("%w: missing host", ErrInvalidURL)
	}
	return parsed, nil
}
