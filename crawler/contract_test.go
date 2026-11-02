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

	rootHTML := loadFixture(t, "contract_page.html")

	client := &http.Client{
		Transport: contractTransport{
			responses: map[string]contractResponse{
				"GET " + exampleURL: {
					status: http.StatusOK,
					body:   rootHTML,
					headers: http.Header{
						"Content-Type": []string{"text/html"},
					},
				},
				"HEAD " + exampleURL + "/about": {
					status: http.StatusOK,
				},
				"HEAD " + exampleURL + "/missing": {
					status: http.StatusNotFound,
				},
				"HEAD " + exampleURL + "/static/logo.png": {
					status:           http.StatusOK,
					headers:          http.Header{contentLengthHeader: []string{"16"}},
					contentLength:    16,
					hasContentLength: true,
				},
				"HEAD " + exampleURL + "/static/app.js": {
					status:           http.StatusOK,
					headers:          http.Header{contentLengthHeader: []string{"32"}},
					contentLength:    32,
					hasContentLength: true,
				},
				"HEAD " + exampleURL + "/static/app.css": {
					status:           http.StatusOK,
					headers:          http.Header{contentLengthHeader: []string{"24"}},
					contentLength:    24,
					hasContentLength: true,
				},
			},
		},
	}

	opts := crawler.Options{
		URL:         exampleURL,
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

	assert.Equal(t, exampleURL, report.RootURL)
	assert.Equal(t, opts.Depth, report.Depth)
	require.Len(t, report.Pages, 1)

	page := report.Pages[0]
	assert.Equal(t, exampleURL, page.URL)

	require.Len(t, page.Assets, 3)
	assert.Equal(t, crawler.Asset{
		URL:        exampleURL + "/static/logo.png",
		Type:       assetTypeImage,
		SizeBytes:  16,
		StatusCode: http.StatusOK,
	}, page.Assets[0])
	assert.Equal(t, crawler.Asset{
		URL:        exampleURL + "/static/app.js",
		Type:       assetTypeScript,
		SizeBytes:  32,
		StatusCode: http.StatusOK,
	}, page.Assets[1])
	assert.Equal(t, crawler.Asset{
		URL:        exampleURL + "/static/app.css",
		Type:       assetTypeStyle,
		SizeBytes:  24,
		StatusCode: http.StatusOK,
	}, page.Assets[2])

	require.Len(t, page.BrokenLinks, 1)
	assert.Equal(t, crawler.BrokenLink{
		URL:        exampleURL + "/missing",
		StatusCode: http.StatusNotFound,
		Error:      http.StatusText(http.StatusNotFound),
	}, page.BrokenLinks[0])
}
