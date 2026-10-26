package crawler

import (
	"encoding/json"
	"time"
)

const (
	statusOK    = "ok"
	statusError = "error"
)

type report struct {
	RootURL     string    `json:"root_url"`
	Depth       int       `json:"depth"`
	GeneratedAt time.Time `json:"generated_at"`
	Pages       []page    `json:"pages"`
}

type page struct {
	URL          string       `json:"url"`
	Depth        int          `json:"depth"`
	HTTPStatus   int          `json:"http_status"`
	Status       string       `json:"status"`
	SEO          seo          `json:"seo"`
	Error        string       `json:"error"`
	BrokenLinks  []brokenLink `json:"broken_links"`
	DiscoveredAt time.Time    `json:"discovered_at"`
}

type seo struct {
	HasTitle       bool   `json:"has_title"`
	Title          string `json:"title"`
	HasDescription bool   `json:"has_description"`
	Description    string `json:"description"`
	HasH1          bool   `json:"has_h1"`
}

type brokenLink struct {
	URL        string `json:"url"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (r report) marshal(indent bool) ([]byte, error) {
	if indent {
		return json.MarshalIndent(r, "", "  ")
	}
	return json.Marshal(r)
}
