package crawler_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/charmap"

	"code/crawler"
)

func encodeWindows1251(t *testing.T, text string) []byte {
	t.Helper()

	encoded, err := charmap.Windows1251.NewEncoder().Bytes([]byte(text))
	require.NoError(t, err)
	return encoded
}

func writeRawBody(t *testing.T, w http.ResponseWriter, body []byte) {
	t.Helper()

	if _, err := w.Write(body); err != nil {
		t.Errorf("write response: %v", err)
	}
}

func TestAnalyzeDecodesContentTypeCharset(t *testing.T) {
	t.Parallel()

	page := `<html><head><title>Привет</title>` +
		`<meta name="description" content="Описание"></head>` +
		`<body><h1>Заголовок</h1></body></html>`

	srv := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=windows-1251")
		writeRawBody(t, w, encodeWindows1251(t, page))
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

	seo := rep.Pages[0].SEO
	assert.True(t, seo.HasTitle)
	assert.Equal(t, "Привет", seo.Title)
	assert.True(t, seo.HasDescription)
	assert.Equal(t, "Описание", seo.Description)
	assert.True(t, seo.HasH1)
}

func TestAnalyzeDecodesMetaCharset(t *testing.T) {
	t.Parallel()

	page := `<html><head><meta charset="windows-1251"><title>Привет</title></head></html>`

	srv := newTestServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		writeRawBody(t, w, encodeWindows1251(t, page))
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
	assert.Equal(t, "Привет", rep.Pages[0].SEO.Title)
}
