package crawler_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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
	URL          string           `json:"url"`
	Depth        int              `json:"depth"`
	HTTPStatus   int              `json:"http_status"`
	Status       string           `json:"status"`
	SEO          testSEO          `json:"seo"`
	Error        string           `json:"error"`
	BrokenLinks  []testBrokenLink `json:"broken_links"`
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
	assert.False(t, got.DiscoveredAt.IsZero())
}

func TestAnalyzeSEO(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, err := io.WriteString(w, `<!doctype html>
<html>
<head>
<title> Example Test </title>
<meta content="A description" name="description">
</head>
<body><h1>Main <span>Heading</span></h1></body>
</html>`)
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
		_, err := io.WriteString(w, `<!doctype html>
<html>
<head></head>
<body><p>No SEO tags</p></body>
</html>`)
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
		_, err := io.WriteString(w, `<!doctype html>
<html>
<head>
<title>Fish &amp; Chips</title>
<meta name="description" content="Tom &quot;Jerry&quot; &lt;cartoon&gt;">
</head>
<body><h1>Caf&eacute; &amp; Bar</h1></body>
</html>`)
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
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, []testBrokenLink{
		{URL: srv.URL + "/broken", StatusCode: http.StatusNotFound},
	}, rep.Pages[0].BrokenLinks)
}

func TestAnalyzeBrokenLinkTags(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<!doctype html>
<html>
<head><link href="/assets/ghost.css"></head>
<body>
<a href="/working">working</a>
<script src="/assets/app.js"></script>
<img src="/assets/missing.png">
</body>
</html>`)
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
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, []testBrokenLink{
		{URL: srv.URL + "/assets/ghost.css", StatusCode: http.StatusNotFound},
		{URL: srv.URL + "/assets/missing.png", StatusCode: http.StatusNotFound},
	}, rep.Pages[0].BrokenLinks)
}

func TestAnalyzeBrokenLinksIgnoresUnsupported(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<a href="">empty</a>
<a href="   ">spaces</a>
<a href="#top">fragment</a>
<a href="mailto:test@example.com">mail</a>
<a href="javascript:void(0)">js</a>
<a href="ftp://example.com/file">ftp</a>
<a href="http://[::1">malformed</a>
<a href="https:///no-host">no host</a>
<a href="/broken">broken</a>`)
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
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, []testBrokenLink{
		{URL: srv.URL + "/broken", StatusCode: http.StatusNotFound},
	}, rep.Pages[0].BrokenLinks)
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
	require.Len(t, rep.Pages, 1)
	require.Len(t, rep.Pages[0].BrokenLinks, 1)

	got := rep.Pages[0].BrokenLinks[0]
	assert.Equal(t, "https://cdn.invalid/app.js", got.URL)
	assert.Zero(t, got.StatusCode)
	assert.Contains(t, got.Error, "connection failed")
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := crawler.Analyze(t.Context(), tt.opts)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}
