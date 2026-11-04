package crawler_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/charmap"

	"github.com/CosmoS1X/web-crawler/crawler"
)

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
			name:  "too many requests",
			first: sequenceResult{status: http.StatusTooManyRequests},
		},
		{
			name:  "internal server error",
			first: sequenceResult{status: http.StatusInternalServerError},
		},
		{
			name:  "service unavailable",
			first: sequenceResult{status: http.StatusServiceUnavailable},
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

	tests := []struct {
		name      string
		transport http.RoundTripper
	}{
		{name: "immediate error", transport: failingBodyTransport{}},
		{name: "error after preview", transport: lateFailingBodyTransport{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rep := analyzeReport(t, crawler.Options{
				URL:        exampleURL + "/",
				Depth:      1,
				Timeout:    time.Second,
				HTTPClient: &http.Client{Transport: tt.transport},
			})
			require.Len(t, rep.Pages, 1)

			got := rep.Pages[0]
			assert.Equal(t, http.StatusOK, got.HTTPStatus)
			assert.Equal(t, "error", got.Status)
			assert.Contains(t, got.Error, "read body")
			assert.Nil(t, got.BrokenLinks)
			assert.Nil(t, got.Assets)
		})
	}
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

type lateFailingBody struct {
	remaining int
}

func (b *lateFailingBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, errors.New("read failed")
	}
	b.remaining--
	for i := range p {
		p[i] = 'a'
	}
	return len(p), nil
}

func (b *lateFailingBody) Close() error {
	return nil
}

type lateFailingBodyTransport struct{}

func (lateFailingBodyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     http.StatusText(http.StatusOK),
		Header:     make(http.Header),
		Body:       &lateFailingBody{remaining: 1},
		Request:    req,
	}, nil
}

func encodeWindows1251(t *testing.T, text string) []byte {
	t.Helper()

	encoded, err := charmap.Windows1251.NewEncoder().Bytes([]byte(text))
	require.NoError(t, err)
	return encoded
}

func writeRawBody(t *testing.T, w http.ResponseWriter, body []byte) {
	t.Helper()

	if _, err := w.Write(body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func TestAnalyzeDecodesContentTypeCharset(t *testing.T) {
	t.Parallel()

	page := `<html><head><title>Привет</title>` +
		`<meta name="description" content="Описание"></head>` +
		`<body><h1>Заголовок</h1></body></html>`

	srv := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=windows-1251")
		writeRawBody(t, w, encodeWindows1251(t, page))
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

	seo := rep.Pages[0].SEO
	assert.True(t, seo.HasTitle)
	assert.Equal(t, "Привет", seo.Title)
	assert.True(t, seo.HasDescription)
	assert.Equal(t, "Описание", seo.Description)
	assert.True(t, seo.HasH1)
}

func TestAnalyzeDecodesMetaCharset(t *testing.T) {
	t.Parallel()

	page := `<html><head><meta charset="windows-1251"><title>Привет</title></head></html>`

	srv := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		writeRawBody(t, w, encodeWindows1251(t, page))
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
	assert.Equal(t, "Привет", rep.Pages[0].SEO.Title)
}

func TestAnalyzeLimitsPageBody(t *testing.T) {
	t.Parallel()

	// Keep in sync with maxPageBytes in crawler/fetch.go.
	const maxPageBytes = 10 << 20

	body := `<title>Start</title>` +
		strings.Repeat("x", maxPageBytes) +
		`<a href="/next">next</a>`

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			// The client stops reading after maxPageBytes, so a write error is expected.
			if _, err := io.WriteString(w, body); err != nil {
				return
			}
		case "/next":
			writeBody(t, w, "")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      2,
		Timeout:    5 * time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1, "links beyond the body limit must not be discovered")
	assert.Equal(t, "Start", rep.Pages[0].SEO.Title)
	assert.Equal(t, "ok", rep.Pages[0].Status)
}

type methodCheck struct {
	method string
	url    string
}

type methodRecordingTransport struct {
	mu      sync.Mutex
	checks  []methodCheck
	pageURL string
}

func (tr *methodRecordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.mu.Lock()
	tr.checks = append(tr.checks, methodCheck{method: req.Method, url: req.URL.String()})
	tr.mu.Unlock()

	if req.URL.String() == tr.pageURL {
		return testResponse(req, `<a href="https://links.example/page">link</a><img src="https://assets.example/logo.png">`), nil
	}
	return testResponse(req, ""), nil
}

func (tr *methodRecordingTransport) recorded() []methodCheck {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	return append([]methodCheck(nil), tr.checks...)
}

func TestAnalyzeUsesHeadForLinkAndAssetChecks(t *testing.T) {
	t.Parallel()

	tr := &methodRecordingTransport{pageURL: exampleURL + "/"}
	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)
	require.NotEmpty(t, data)

	checks := tr.recorded()
	assert.ElementsMatch(t, []methodCheck{
		{method: http.MethodGet, url: exampleURL + "/"},
		{method: http.MethodHead, url: "https://assets.example/logo.png"},
		{method: http.MethodHead, url: "https://links.example/page"},
	}, checks)
}

func TestAnalyzeFallsBackToGetOnMethodNotAllowed(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		methods = make(map[string][]string)
	)
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods[r.URL.Path] = append(methods[r.URL.Path], r.Method)
		mu.Unlock()

		switch r.URL.Path {
		case "/":
			writeBody(t, w, `<a href="/link">link</a><img src="`+logoPath+`">`)
		case "/link", logoPath:
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			writeBody(t, w, "logo!")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
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
	assert.Empty(t, rep.Pages[0].BrokenLinks)
	require.Len(t, rep.Pages[0].Assets, 1)
	assert.Equal(t, int64(len("logo!")), rep.Pages[0].Assets[0].SizeBytes)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{http.MethodHead, http.MethodGet}, methods["/link"])
	assert.Equal(t, []string{http.MethodHead, http.MethodGet}, methods[logoPath])
}

type getFallbackErrorTransport struct{}

func (getFallbackErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.URL.Path {
	case "", "/":
		return testResponse(req, `<a href="/link">link</a><img src="/asset.bin">`), nil
	case "/link", "/asset.bin":
		if req.Method == http.MethodHead {
			return responseWithStatus(req, http.StatusMethodNotAllowed, ""), nil
		}
		return nil, errors.New("connection failed")
	default:
		return responseWithStatus(req, http.StatusNotFound, ""), nil
	}
}

func TestAnalyzeGETFallbackError(t *testing.T) {
	t.Parallel()

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      0,
		Timeout:    time.Second,
		HTTPClient: &http.Client{Transport: getFallbackErrorTransport{}},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	require.Len(t, rep.Pages[0].BrokenLinks, 1)
	assert.Equal(t, exampleURL+"/link", rep.Pages[0].BrokenLinks[0].URL)
	assert.Zero(t, rep.Pages[0].BrokenLinks[0].StatusCode)
	assert.Contains(t, rep.Pages[0].BrokenLinks[0].Error, "connection failed")

	require.Len(t, rep.Pages[0].Assets, 1)
	assert.Equal(t, exampleURL+"/asset.bin", rep.Pages[0].Assets[0].URL)
	assert.Zero(t, rep.Pages[0].Assets[0].StatusCode)
	assert.Contains(t, rep.Pages[0].Assets[0].Error, "connection failed")
}

type assetReadErrorTransport struct{}

func (assetReadErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == "" || req.URL.Path == "/" {
		return testResponse(req, `<img src="/asset.bin">`), nil
	}
	if req.Method == http.MethodHead {
		return responseWithStatus(req, http.StatusMethodNotAllowed, ""), nil
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Status:        http.StatusText(http.StatusOK),
		Header:        make(http.Header),
		Body:          failingBody{},
		ContentLength: -1,
		Request:       req,
	}, nil
}

func TestAnalyzeAssetGETReadError(t *testing.T) {
	t.Parallel()

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL,
		Depth:      0,
		Timeout:    time.Second,
		HTTPClient: &http.Client{Transport: assetReadErrorTransport{}},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	require.Len(t, rep.Pages[0].Assets, 1)
	assert.Contains(t, rep.Pages[0].Assets[0].Error, "read body")
}

type unknownStatusTransport struct{}

func (unknownStatusTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return responseWithStatus(req, 599, testHTMLBody), nil
}

func TestAnalyzeUnknownHTTPStatus(t *testing.T) {
	t.Parallel()

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL,
		Depth:      0,
		Timeout:    time.Second,
		HTTPClient: &http.Client{Transport: unknownStatusTransport{}},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, 599, rep.Pages[0].HTTPStatus)
	assert.Equal(t, "error", rep.Pages[0].Status)
	assert.Equal(t, "unexpected http status: 599", rep.Pages[0].Error)
}

const rateLimitPageHTML = `<a href="https://one.example/a">a</a><a href="https://two.example/b">b</a>`

type timedTransport struct {
	mu        sync.Mutex
	times     []time.Time
	pageURL   string
	pageHTML  string
	onFirst   func()
	firstOnce sync.Once
}

func (tr *timedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.mu.Lock()
	tr.times = append(tr.times, time.Now())
	tr.mu.Unlock()

	if tr.onFirst != nil {
		tr.firstOnce.Do(tr.onFirst)
	}
	if req.URL.String() == tr.pageURL {
		return testResponse(req, tr.pageHTML), nil
	}
	return testResponse(req, ""), nil
}

func (tr *timedTransport) callTimes() []time.Time {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	return append([]time.Time(nil), tr.times...)
}

func TestAnalyzeDelayEnforcesInterval(t *testing.T) {
	t.Parallel()

	tr := &timedTransport{pageURL: exampleURL + "/", pageHTML: rateLimitPageHTML}
	_, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		Delay:      25 * time.Millisecond,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)

	times := tr.callTimes()
	require.Len(t, times, 3, "root page plus two external link checks")
	for i := 1; i < len(times); i++ {
		assert.GreaterOrEqual(t, times[i].Sub(times[i-1]), 15*time.Millisecond,
			"delay must space consecutive requests")
	}
}

func TestAnalyzeRPSOverridesDelay(t *testing.T) {
	t.Parallel()

	tr := &timedTransport{pageURL: exampleURL + "/", pageHTML: rateLimitPageHTML}
	startedAt := time.Now()
	_, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		RPS:        1000,
		Delay:      time.Second,
		HTTPClient: &http.Client{Transport: tr},
	})
	elapsed := time.Since(startedAt)
	require.NoError(t, err)

	require.Len(t, tr.callTimes(), 3)
	assert.Less(t, elapsed, 500*time.Millisecond, "Delay must be ignored when RPS is set")
}

func TestAnalyzeRateLimitContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := &timedTransport{
		pageURL:  exampleURL + "/",
		pageHTML: `<a href="https://offline.example/page">offline</a>`,
		onFirst: func() {
			time.AfterFunc(50*time.Millisecond, cancel)
		},
	}

	startedAt := time.Now()
	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		Delay:      time.Second,
		HTTPClient: &http.Client{Transport: tr},
	})
	elapsed := time.Since(startedAt)
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	assert.Less(t, elapsed, 800*time.Millisecond, "cancellation must interrupt the limiter wait")

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Empty(t, rep.Pages[0].BrokenLinks)
	assert.NotContains(t, string(data), context.Canceled.Error())
}
