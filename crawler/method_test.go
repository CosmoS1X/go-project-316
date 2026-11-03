package crawler_test

import (
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

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
