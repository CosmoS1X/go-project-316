package crawler_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

const testHTMLBody = "<html></html>"

type sequenceResult struct {
	status int
	err    error
	body   string
}

type sequenceTransport struct {
	mu      sync.Mutex
	results []sequenceResult
	calls   []time.Time
}

func (tr *sequenceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.mu.Lock()
	index := len(tr.calls)
	tr.calls = append(tr.calls, time.Now())
	tr.mu.Unlock()

	result := tr.results[len(tr.results)-1]
	if index < len(tr.results) {
		result = tr.results[index]
	}

	if result.err != nil {
		return nil, result.err
	}

	return &http.Response{
		StatusCode:    result.status,
		Status:        http.StatusText(result.status),
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(result.body)),
		ContentLength: int64(len(result.body)),
		Request:       req,
	}, nil
}

func (tr *sequenceTransport) callCount() int {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	return len(tr.calls)
}

func (tr *sequenceTransport) callTimes() []time.Time {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	return append([]time.Time(nil), tr.calls...)
}

func TestAnalyzeRetriesUntilSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		first sequenceResult
	}{
		{
			name:  "http error",
			first: sequenceResult{status: http.StatusInternalServerError},
		},
		{
			name:  "network error",
			first: sequenceResult{err: errors.New("connection failed")},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr := &sequenceTransport{results: []sequenceResult{
				tt.first,
				{status: http.StatusOK, body: testHTMLBody},
			}}

			data, err := crawler.Analyze(t.Context(), crawler.Options{
				URL:        exampleURL,
				Depth:      0,
				Timeout:    time.Second,
				Retries:    1,
				HTTPClient: &http.Client{Transport: tr},
			})
			require.NoError(t, err)

			rep := decodeReport(t, data)
			require.Len(t, rep.Pages, 1)
			assert.Equal(t, "ok", rep.Pages[0].Status)
			assert.Equal(t, http.StatusOK, rep.Pages[0].HTTPStatus)
			assert.Empty(t, rep.Pages[0].Error)
			assert.Equal(t, 2, tr.callCount())
		})
	}
}

func TestAnalyzeRetriesExhausted(t *testing.T) {
	t.Parallel()

	tr := &sequenceTransport{results: []sequenceResult{
		{status: http.StatusInternalServerError},
		{status: http.StatusBadGateway},
		{status: http.StatusGatewayTimeout},
	}}

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL,
		Depth:      0,
		Timeout:    time.Second,
		Retries:    2,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)

	got := rep.Pages[0]
	assert.Equal(t, http.StatusGatewayTimeout, got.HTTPStatus, "report must contain the last attempt result")
	assert.Equal(t, "error", got.Status)
	assert.NotEmpty(t, got.Error)
	assert.Equal(t, 3, tr.callCount(), "must make exactly retries + 1 attempts")
}

func TestAnalyzeRetriesNetworkErrorExhausted(t *testing.T) {
	t.Parallel()

	tr := &sequenceTransport{results: []sequenceResult{
		{err: errors.New("connection failed")},
	}}

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL,
		Depth:      0,
		Timeout:    time.Second,
		Retries:    2,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)

	got := rep.Pages[0]
	assert.Zero(t, got.HTTPStatus)
	assert.Equal(t, "error", got.Status)
	assert.Contains(t, got.Error, "connection failed")
	assert.Equal(t, 3, tr.callCount(), "must make exactly retries + 1 attempts")
}

func TestAnalyzeRetriesTransientStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
	}{
		{name: "too many requests", status: http.StatusTooManyRequests},
		{name: "internal server error", status: http.StatusInternalServerError},
		{name: "service unavailable", status: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr := &sequenceTransport{results: []sequenceResult{
				{status: tt.status},
				{status: http.StatusOK, body: testHTMLBody},
			}}

			data, err := crawler.Analyze(t.Context(), crawler.Options{
				URL:        exampleURL,
				Depth:      0,
				Timeout:    time.Second,
				Retries:    1,
				HTTPClient: &http.Client{Transport: tr},
			})
			require.NoError(t, err)

			rep := decodeReport(t, data)
			require.Len(t, rep.Pages, 1)
			assert.Equal(t, "ok", rep.Pages[0].Status)
			assert.Equal(t, 2, tr.callCount())
		})
	}
}

func TestAnalyzeDoesNotRetryClientError(t *testing.T) {
	t.Parallel()

	tr := &sequenceTransport{results: []sequenceResult{
		{status: http.StatusNotFound},
	}}

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL,
		Depth:      0,
		Timeout:    time.Second,
		Retries:    3,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, http.StatusNotFound, rep.Pages[0].HTTPStatus)
	assert.Equal(t, "error", rep.Pages[0].Status)
	assert.Equal(t, 1, tr.callCount())
}

func TestAnalyzeZeroRetriesSingleAttempt(t *testing.T) {
	t.Parallel()

	tr := &sequenceTransport{results: []sequenceResult{
		{status: http.StatusInternalServerError},
		{status: http.StatusOK, body: testHTMLBody},
	}}

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL,
		Depth:      0,
		Timeout:    time.Second,
		Retries:    0,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, http.StatusInternalServerError, rep.Pages[0].HTTPStatus)
	assert.Equal(t, "error", rep.Pages[0].Status)
	assert.Equal(t, 1, tr.callCount())
}

func TestAnalyzeRetryWaitsBetweenAttempts(t *testing.T) {
	t.Parallel()

	tr := &sequenceTransport{results: []sequenceResult{
		{status: http.StatusInternalServerError},
		{status: http.StatusOK, body: testHTMLBody},
	}}

	_, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL,
		Depth:      0,
		Timeout:    time.Second,
		Retries:    1,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)

	times := tr.callTimes()
	require.Len(t, times, 2)
	assert.GreaterOrEqual(t, times[1].Sub(times[0]), 50*time.Millisecond,
		"retry must wait before the next attempt")
}

func TestAnalyzeBrokenLinkReportsLastAttempt(t *testing.T) {
	t.Parallel()

	var assetCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<a href="/asset">asset</a>`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/asset":
			if assetCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Timeout:    time.Second,
		Retries:    1,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 2)
	assert.Equal(t, []testBrokenLink{
		{URL: srv.URL + "/asset", StatusCode: http.StatusNotFound, Error: http.StatusText(http.StatusNotFound)},
	}, rep.Pages[0].BrokenLinks)
	assert.Equal(t, int32(3), assetCalls.Load(), "broken link check retries once, then the page is visited")
}

type cancelingTransport struct {
	cancel context.CancelFunc
	calls  atomic.Int32
}

func (tr *cancelingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.calls.Add(1)
	tr.cancel()

	return &http.Response{
		StatusCode:    http.StatusInternalServerError,
		Status:        http.StatusText(http.StatusInternalServerError),
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader("")),
		ContentLength: 0,
		Request:       req,
	}, nil
}

type delayedCancelingTransport struct {
	cancel context.CancelFunc
	delay  time.Duration
	calls  atomic.Int32
}

func (tr *delayedCancelingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.calls.Add(1)
	time.AfterFunc(tr.delay, tr.cancel)

	return &http.Response{
		StatusCode:    http.StatusInternalServerError,
		Status:        http.StatusText(http.StatusInternalServerError),
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader("")),
		ContentLength: 0,
		Request:       req,
	}, nil
}

func TestAnalyzeRetryWaitStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	tr := &delayedCancelingTransport{cancel: cancel, delay: 10 * time.Millisecond}

	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        exampleURL,
		Depth:      0,
		Timeout:    time.Second,
		Retries:    3,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, "error", rep.Pages[0].Status)
	assert.Equal(t, int32(1), tr.calls.Load(), "cancellation during the pause must stop retries")
}

func TestAnalyzeRetryStopsOnContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	tr := &cancelingTransport{cancel: cancel}

	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        exampleURL,
		Depth:      0,
		Timeout:    time.Second,
		Retries:    2,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, "error", rep.Pages[0].Status)
	assert.Equal(t, int32(1), tr.calls.Load(), "cancellation must stop further retries")
}
