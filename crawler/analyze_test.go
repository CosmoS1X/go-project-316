package crawler_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

const exampleURL = "https://example.com"

type testReport struct {
	RootURL     string     `json:"root_url"`
	Depth       int        `json:"depth"`
	GeneratedAt time.Time  `json:"generated_at"`
	Pages       []testPage `json:"pages"`
}

type testPage struct {
	URL        string `json:"url"`
	Depth      int    `json:"depth"`
	HTTPStatus int    `json:"http_status"`
	Status     string `json:"status"`
	Error      string `json:"error"`
}

func decodeReport(t *testing.T, data []byte) testReport {
	t.Helper()

	var rep testReport
	require.NoError(t, json.Unmarshal(data, &rep))
	return rep
}

func TestAnalyzeSuccess(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

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
}

func TestAnalyzeHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)

	got := rep.Pages[0]
	assert.Equal(t, http.StatusInternalServerError, got.HTTPStatus)
	assert.Equal(t, "error", got.Status)
	assert.NotEmpty(t, got.Error)
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection failed")
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := crawler.Analyze(t.Context(), tt.opts)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
