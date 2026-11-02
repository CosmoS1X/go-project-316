package crawler_test

import (
	"encoding/json"
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

func newTestServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func writeBody(t *testing.T, w http.ResponseWriter, body string) {
	t.Helper()

	if _, err := io.WriteString(w, body); err != nil {
		t.Errorf("write response: %v", err)
	}
}
