package crawler_test

import (
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

func TestAnalyzeSuccess(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Timeout:    time.Second,
		IndentJSON: true,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	assert.Equal(t, srv.URL, rep.RootURL)
	assert.Equal(t, 1, rep.Depth)
	assert.False(t, rep.GeneratedAt.IsZero())
	require.Len(t, rep.Pages, 1)

	got := rep.Pages[0]
	assert.Equal(t, srv.URL, got.URL)
	assert.Equal(t, 0, got.Depth)
	assert.Equal(t, http.StatusOK, got.HTTPStatus)
	assert.Equal(t, "ok", got.Status)
	assert.Empty(t, got.Error)
	assert.NotNil(t, got.BrokenLinks)
	assert.Empty(t, got.BrokenLinks)
	assert.NotNil(t, got.Assets)
	assert.Empty(t, got.Assets)
	assert.False(t, got.DiscoveredAt.IsZero())
}

func TestAnalyzeInvalidOptions(t *testing.T) {
	client := &http.Client{}

	tests := []struct {
		name    string
		opts    crawler.Options
		wantErr error
	}{
		{
			name:    "empty url",
			opts:    crawler.Options{URL: "", HTTPClient: client},
			wantErr: crawler.ErrInvalidURL,
		},
		{
			name:    "unsupported scheme",
			opts:    crawler.Options{URL: "ftp://example.com", HTTPClient: client},
			wantErr: crawler.ErrInvalidURL,
		},
		{
			name:    "malformed url",
			opts:    crawler.Options{URL: "http://[::1", HTTPClient: client},
			wantErr: crawler.ErrInvalidURL,
		},
		{
			name:    "missing host",
			opts:    crawler.Options{URL: "https://", HTTPClient: client},
			wantErr: crawler.ErrInvalidURL,
		},
		{
			name:    "nil client",
			opts:    crawler.Options{URL: exampleURL},
			wantErr: crawler.ErrNilHTTPClient,
		},
		{
			name:    "negative depth",
			opts:    crawler.Options{URL: exampleURL, Depth: -1, HTTPClient: client},
			wantErr: crawler.ErrInvalidOptions,
		},
		{
			name:    "negative timeout",
			opts:    crawler.Options{URL: exampleURL, Timeout: -time.Second, HTTPClient: client},
			wantErr: crawler.ErrInvalidOptions,
		},
		{
			name:    "negative retries",
			opts:    crawler.Options{URL: exampleURL, Retries: -1, HTTPClient: client},
			wantErr: crawler.ErrInvalidOptions,
		},
		{
			name:    "negative delay",
			opts:    crawler.Options{URL: exampleURL, Delay: -time.Second, HTTPClient: client},
			wantErr: crawler.ErrInvalidOptions,
		},
		{
			name:    "negative concurrency",
			opts:    crawler.Options{URL: exampleURL, Concurrency: -1, HTTPClient: client},
			wantErr: crawler.ErrInvalidOptions,
		},
		{
			name:    "negative rps",
			opts:    crawler.Options{URL: exampleURL, RPS: -1, HTTPClient: client},
			wantErr: crawler.ErrInvalidOptions,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := crawler.Analyze(t.Context(), tt.opts)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestAnalyzeWithUserAgent(t *testing.T) {
	t.Parallel()

	var gotUA atomic.Value
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotUA.Store(r.UserAgent())
		w.WriteHeader(http.StatusOK)
	})

	_, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      0,
		Timeout:    time.Second,
		UserAgent:  "test-agent",
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)
	assert.Equal(t, "test-agent", gotUA.Load())
}

func TestAnalyzeNetworkError(t *testing.T) {
	t.Parallel()

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL,
		Depth:      1,
		Timeout:    time.Second,
		IndentJSON: true,
		HTTPClient: &http.Client{Transport: failingTransport{}},
	})
	require.NoError(t, err, "network errors must not fail Analyze")

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)

	got := rep.Pages[0]
	assert.Zero(t, got.HTTPStatus)
	assert.Equal(t, "error", got.Status)
	assert.NotEmpty(t, got.Error)
}

func TestAnalyzeTimeout(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Timeout:    10 * time.Millisecond,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)

	got := rep.Pages[0]
	assert.Zero(t, got.HTTPStatus)
	assert.Equal(t, "error", got.Status)
	assert.NotEmpty(t, got.Error)
}

func TestAnalyzeReadBodyError(t *testing.T) {
	t.Parallel()

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		HTTPClient: &http.Client{Transport: failingBodyTransport{}},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)

	got := rep.Pages[0]
	assert.Equal(t, http.StatusOK, got.HTTPStatus)
	assert.Equal(t, "error", got.Status)
	assert.Contains(t, got.Error, "read body")
	assert.Nil(t, got.BrokenLinks)
	assert.Nil(t, got.Assets)
}

func TestAnalyzePageHTTPError(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      0,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)

	got := rep.Pages[0]
	assert.Equal(t, http.StatusInternalServerError, got.HTTPStatus)
	assert.Equal(t, "error", got.Status)
	assert.Equal(t, http.StatusText(http.StatusInternalServerError), got.Error)
	assert.NotNil(t, got.BrokenLinks)
	assert.Empty(t, got.BrokenLinks)
	assert.NotNil(t, got.Assets)
	assert.Empty(t, got.Assets)
}

type failingBody struct{}

func (failingBody) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}

func (failingBody) Close() error {
	return nil
}

type failingBodyTransport struct{}

func (failingBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     http.StatusText(http.StatusOK),
		Header:     make(http.Header),
		Body:       failingBody{},
		Request:    req,
	}, nil
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection failed")
}
