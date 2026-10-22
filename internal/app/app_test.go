package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/urfave/cli/v3"

	"code/internal/app"
)

const commandName = "hexlet-go-crawler"

type testReport struct {
	RootURL string `json:"root_url"`
	Pages   []struct {
		URL        string `json:"url"`
		HTTPStatus int    `json:"http_status"`
		Status     string `json:"status"`
		Error      string `json:"error"`
	} `json:"pages"`
}

func newTestCommand(client *http.Client) (*cli.Command, *bytes.Buffer) {
	cmd := app.New(client)
	buf := &bytes.Buffer{}
	cmd.Writer = buf
	cmd.ErrWriter = buf
	return cmd, buf
}

func TestRunPrintsReport(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cmd, buf := newTestCommand(srv.Client())
	require.NoError(t, cmd.Run(t.Context(), []string{commandName, srv.URL}))

	var rep testReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rep), "output: %s", buf.String())
	assert.Equal(t, srv.URL, rep.RootURL)
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, http.StatusOK, rep.Pages[0].HTTPStatus)
}

type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("connection failed")
}

func TestRunNetworkErrorExitsSuccessfully(t *testing.T) {
	t.Parallel()

	cmd, buf := newTestCommand(&http.Client{Transport: failingTransport{}})
	require.NoError(t, cmd.Run(t.Context(), []string{commandName, "https://example.com"}))

	var rep testReport
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rep), "output: %s", buf.String())
	require.Len(t, rep.Pages, 1)
	assert.Equal(t, "error", rep.Pages[0].Status)
}

func TestRunRequiresURL(t *testing.T) {
	t.Parallel()

	cmd, _ := newTestCommand(&http.Client{})
	cmd.ExitErrHandler = func(context.Context, *cli.Command, error) {}

	require.Error(t, cmd.Run(t.Context(), []string{commandName}))
}
