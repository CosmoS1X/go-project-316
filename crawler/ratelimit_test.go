package crawler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

const rateLimitPageHTML = `<a href="https://one.example/a">a</a><a href="https://two.example/b">b</a>`

type timedTransport struct {
	mu        sync.Mutex
	times     []time.Time
	pageURL   string
	pageHTML  string
	onFirst   func()
	firstOnce sync.Once
}

func (tr *timedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.mu.Lock()
	tr.times = append(tr.times, time.Now())
	tr.mu.Unlock()

	if tr.onFirst != nil {
		tr.firstOnce.Do(tr.onFirst)
	}
	if req.URL.String() == tr.pageURL {
		return testResponse(req, tr.pageHTML), nil
	}
	return testResponse(req, ""), nil
}

func (tr *timedTransport) callTimes() []time.Time {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	return append([]time.Time(nil), tr.times...)
}

func TestAnalyzeDelayEnforcesInterval(t *testing.T) {
	t.Parallel()

	tr := &timedTransport{pageURL: exampleURL + "/", pageHTML: rateLimitPageHTML}
	_, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		Delay:      25 * time.Millisecond,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)

	times := tr.callTimes()
	require.Len(t, times, 3, "root page plus two external link checks")
	for i := 1; i < len(times); i++ {
		assert.GreaterOrEqual(t, times[i].Sub(times[i-1]), 15*time.Millisecond,
			"delay must space consecutive requests")
	}
}

func TestAnalyzeRPSOverridesDelay(t *testing.T) {
	t.Parallel()

	tr := &timedTransport{pageURL: exampleURL + "/", pageHTML: rateLimitPageHTML}
	startedAt := time.Now()
	_, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		RPS:        1000,
		Delay:      time.Second,
		HTTPClient: &http.Client{Transport: tr},
	})
	elapsed := time.Since(startedAt)
	require.NoError(t, err)

	require.Len(t, tr.callTimes(), 3)
	assert.Less(t, elapsed, 500*time.Millisecond, "Delay must be ignored when RPS is set")
}

func TestAnalyzeRateLimitContextCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	tr := &timedTransport{
		pageURL:  exampleURL + "/",
		pageHTML: `<a href="https://offline.example/page">offline</a>`,
		onFirst: func() {
			time.AfterFunc(50*time.Millisecond, cancel)
		},
	}

	startedAt := time.Now()
	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		Delay:      time.Second,
		HTTPClient: &http.Client{Transport: tr},
	})
	elapsed := time.Since(startedAt)
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	assert.Less(t, elapsed, 800*time.Millisecond, "cancellation must interrupt the limiter wait")

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Empty(t, rep.Pages[0].BrokenLinks)
	assert.NotContains(t, string(data), context.Canceled.Error())
}
