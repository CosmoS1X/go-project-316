package crawler_test

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

func TestAnalyzeLimitsPageBody(t *testing.T) {
	t.Parallel()

	// Keep in sync with maxPageBytes in crawler/fetch.go.
	const maxPageBytes = 10 << 20

	body := `<title>Start</title>` +
		strings.Repeat("x", maxPageBytes) +
		`<a href="/next">next</a>`

	srv := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			// The client stops reading after maxPageBytes, so a write error is expected.
			if _, err := io.WriteString(w, body); err != nil {
				return
			}
		case "/next":
			writeBody(t, w, "")
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        srv.URL,
		Depth:      2,
		Timeout:    5 * time.Second,
		HTTPClient: srv.Client(),
	})
	require.NoError(t, err)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1, "links beyond the body limit must not be discovered")
	assert.Equal(t, "Start", rep.Pages[0].SEO.Title)
	assert.Equal(t, "ok", rep.Pages[0].Status)
}
