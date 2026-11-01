package crawler_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

const fallbackLogoPath = "/logo.png"

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
	require.Len(t, checks, 3)

	assert.Equal(t, methodCheck{method: http.MethodGet, url: exampleURL + "/"}, checks[0])
	assert.Equal(t, methodCheck{method: http.MethodHead, url: "https://assets.example/logo.png"}, checks[1])
	assert.Equal(t, methodCheck{method: http.MethodHead, url: "https://links.example/page"}, checks[2])
}

func TestAnalyzeFallsBackToGetOnMethodNotAllowed(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		methods = make(map[string][]string)
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods[r.URL.Path] = append(methods[r.URL.Path], r.Method)
		mu.Unlock()

		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<a href="/link">link</a><img src="`+fallbackLogoPath+`">`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/link", fallbackLogoPath:
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			_, err := io.WriteString(w, "logo!")
			if err != nil {
				t.Errorf("write response: %v", err)
			}
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
	assert.Empty(t, rep.Pages[0].BrokenLinks)
	require.Len(t, rep.Pages[0].Assets, 1)
	assert.Equal(t, int64(len("logo!")), rep.Pages[0].Assets[0].SizeBytes)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{http.MethodHead, http.MethodGet}, methods["/link"])
	assert.Equal(t, []string{http.MethodHead, http.MethodGet}, methods[fallbackLogoPath])
}
