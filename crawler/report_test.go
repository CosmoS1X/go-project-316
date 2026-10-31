package crawler_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

const (
	referenceRootURL  = "https://example.com"
	referenceLogoSize = 12345
)

type referenceTransport struct {
	pageHTML string
}

func (tr referenceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	switch {
	case req.URL.Host == exampleHost && req.URL.Path == "/static/logo.png":
		return referenceResponse(req, http.StatusOK, strings.Repeat("x", referenceLogoSize)), nil
	case req.URL.Host == exampleHost:
		return referenceResponse(req, http.StatusOK, tr.pageHTML), nil
	case req.URL.Host == "external.example" && req.URL.Path == "/missing":
		return referenceResponse(req, http.StatusNotFound, ""), nil
	default:
		return nil, fmt.Errorf("unexpected request: %s", req.URL)
	}
}

func referenceResponse(req *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode:    statusCode,
		Status:        http.StatusText(statusCode),
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func referenceOptions(t *testing.T, indent bool) crawler.Options {
	t.Helper()

	return crawler.Options{
		URL:        referenceRootURL,
		Depth:      1,
		Timeout:    time.Second,
		IndentJSON: indent,
		HTTPClient: &http.Client{
			Transport: referenceTransport{pageHTML: loadFixture(t, "reference_page.html")},
		},
	}
}

var timeFieldPattern = regexp.MustCompile(`("(?:generated_at|discovered_at)":\s*)"[^"]*"`)

func normalizeTimes(data string) string {
	return timeFieldPattern.ReplaceAllString(data, `$1"__TIME__"`)
}

func TestAnalyzeMatchesReferenceReport(t *testing.T) {
	t.Parallel()

	data, err := crawler.Analyze(t.Context(), referenceOptions(t, true))
	require.NoError(t, err)

	want := normalizeTimes(strings.TrimSuffix(loadFixture(t, "report_reference.json"), "\n"))
	assert.Equal(t, want, normalizeTimes(string(data)))
}

func TestAnalyzeIndentJSONOnlyChangesFormatting(t *testing.T) {
	t.Parallel()

	compact, err := crawler.Analyze(t.Context(), referenceOptions(t, false))
	require.NoError(t, err)

	pretty, err := crawler.Analyze(t.Context(), referenceOptions(t, true))
	require.NoError(t, err)

	normalizedPretty := normalizeTimes(string(pretty))
	var compactedPretty bytes.Buffer
	require.NoError(t, json.Compact(&compactedPretty, []byte(normalizedPretty)))

	assert.Equal(t, normalizeTimes(string(compact)), compactedPretty.String())
	assert.NotContains(t, string(compact), "\n")
	assert.Contains(t, string(pretty), "\n")

	wantCompact := normalizeTimes(strings.TrimSuffix(loadFixture(t, "report_reference.json"), "\n"))
	var compactFixture bytes.Buffer
	require.NoError(t, json.Compact(&compactFixture, []byte(wantCompact)))
	assert.Equal(t, compactFixture.String(), normalizeTimes(string(compact)))
}

func TestAnalyzeTimestampsISO8601(t *testing.T) {
	t.Parallel()

	data, err := crawler.Analyze(t.Context(), referenceOptions(t, true))
	require.NoError(t, err)

	var raw struct {
		GeneratedAt string `json:"generated_at"`
		Pages       []struct {
			DiscoveredAt string `json:"discovered_at"`
		} `json:"pages"`
	}
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Len(t, raw.Pages, 1)

	for _, value := range []string{raw.GeneratedAt, raw.Pages[0].DiscoveredAt} {
		parsed, parseErr := time.Parse(time.RFC3339, value)
		require.NoError(t, parseErr)
		assert.Equal(t, time.UTC, parsed.Location())
		assert.Equal(t, value, parsed.UTC().Format(time.RFC3339))
	}
}
