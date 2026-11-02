package crawler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

func TestAnalyzeDepthZero(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			writeBody(t, w, `<a href="/inner">inner</a>`)
		case "/inner":
			w.WriteHeader(http.StatusOK)
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
	assert.Equal(t, srv.URL, rep.Pages[0].URL)
	assert.Equal(t, 0, rep.Pages[0].Depth)
	assert.Equal(t, "ok", rep.Pages[0].Status)
}

func TestAnalyzeDuplicateInternalLinks(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			writeBody(t, w, `<a href="/page">1</a><a href="/page">2</a><a href="/page/">3</a>`)
		case pagePath, pagePath + "/":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      2,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 2)
	assert.Equal(t, srv.URL, rep.Pages[0].URL)
	assert.Equal(t, 0, rep.Pages[0].Depth)
	assert.Equal(t, "ok", rep.Pages[0].Status)
	assert.Equal(t, 1, rep.Pages[1].Depth)
	assert.Equal(t, "ok", rep.Pages[1].Status)
	assert.NotEqual(t, rep.Pages[1].URL, rep.Pages[0].URL)
}

func TestAnalyzeExternalLinkNotTraversed(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			writeBody(t, w, `<a href="/ok">ok</a><a href="https://other.example/x">ext</a>`)
		case "/ok":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      2,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 2)

	assert.Equal(t, srv.URL, rep.Pages[0].URL)
	assert.Equal(t, 0, rep.Pages[0].Depth)
	assert.Equal(t, srv.URL+"/ok", rep.Pages[1].URL)
	assert.Equal(t, 1, rep.Pages[1].Depth)
	assert.Equal(t, "ok", rep.Pages[1].Status)

	for _, p := range rep.Pages {
		assert.NotContains(t, p.URL, "other.example")
	}
}

func TestAnalyzeMalformedNavigationLink(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			writeBody(t, w, `<a href="http://%zz">bad</a>`)
		default:
			w.WriteHeader(http.StatusOK)
		}
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      2,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, srv.URL, rep.Pages[0].URL)
}

func TestAnalyzeContextCancelMidCrawl(t *testing.T) {
	t.Parallel()

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			writeBody(t, w, `<a href="/a">a</a><a href="/b">b</a>`)
		case "/a":
			time.Sleep(50 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		case "/b":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        srv.URL,
		Depth:      2,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)
	require.True(t, json.Valid(data))

	var rep crawler.Report
	require.NoError(t, json.Unmarshal(data, &rep))
	require.NotEmpty(t, rep.Pages)
	assert.Equal(t, srv.URL, rep.Pages[0].URL)
}
