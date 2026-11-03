package crawler_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

const brokenPath = "/broken"

func TestAnalyzeBrokenLinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		fixture    string
		statuses   map[string]int
		wantBroken []crawler.BrokenLink
		wantAssets []crawler.Asset
	}{
		{
			name:    "navigation links",
			fixture: "broken_links.html",
			statuses: map[string]int{
				"/working": http.StatusOK,
				"/broken":  http.StatusNotFound,
			},
			wantBroken: []crawler.BrokenLink{
				{URL: brokenPath, StatusCode: http.StatusNotFound, Error: http.StatusText(http.StatusNotFound)},
			},
			wantAssets: []crawler.Asset{},
		},
		{
			name:    "tags and assets",
			fixture: "broken_tags.html",
			statuses: map[string]int{
				"/working":       http.StatusOK,
				"/assets/app.js": http.StatusOK,
			},
			wantBroken: []crawler.BrokenLink{},
			wantAssets: []crawler.Asset{
				{
					URL:        "/assets/missing.png",
					Type:       assetTypeImage,
					StatusCode: http.StatusNotFound,
					Error:      http.StatusText(http.StatusNotFound),
				},
				{
					URL:        "/assets/app.js",
					Type:       assetTypeScript,
					StatusCode: http.StatusOK,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newPageServer(t, loadFixture(t, tt.fixture), tt.statuses)
			rep := analyzeReport(t, crawler.Options{
				URL:        srv.URL,
				Depth:      1,
				Timeout:    time.Second,
				HTTPClient: srv.Client(),
			})
			require.Len(t, rep.Pages, 1)

			page := rep.Pages[0]
			assert.Equal(t, srv.URL, page.URL)
			assert.Equal(t, 0, page.Depth)
			assert.Equal(t, absolutize(srv.URL, tt.wantBroken), page.BrokenLinks)
			assert.Equal(t, absolutizeAssets(srv.URL, tt.wantAssets), page.Assets)
		})
	}
}

func absolutize(baseURL string, links []crawler.BrokenLink) []crawler.BrokenLink {
	result := make([]crawler.BrokenLink, 0, len(links))
	for _, link := range links {
		link.URL = baseURL + link.URL
		result = append(result, link)
	}
	return result
}

func absolutizeAssets(baseURL string, assets []crawler.Asset) []crawler.Asset {
	result := make([]crawler.Asset, 0, len(assets))
	for _, asset := range assets {
		asset.URL = baseURL + asset.URL
		result = append(result, asset)
	}
	return result
}

func TestAnalyzeBrokenLinksIgnoresUnsupported(t *testing.T) {
	t.Parallel()

	body := loadFixture(t, "broken_links_ignore.html")
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			writeBody(t, w, body)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)

	assert.Equal(t, srv.URL, rep.Pages[0].URL)
	assert.Equal(t, 0, rep.Pages[0].Depth)
	assert.Equal(t, []crawler.BrokenLink{
		{URL: srv.URL + brokenPath, StatusCode: http.StatusNotFound, Error: http.StatusText(http.StatusNotFound)},
	}, rep.Pages[0].BrokenLinks)
}

type unreachableLinkTransport struct{}

func (unreachableLinkTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.String() == exampleURL+"/" {
		return testResponse(req, `<a href="https://offline.example/page">offline</a>`), nil
	}
	return nil, errors.New("connection failed")
}

func TestAnalyzeBrokenLinkNetworkError(t *testing.T) {
	t.Parallel()

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		HTTPClient: &http.Client{Transport: unreachableLinkTransport{}},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	require.Len(t, rep.Pages[0].BrokenLinks, 1)

	got := rep.Pages[0].BrokenLinks[0]
	assert.Equal(t, "https://offline.example/page", got.URL)
	assert.Zero(t, got.StatusCode)
	assert.Contains(t, got.Error, "connection failed")
	assert.Empty(t, rep.Pages[0].Assets)
}

func TestAnalyzeBrokenLinkReportsLastAttempt(t *testing.T) {
	t.Parallel()

	var linkCalls atomic.Int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			writeBody(t, w, `<a href="/asset">asset</a>`)
		case "/asset":
			if linkCalls.Add(1) == 1 {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Timeout:    time.Second,
		Retries:    1,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, []crawler.BrokenLink{
		{URL: srv.URL + "/asset", StatusCode: http.StatusNotFound, Error: http.StatusText(http.StatusNotFound)},
	}, rep.Pages[0].BrokenLinks)
	assert.Equal(t, int32(2), linkCalls.Load(), "broken link check retries once")
}

func TestAnalyzeBrokenLinksDeduplicated(t *testing.T) {
	t.Parallel()

	var checkCalls atomic.Int32
	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			writeBody(t, w, `<a href="/broken">a</a><a href="/broken#frag">b</a><a href="/broken">c</a>`)
		case brokenPath:
			checkCalls.Add(1)
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, []crawler.BrokenLink{
		{URL: srv.URL + brokenPath, StatusCode: http.StatusNotFound, Error: http.StatusText(http.StatusNotFound)},
	}, rep.Pages[0].BrokenLinks)
	assert.Equal(t, int32(1), checkCalls.Load(), "duplicate links must be checked once per page")
}

type linkCancelingTransport struct {
	cancel context.CancelFunc
}

func (tr linkCancelingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Path == brokenPath {
		tr.cancel()
		return nil, context.Canceled
	}
	return testResponse(req, `<a href="/broken">broken</a>`), nil
}

func TestAnalyzeBrokenLinksContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        exampleURL,
		Depth:      1,
		Timeout:    time.Second,
		HTTPClient: &http.Client{Transport: linkCancelingTransport{cancel: cancel}},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Empty(t, rep.Pages[0].BrokenLinks)
	assert.NotContains(t, string(data), context.Canceled.Error())
}

type hostNormalizationTransport struct {
	upperHeadCalls atomic.Int32
	portHeadCalls  atomic.Int32
}

func (tr *hostNormalizationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := strings.ToLower(req.URL.Hostname())
	switch {
	case host == exampleHost && (req.URL.Path == "" || req.URL.Path == "/"):
		return testResponse(req, `<a href="https://EXAMPLE.com/upper">upper</a><a href="https://example.com:443/port">port</a><a href="https://external.example/x">ext</a>`), nil
	case host == exampleHost && req.URL.Path == "/upper":
		if req.Method == http.MethodHead {
			tr.upperHeadCalls.Add(1)
		}
		return testResponse(req, ""), nil
	case host == exampleHost && req.URL.Path == "/port":
		if req.Method == http.MethodHead {
			tr.portHeadCalls.Add(1)
		}
		return testResponse(req, ""), nil
	case host == externalHost && req.URL.Path == "/x":
		return responseWithStatus(req, http.StatusNotFound, ""), nil
	default:
		return nil, fmt.Errorf("unexpected request: %s", req.URL)
	}
}

func TestAnalyzeHostComparisonNormalized(t *testing.T) {
	t.Parallel()

	tr := &hostNormalizationTransport{}
	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL,
		Depth:      2,
		Timeout:    time.Second,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 3, "links with a differently cased or default-port host must stay internal")
	require.Len(t, rep.Pages[0].BrokenLinks, 1)
	assert.Equal(t, crawler.BrokenLink{
		URL:        "https://external.example/x",
		StatusCode: http.StatusNotFound,
		Error:      http.StatusText(http.StatusNotFound),
	}, rep.Pages[0].BrokenLinks[0])
	assert.Zero(t, tr.upperHeadCalls.Load(), "internal link with uppercase host must not be probed")
	assert.Zero(t, tr.portHeadCalls.Load(), "internal link with default port must not be probed")
}

type ipv6Transport struct {
	internalHeadCalls atomic.Int32
}

func (tr *ipv6Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	host := strings.ToLower(req.URL.Hostname())
	switch {
	case host == "::1" && (req.URL.Path == "" || req.URL.Path == "/"):
		return testResponse(req, `<a href="/page">page</a><a href="http://[::1]:80/port">port</a><a href="http://external.example/x">ext</a>`), nil
	case host == "::1" && (req.URL.Path == pagePath || req.URL.Path == "/port"):
		if req.Method == http.MethodHead {
			tr.internalHeadCalls.Add(1)
		}
		return testResponse(req, ""), nil
	case host == externalHost && req.URL.Path == "/x":
		return responseWithStatus(req, http.StatusNotFound, ""), nil
	default:
		return nil, fmt.Errorf("unexpected request: %s", req.URL)
	}
}

func TestAnalyzeIPv6HostNormalized(t *testing.T) {
	t.Parallel()

	tr := &ipv6Transport{}
	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        "http://[::1]/",
		Depth:      2,
		Timeout:    time.Second,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 3, "IPv6 links with the default port must stay internal")
	require.Len(t, rep.Pages[0].BrokenLinks, 1)
	assert.Equal(t, "http://external.example/x", rep.Pages[0].BrokenLinks[0].URL)
	assert.Zero(t, tr.internalHeadCalls.Load(), "internal IPv6 links must not be probed")
}
