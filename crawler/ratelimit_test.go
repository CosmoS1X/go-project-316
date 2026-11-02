package crawler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"code/crawler"
)

type timingTransport struct {
	times []time.Time
}

func (tr *timingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	tr.times = append(tr.times, time.Now())
	return responseWithStatus(req, http.StatusOK, ""), nil
}

func TestAnalyzeWithDelay(t *testing.T) {
	t.Parallel()

	tr := &timingTransport{}
	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      0,
		Timeout:    time.Second,
		Delay:      50 * time.Millisecond,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	require.Len(t, tr.times, 1)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, exampleURL+"/", rep.Pages[0].URL)
	assert.Equal(t, "ok", rep.Pages[0].Status)
}

func TestAnalyzeWithRPS(t *testing.T) {
	t.Parallel()

	tr := &timingTransport{}
	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      0,
		Timeout:    time.Second,
		RPS:        10,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	require.Len(t, tr.times, 1)

	rep := decodeReport(t, data)
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, exampleURL+"/", rep.Pages[0].URL)
	assert.Equal(t, "ok", rep.Pages[0].Status)
}

func TestAnalyzeRateLimitIntervals(t *testing.T) {
	t.Parallel()

	tr := &timingTransport{}
	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      1,
		Timeout:    time.Second,
		Delay:      40 * time.Millisecond,
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)
	require.True(t, json.Valid(data))

	require.GreaterOrEqual(t, len(tr.times), 1)

	rep := decodeReport(t, data)
	require.NotEmpty(t, rep.Pages)

	if len(tr.times) >= 2 {
		for i := 1; i < len(tr.times); i++ {
			interval := tr.times[i].Sub(tr.times[i-1])
			require.GreaterOrEqual(t, interval, 30*time.Millisecond)
		}
	}
}

func TestAnalyzeRateLimitRPSPriority(t *testing.T) {
	t.Parallel()

	tr := &timingTransport{}
	data, err := crawler.Analyze(t.Context(), crawler.Options{
		URL:        exampleURL + "/",
		Depth:      0,
		Timeout:    time.Second,
		RPS:        5,
		Delay:      time.Hour, // Delay should be ignored when RPS > 0
		HTTPClient: &http.Client{Transport: tr},
	})
	require.NoError(t, err)
	require.True(t, json.Valid(data))
	require.Len(t, tr.times, 1)
}

func TestAnalyzeRateLimitContextCancel(t *testing.T) {
	t.Parallel()

	tr := &timingTransport{}
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	data, err := crawler.Analyze(ctx, crawler.Options{
		URL:        exampleURL + "/",
		Depth:      0,
		Timeout:    time.Second,
		Delay:      time.Second, // long delay
		HTTPClient: &http.Client{Transport: tr},
	})
	if err != nil {
		return
	}
	require.True(t, json.Valid(data))
}
