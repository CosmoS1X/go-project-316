package crawler_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"code/crawler"
)

const (
	exampleURL      = "https://example.com"
	exampleHost     = "example.com"
	externalHost    = "external.example"
	pagePath        = "/page"
	logoPath        = "/logo.png"
	testHTMLBody    = "<html></html>"
	assetTypeImage  = "image"
	assetTypeScript = "script"
	assetTypeStyle  = "style"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name))
	require.NoError(t, err)
	return string(b)
}

func decodeReport(t *testing.T, data []byte) crawler.Report {
	t.Helper()

	var rep crawler.Report
	require.NoError(t, json.Unmarshal(data, &rep))
	return rep
}

func analyzeReport(t *testing.T, opts crawler.Options) crawler.Report {
	t.Helper()

	data, err := crawler.Analyze(t.Context(), opts)
	require.NoError(t, err)
	return decodeReport(t, data)
}

func testResponse(req *http.Request, body string) *http.Response {
	return responseWithStatus(req, http.StatusOK, body)
}

func responseWithStatus(req *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode:    statusCode,
		Status:        http.StatusText(statusCode),
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func responseWithContentLength(req *http.Request, contentLength int64) *http.Response {
	resp := responseWithStatus(req, http.StatusOK, "")
	resp.ContentLength = contentLength
	return resp
}

func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// newPageServer serves pageHTML at the root and the configured status codes for
// other paths. Unknown paths return 404 so unexpected probes are visible.
func newPageServer(t *testing.T, pageHTML string, statuses map[string]int) *httptest.Server {
	t.Helper()

	return newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			writeBody(t, w, pageHTML)
			return
		}
		statusCode, ok := statuses[r.URL.Path]
		if !ok {
			statusCode = http.StatusNotFound
		}
		w.WriteHeader(statusCode)
	})
}

func writeBody(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()

	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection failed")
}
