package crawler_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

const contentLengthHeader = "Content-Length"

type contractResponse struct {
	status           int
	body             string
	headers          http.Header
	contentLength    int64
	hasContentLength bool
}

type contractTransport struct {
	responses map[string]contractResponse
}

func (m contractTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.String()
	resp, ok := m.responses[key]
	if !ok {
		return nil, fmt.Errorf("unexpected request %s", key)
	}

	header := http.Header{}
	for name, values := range resp.headers {
		for _, value := range values {
			header.Add(name, value)
		}
	}

	contentLength := int64(len(resp.body))
	if resp.hasContentLength {
		contentLength = resp.contentLength
	}

	return &http.Response{
		StatusCode:    resp.status,
		Header:        header,
		Body:          io.NopCloser(strings.NewReader(resp.body)),
		ContentLength: contentLength,
		Request:       req,
	}, nil
}

func TestAnalyzeOptionsAndReportContract(t *testing.T) {
	t.Parallel()

	rootURL := "https://example.com"
	rootHTML := loadFixture(t, "contract_page.html")

	client := &http.Client{
		Transport: contractTransport{
			responses: map[string]contractResponse{
				"GET https://example.com": {
					status: http.StatusOK,
					body:   rootHTML,
					headers: http.Header{
						"Content-Type": []string{"text/html"},
					},
				},
				"HEAD https://example.com/about": {
					status: http.StatusOK,
				},
				"HEAD https://example.com/missing": {
					status: http.StatusNotFound,
				},
				"HEAD https://example.com/static/logo.png": {
					status:           http.StatusOK,
					headers:          http.Header{contentLengthHeader: []string{"16"}},
					contentLength:    16,
					hasContentLength: true,
				},
				"HEAD https://example.com/static/app.js": {
					status:           http.StatusOK,
					headers:          http.Header{contentLengthHeader: []string{"32"}},
					contentLength:    32,
					hasContentLength: true,
				},
				"HEAD https://example.com/static/app.css": {
					status:           http.StatusOK,
					headers:          http.Header{contentLengthHeader: []string{"24"}},
					contentLength:    24,
					hasContentLength: true,
				},
			},
		},
	}

	opts := crawler.Options{
		URL:         rootURL,
		Depth:       1,
		Retries:     0,
		Delay:       0,
		Timeout:     time.Second,
		Concurrency: 1,
		IndentJSON:  false,
		HTTPClient:  client,
	}

	data, err := crawler.Analyze(t.Context(), opts)
	require.NoError(t, err)

	var report crawler.Report
	require.NoError(t, json.Unmarshal(data, &report))

	assert.Equal(t, rootURL, report.RootURL)
	assert.Equal(t, opts.Depth, report.Depth)
	require.Len(t, report.Pages, 1)

	page := report.Pages[0]
	assert.Equal(t, rootURL, page.URL)
	assert.Equal(t, "Home", page.SEO.Title)
	assert.True(t, page.SEO.HasTitle)
	assert.Equal(t, "Root page description", page.SEO.Description)
	assert.True(t, page.SEO.HasDescription)
	assert.True(t, page.SEO.HasH1)

	require.Len(t, page.Assets, 3)
	assert.Equal(t, "https://example.com/static/logo.png", page.Assets[0].URL)
	assert.Equal(t, "image", page.Assets[0].Type)
	assert.Equal(t, "https://example.com/static/app.js", page.Assets[1].URL)
	assert.Equal(t, "script", page.Assets[1].Type)
	assert.Equal(t, "https://example.com/static/app.css", page.Assets[2].URL)
	assert.Equal(t, "style", page.Assets[2].Type)

	require.Len(t, page.BrokenLinks, 1)
	assert.Equal(t, "https://example.com/missing", page.BrokenLinks[0].URL)
	assert.Equal(t, http.StatusNotFound, page.BrokenLinks[0].StatusCode)
}
