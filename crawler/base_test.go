package crawler_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
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
