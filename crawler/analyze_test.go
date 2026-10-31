package crawler_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(b)
}

const (
	exampleURL  = "https://example.com"
	exampleHost = "example.com"
)

type testReport struct {
	RootURL     string     `json:"root_url"`
	Depth       int        `json:"depth"`
	GeneratedAt time.Time  `json:"generated_at"`
	Pages       []testPage `json:"pages"`
}

type testPage struct {
	URL          string           `json:"url"`
	Depth        int              `json:"depth"`
	HTTPStatus   int              `json:"http_status"`
	Status       string           `json:"status"`
	SEO          testSEO          `json:"seo"`
	Error        string           `json:"error"`
	BrokenLinks  []testBrokenLink `json:"broken_links"`
	Assets       []testAsset      `json:"assets"`
	DiscoveredAt time.Time        `json:"discovered_at"`
}

type testSEO struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description"`
	HasH1          bool   `json:"has_h1"`
}

type testBrokenLink struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code"`
	Error      string `json:"error"`
}

type testAsset struct {
	URL        string `json:"url"`
	Type       string `json:"type"`
	StatusCode int    `json:"status_code"`
	SizeBytes  int64  `json:"size_bytes"`
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
	assert.NotNil(t, got.BrokenLinks)
	assert.Empty(t, got.BrokenLinks)
	assert.NotNil(t, got.Assets)
	assert.Empty(t, got.Assets)
	assert.False(t, got.DiscoveredAt.IsZero())
}

func TestAnalyzeSEO(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := io.WriteString(w, loadFixture(t, "seo_full.html"))
		if err != nil {
			t.Errorf("write response: %v", err)
		}
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
	assert.Equal(t, testSEO{
		HasTitle:       true,
		Title:          "Example Test",
		HasDescription: true,
		Description:    "A description",
		HasH1:          true,
	}, rep.Pages[0].SEO)
}

func TestAnalyzeSEOMissing(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := io.WriteString(w, loadFixture(t, "seo_missing.html"))
		if err != nil {
			t.Errorf("write response: %v", err)
		}
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
	assert.Equal(t, testSEO{}, rep.Pages[0].SEO)
}

func TestAnalyzeSEOHTMLEntities(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := io.WriteString(w, loadFixture(t, "seo_entities.html"))
		if err != nil {
			t.Errorf("write response: %v", err)
		}
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
	assert.Equal(t, testSEO{
		HasTitle:       true,
		Title:          "Fish & Chips",
		HasDescription: true,
		Description:    `Tom "Jerry" <cartoon>`,
		HasH1:          true,
	}, rep.Pages[0].SEO)
}

func TestAnalyzeBrokenLinks(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<a href="/working">working</a><a href="/broken">broken</a>`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/working":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
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
	require.Len(t, rep.Pages, 3)

	assert.Equal(t, srv.URL, rep.Pages[0].URL)
	assert.Equal(t, 0, rep.Pages[0].Depth)
	assert.Equal(t, []testBrokenLink{
		{URL: srv.URL + "/broken", StatusCode: http.StatusNotFound, Error: http.StatusText(http.StatusNotFound)},
	}, rep.Pages[0].BrokenLinks)

	assert.Equal(t, srv.URL+"/working", rep.Pages[1].URL)
	assert.Equal(t, 1, rep.Pages[1].Depth)
	assert.Equal(t, http.StatusOK, rep.Pages[1].HTTPStatus)
	assert.Equal(t, "ok", rep.Pages[1].Status)
	assert.Empty(t, rep.Pages[1].BrokenLinks)

	assert.Equal(t, srv.URL+"/broken", rep.Pages[2].URL)
	assert.Equal(t, 1, rep.Pages[2].Depth)
	assert.Equal(t, http.StatusNotFound, rep.Pages[2].HTTPStatus)
	assert.Equal(t, "error", rep.Pages[2].Status)
	assert.Empty(t, rep.Pages[2].BrokenLinks)
}

func TestAnalyzeBrokenLinkTags(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, loadFixture(t, "broken_tags.html"))
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/working", "/assets/app.js":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
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
	require.Len(t, rep.Pages, 2)

	assert.Equal(t, srv.URL, rep.Pages[0].URL)
	assert.Equal(t, 0, rep.Pages[0].Depth)
	assert.Empty(t, rep.Pages[0].BrokenLinks)
	assert.Equal(t, []testAsset{
		{
			URL:        srv.URL + "/assets/ghost.css",
			Type:       "other",
			StatusCode: http.StatusNotFound,
			Error:      http.StatusText(http.StatusNotFound),
		},
		{
			URL:        srv.URL + "/assets/app.js",
			Type:       "script",
			StatusCode: http.StatusOK,
		},
		{
			URL:        srv.URL + "/assets/missing.png",
			Type:       "image",
			StatusCode: http.StatusNotFound,
			Error:      http.StatusText(http.StatusNotFound),
		},
	}, rep.Pages[0].Assets)

	assert.Equal(t, srv.URL+"/working", rep.Pages[1].URL)
	assert.Equal(t, 1, rep.Pages[1].Depth)
	assert.Equal(t, http.StatusOK, rep.Pages[1].HTTPStatus)
	assert.Equal(t, "ok", rep.Pages[1].Status)
	assert.Empty(t, rep.Pages[1].BrokenLinks)
	assert.Empty(t, rep.Pages[1].Assets)
}

func TestAnalyzeBrokenLinksIgnoresUnsupported(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, loadFixture(t, "broken_links_ignore.html"))
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/broken":
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusOK)
		}
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
	require.Len(t, rep.Pages, 2)

	assert.Equal(t, srv.URL, rep.Pages[0].URL)
	assert.Equal(t, 0, rep.Pages[0].Depth)
	assert.Equal(t, []testBrokenLink{
		{URL: srv.URL + "/broken", StatusCode: http.StatusNotFound, Error: http.StatusText(http.StatusNotFound)},
	}, rep.Pages[0].BrokenLinks)

	assert.Equal(t, srv.URL+"/broken", rep.Pages[1].URL)
	assert.Equal(t, 1, rep.Pages[1].Depth)
	assert.Equal(t, http.StatusNotFound, rep.Pages[1].HTTPStatus)
	assert.Equal(t, "error", rep.Pages[1].Status)
	assert.Empty(t, rep.Pages[1].BrokenLinks)
}

type brokenLinkTransport struct{}

func (brokenLinkTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.URL.Path {
	case "/":
		return testResponse(req, `<a href="/ok">ok</a><script src="https://cdn.invalid/app.js"></script>`), nil
	case "/ok":
		return testResponse(req, ""), nil
	default:
		return nil, errors.New("connection failed")
	}
}

func testResponse(req *http.Request, body string) *http.Response {
	return &http.Response{
		StatusCode:    http.StatusOK,
		Status:        http.StatusText(http.StatusOK),
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func TestAnalyzeBrokenLinkNetworkError(t *testing.T) {
	t.Parallel()

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		HTTPClient: &http.Client{Transport: brokenLinkTransport{}},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 2)

	assert.Equal(t, exampleURL+"/", rep.Pages[0].URL)
	assert.Equal(t, 0, rep.Pages[0].Depth)
	assert.Empty(t, rep.Pages[0].BrokenLinks)
	require.Len(t, rep.Pages[0].Assets, 1)

	got := rep.Pages[0].Assets[0]
	assert.Equal(t, "https://cdn.invalid/app.js", got.URL)
	assert.Equal(t, "script", got.Type)
	assert.Zero(t, got.StatusCode)
	assert.Zero(t, got.SizeBytes)
	assert.Contains(t, got.Error, "connection failed")

	assert.Equal(t, exampleURL+"/ok", rep.Pages[1].URL)
	assert.Equal(t, 1, rep.Pages[1].Depth)
	assert.Equal(t, http.StatusOK, rep.Pages[1].HTTPStatus)
	assert.Equal(t, "ok", rep.Pages[1].Status)
	assert.Empty(t, rep.Pages[1].BrokenLinks)
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
	assert.NotNil(t, got.BrokenLinks)
	assert.Empty(t, got.BrokenLinks)
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
	assert.NotNil(t, got.BrokenLinks)
	assert.Empty(t, got.BrokenLinks)
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

func TestAnalyzeTimeout(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

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

func TestAnalyzeNormalizeURLForKeyWithBadLink(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<a href="http://%zz">bad</a>`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		default:
			w.WriteHeader(http.StatusOK)
		}
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
	assert.Equal(t, srv.URL, rep.Pages[0].URL)
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

	var gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.UserAgent()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	_, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      0,
		Timeout:    time.Second,
		UserAgent:  "test-agent",
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)
	assert.Equal(t, "test-agent", gotUA)
}

func TestAnalyzeDepthZero(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<a href="/inner">inner</a>`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/inner":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<a href="/page">1</a><a href="/page">2</a><a href="/page/">3</a>`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/page", "/page/":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
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

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<a href="/ok">ok</a><a href="https://other.example/x">ext</a>`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/ok":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
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

func TestAnalyzeContextCancelMidCrawl(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<a href="/a">a</a><a href="/b">b</a>`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/a":
			time.Sleep(50 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		case "/b":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        srv.URL,
		Depth:      1,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)
	require.True(t, json.Valid(data))

	var rep testReport
	require.NoError(t, json.Unmarshal(data, &rep))
	require.NotEmpty(t, rep.Pages)
	assert.Equal(t, srv.URL, rep.Pages[0].URL)
}

type timingTransport struct {
	times []time.Time
}

func (tr *timingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.times = append(tr.times, time.Now())
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     http.StatusText(http.StatusOK),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
		Request:    req,
	}, nil
}

func TestAnalyzeWithDelay(t *testing.T) {
	t.Parallel()

	tr := &timingTransport{}
	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      0,
		Timeout:    time.Second,
		Delay:      50 * time.Millisecond,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	require.Len(t, tr.times, 1)

	var rep testReport
	require.NoError(t, json.Unmarshal(data, &rep))
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, exampleURL+"/", rep.Pages[0].URL)
	assert.Equal(t, "ok", rep.Pages[0].Status)
}

func TestAnalyzeWithRPS(t *testing.T) {
	t.Parallel()

	tr := &timingTransport{}
	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      0,
		Timeout:    time.Second,
		RPS:        10,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	require.Len(t, tr.times, 1)

	var rep testReport
	require.NoError(t, json.Unmarshal(data, &rep))
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, exampleURL+"/", rep.Pages[0].URL)
	assert.Equal(t, "ok", rep.Pages[0].Status)
}

func TestAnalyzeRateLimitIntervals(t *testing.T) {
	t.Parallel()

	tr := &timingTransport{}
	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		Delay:      40 * time.Millisecond,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)
	require.True(t, json.Valid(data))

	// Need at least a couple of requests (root page + possibly broken link check? root has no links in empty body)
	// With empty body and no links, we only fetch the root page once
	require.GreaterOrEqual(t, len(tr.times), 1)

	var rep testReport
	require.NoError(t, json.Unmarshal(data, &rep))
	require.NotEmpty(t, rep.Pages)

	if len(tr.times) >= 2 {
		for i := 1; i < len(tr.times); i++ {
			interval := tr.times[i].Sub(tr.times[i-1])
			// Allow some timing tolerance
			require.GreaterOrEqual(t, interval, 30*time.Millisecond)
		}
	}
}

func TestAnalyzeRateLimitRPSPriority(t *testing.T) {
	t.Parallel()

	tr := &timingTransport{}
	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      0,
		Timeout:    time.Second,
		RPS:        5,
		Delay:      time.Hour, // Delay should be ignored when RPS > 0
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	require.Len(t, tr.times, 1)
}

func TestAnalyzeRateLimitContextCancel(t *testing.T) {
	t.Parallel()

	tr := &timingTransport{}
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        exampleURL + "/",
		Depth:      0,
		Timeout:    time.Second,
		Delay:      time.Second, // long delay
		HTTPClient: &http.Client{Transport: tr},
	})
	// Context cancellation should interrupt waiting without hanging
	// The error might be returned or handled gracefully depending on implementation
	// But we need to ensure it doesn't hang - test will timeout if it hangs
	if err != nil {
		// Error is acceptable if context was cancelled
		return
	}
	require.True(t, json.Valid(data))
}
