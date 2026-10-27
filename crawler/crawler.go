package crawler

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type crawler struct {
	opts Options
}

func newCrawler(opts Options) (*crawler, error) {
	if err := opts.validate(); err != nil {
		return nil, err
	}
	return &crawler{opts: opts}, nil
}

func (c *crawler) Run(ctx context.Context) *report {
	generatedAt := time.Now().UTC().Truncate(time.Second)

	rootURL := c.opts.URL
	rootParsed, err := url.Parse(rootURL)
	if err != nil {
		rootParsed = &url.URL{}
	}

	visited := make(map[string]struct{})
	pages := make([]page, 0)

	type queueItem struct {
		url   string
		depth int
	}

	queue := make([]queueItem, 0)
	key0 := normalizeURLForKey(rootURL)
	if key0 == "" {
		key0 = rootURL
	}
	visited[key0] = struct{}{}
	queue = append(queue, queueItem{url: rootURL, depth: 0})

	for len(queue) > 0 {
		if ctx.Err() != nil {
			break
		}

		current := queue[0]
		queue = queue[1:]

		p, body := c.visit(ctx, current.url, current.depth)
		pages = append(pages, p)

		if ctx.Err() != nil {
			break
		}

		if current.depth >= c.opts.Depth {
			continue
		}

		if p.Status != statusOK {
			continue
		}

		if len(body) == 0 {
			continue
		}

		base, err := url.Parse(current.url)
		if err != nil {
			continue
		}

		for _, raw := range extractLinks(body) {
			if ctx.Err() != nil {
				break
			}
			linkURL, ok := resolveLink(base, raw)
			if !ok {
				continue
			}
			linkParsed, err := url.Parse(linkURL)
			if err != nil {
				continue
			}
			if rootParsed.Host != "" && linkParsed.Host != rootParsed.Host {
				continue
			}
			if linkParsed.Scheme != schemeHTTP && linkParsed.Scheme != schemeHTTPS {
				continue
			}

			key := normalizeURLForKey(linkURL)
			if key == "" {
				continue
			}
			if _, exists := visited[key]; exists {
				continue
			}
			visited[key] = struct{}{}
			queue = append(queue, queueItem{url: linkURL, depth: current.depth + 1})
		}
	}

	return &report{
		RootURL:     c.opts.URL,
		Depth:       c.opts.Depth,
		GeneratedAt: generatedAt,
		Pages:       pages,
	}
}

func (c *crawler) visit(ctx context.Context, rawURL string, depth int) (p page, body []byte) {
	discoveredAt := time.Now().UTC().Truncate(time.Second)
	statusCode, body, err := c.fetch(ctx, rawURL, true)

	result := page{
		URL:          rawURL,
		Depth:        depth,
		HTTPStatus:   statusCode,
		Status:       statusOK,
		BrokenLinks:  make([]brokenLink, 0),
		DiscoveredAt: discoveredAt,
	}
	switch {
	case err != nil:
		result.Status = statusError
		result.Error = err.Error()
		return result, nil
	case statusCode >= http.StatusBadRequest:
		result.Status = statusError
		result.Error = "unexpected http status: " + strconv.Itoa(statusCode)
		return result, nil
	default:
		result.SEO = extractSEO(body)
		result.BrokenLinks = c.findBrokenLinks(ctx, rawURL, body)
		return result, body
	}
}

func (c *crawler) fetch(
	ctx context.Context,
	rawURL string,
	readBody bool,
) (statusCode int, body []byte, err error) {
	reqCtx := ctx
	if c.opts.Timeout > 0 {
		var cancel context.CancelFunc
		reqCtx, cancel = context.WithTimeout(ctx, c.opts.Timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return 0, nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.opts.HTTPClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	if !readBody {
		return resp.StatusCode, nil, nil
	}

	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read body: %w", err)
	}
	return resp.StatusCode, body, nil
}
