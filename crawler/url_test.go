package crawler_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/CosmoS1X/web-crawler/crawler"
)

func TestAnalyzeBaseHrefAffectsNavigation(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			writeBody(t, w, `<base href="/section/"><a href="child">child</a>`)
		case "/section/child":
			writeBody(t, w, "")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL + "/page",
		Depth:      2,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 2)
	assert.Equal(t, srv.URL+"/page", rep.Pages[0].URL)
	assert.Equal(t, srv.URL+"/section/child", rep.Pages[1].URL)
}

func TestAnalyzeBaseHrefAffectsAssets(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			writeBody(t, w, `<base href="/static/"><img src="logo.png">`)
		case "/static/logo.png":
			w.Header().Set("Content-Length", "4")
			writeBody(t, w, "logo")
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
	require.Len(t, rep.Pages[0].Assets, 1)

	asset := rep.Pages[0].Assets[0]
	assert.Equal(t, srv.URL+"/static/logo.png", asset.URL)
	assert.Equal(t, assetTypeImage, asset.Type)
	assert.Equal(t, int64(4), asset.SizeBytes)
}

func TestAnalyzeBaseHrefInvalidFallsBackToPageURL(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dir/page":
			writeBody(t, w, `<base href="ftp://example.com/"><a href="next">next</a>`)
		case "/dir/next":
			writeBody(t, w, "")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL + "/dir/page",
		Depth:      2,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 2)
	assert.Equal(t, srv.URL+"/dir/next", rep.Pages[1].URL)
}

func TestAnalyzeFirstBaseHrefWins(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/dir/page":
			writeBody(t, w, `<base href="/first/"><base href="/second/"><a href="child">child</a>`)
		case "/first/child":
			writeBody(t, w, "")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL + "/dir/page",
		Depth:      2,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 2)
	assert.Equal(t, srv.URL+"/first/child", rep.Pages[1].URL)
}

func TestAnalyzeUsesFirstValidBaseHref(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		bases string
	}{
		{name: "base without href", bases: `<base target="_blank">`},
		{name: "empty base href", bases: `<base href="">`},
		{name: "invalid base href", bases: `<base href="ftp://example.com/">`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := newPageServer(t, tt.bases+`<base href="/section/"><a href="child">child</a>`, map[string]int{
				"/section/child": http.StatusOK,
			})
			rep := analyzeReport(t, crawler.Options{
				URL:        srv.URL,
				Depth:      2,
				Timeout:    time.Second,
				HTTPClient: srv.Client(),
			})
			require.Len(t, rep.Pages, 2)
			assert.Equal(t, srv.URL+"/section/child", rep.Pages[1].URL)
		})
	}
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
