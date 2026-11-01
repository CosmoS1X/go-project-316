package crawler_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

const assetLogoPath = "/logo.png"

func TestAnalyzeAssetFieldPresence(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<img src="/ok.png"><script src="/missing.js"></script>`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/ok.png":
			_, err := io.WriteString(w, "png")
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

	var raw struct {
		Pages []struct {
			Assets []map[string]json.RawMessage `json:"assets"`
		} `json:"pages"`
	}
	require.NoError(t, json.Unmarshal(data, &raw))
	require.Len(t, raw.Pages, 1)
	require.Len(t, raw.Pages[0].Assets, 2)

	for _, rawAsset := range raw.Pages[0].Assets {
		assert.Contains(t, rawAsset, "url")
		assert.Contains(t, rawAsset, "type")
	}

	okAsset := raw.Pages[0].Assets[0]
	assert.Contains(t, okAsset, "status_code")
	assert.Contains(t, okAsset, "size_bytes")
	assert.NotContains(t, okAsset, "error")

	missingAsset := raw.Pages[0].Assets[1]
	assert.Contains(t, missingAsset, "status_code")
	assert.NotContains(t, missingAsset, "size_bytes")
	assert.Contains(t, missingAsset, "error")

	rep := decodeReport(t, data)
	assets := rep.Pages[0].Assets
	require.Len(t, assets, 2)

	assert.Equal(t, srv.URL+"/ok.png", assets[0].URL)
	assert.Equal(t, "image", assets[0].Type)
	assert.Equal(t, http.StatusOK, assets[0].StatusCode)
	assert.Equal(t, int64(3), assets[0].SizeBytes)
	assert.Empty(t, assets[0].Error)

	assert.Equal(t, srv.URL+"/missing.js", assets[1].URL)
	assert.Equal(t, "script", assets[1].Type)
	assert.Equal(t, http.StatusNotFound, assets[1].StatusCode)
	assert.Zero(t, assets[1].SizeBytes)
	assert.Equal(t, http.StatusText(http.StatusNotFound), assets[1].Error)
}

func TestAnalyzeAssetRequestsCachedAcrossPages(t *testing.T) {
	t.Parallel()

	var logoCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<a href="/second">second</a><img src="`+assetLogoPath+`">`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case "/second":
			_, err := io.WriteString(w, `<img src="`+assetLogoPath+`">`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case assetLogoPath:
			logoCalls.Add(1)
			_, err := io.WriteString(w, "logo")
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
		Depth:      2,
		Timeout:    time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 2)
	assert.Equal(t, int32(1), logoCalls.Load(), "a cached asset must be requested only once")

	var rootAsset, secondAsset testAsset
	for _, p := range rep.Pages {
		require.Len(t, p.Assets, 1)
		switch p.URL {
		case srv.URL:
			rootAsset = p.Assets[0]
		case srv.URL + "/second":
			secondAsset = p.Assets[0]
		}
	}

	assert.Equal(t, rootAsset, secondAsset)
	assert.Equal(t, srv.URL+assetLogoPath, rootAsset.URL)
	assert.Equal(t, "image", rootAsset.Type)
	assert.Equal(t, int64(len("logo")), rootAsset.SizeBytes)
}

func TestAnalyzeAssetWithoutContentLength(t *testing.T) {
	t.Parallel()

	const logoBody = "0123456789"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<img src="`+assetLogoPath+`">`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		case assetLogoPath:
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Error("response writer does not support flushing")
				return
			}
			flusher.Flush()
			_, err := io.WriteString(w, logoBody)
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
	require.Len(t, rep.Pages[0].Assets, 1)

	got := rep.Pages[0].Assets[0]
	assert.Equal(t, http.StatusOK, got.StatusCode)
	assert.Equal(t, int64(len(logoBody)), got.SizeBytes)
	assert.Empty(t, got.Error)
}

func TestAnalyzeAssetHTTPError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<script src="/missing.js"></script>`)
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
	require.Len(t, rep.Pages[0].Assets, 1)

	got := rep.Pages[0].Assets[0]
	assert.Equal(t, srv.URL+"/missing.js", got.URL)
	assert.Equal(t, "script", got.Type)
	assert.Equal(t, http.StatusNotFound, got.StatusCode)
	assert.Zero(t, got.SizeBytes)
	assert.Equal(t, http.StatusText(http.StatusNotFound), got.Error)
}

func TestAnalyzeAssetTypesAndDeduplication(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, err := io.WriteString(w, `<img src="/a.png"><img src="/a.png"><script src="/app.js"></script><link rel="stylesheet" href="/s.css"><link rel="icon" href="/f.ico">`)
			if err != nil {
				t.Errorf("write response: %v", err)
			}
		default:
			_, err := io.WriteString(w, "asset")
			if err != nil {
				t.Errorf("write response: %v", err)
			}
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

	assets := rep.Pages[0].Assets
	require.Len(t, assets, 3, "duplicate asset URLs must be reported once")
	assert.Equal(t, []string{
		srv.URL + "/a.png",
		srv.URL + "/app.js",
		srv.URL + "/s.css",
	}, []string{assets[0].URL, assets[1].URL, assets[2].URL})
	assert.Equal(t, []string{"image", "script", "style"},
		[]string{assets[0].Type, assets[1].Type, assets[2].Type})
}

type assetFailingTransport struct{}

func (assetFailingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Host == exampleHost {
		return testResponse(req, `<img src="https://cdn.invalid/logo.png">`), nil
	}
	return nil, errors.New("connection failed")
}

func TestAnalyzeAssetNetworkError(t *testing.T) {
	t.Parallel()

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL,
		Depth:      0,
		Timeout:    time.Second,
		HTTPClient: &http.Client{Transport: assetFailingTransport{}},
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	require.Len(t, rep.Pages[0].Assets, 1)

	got := rep.Pages[0].Assets[0]
	assert.Equal(t, "https://cdn.invalid/logo.png", got.URL)
	assert.Equal(t, "image", got.Type)
	assert.Zero(t, got.StatusCode)
	assert.Zero(t, got.SizeBytes)
	assert.Contains(t, got.Error, "connection failed")
}

func TestAnalyzePageErrorHasEmptyAssets(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      0,
		Timeout:    time.Second,
		IndentJSON: true,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Empty(t, rep.Pages[0].Assets)
	require.True(t, strings.Contains(string(data), `"assets": []`))
}
