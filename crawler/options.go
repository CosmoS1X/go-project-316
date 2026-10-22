package crawler

import (
	"errors"
	"fmt"
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
	URL        string
	Depth      int
	Timeout    time.Duration
	IndentJSON bool
	HTTPClient *http.Client
}

func (o Options) validate() error {
	if o.HTTPClient == nil {
		return ErrNilHTTPClient
	}
	if o.Depth < 0 {
		return fmt.Errorf("%w: depth must not be negative", ErrInvalidOptions)
	}
	if o.Timeout < 0 {
		return fmt.Errorf("%w: timeout must not be negative", ErrInvalidOptions)
	}
	return validateURL(o.URL)
}

func validateURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("%w: empty url", ErrInvalidURL)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidURL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%w: unsupported scheme %q", ErrInvalidURL, parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%w: missing host", ErrInvalidURL)
	}
	return nil
}
