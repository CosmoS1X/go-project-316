package crawler

import (
	"cmp"
	"context"
	"net/http"
	"net/url"
	"slices"
	"time"

	"golang.org/x/time/rate"
)

type crawler struct {
	opts       Options
	root       *url.URL
	limiter    *rate.Limiter
	assetCache map[string]assetMeta
}

func newCrawler(opts Options) (*crawler, error) {
	root, err := opts.validate()
	if err != nil {
		return nil, err
	}

	var limiter *rate.Limiter
	if opts.RPS > 0 {
		// RPS has priority over Delay
		limiter = rate.NewLimiter(rate.Limit(opts.RPS), 1)
	} else if opts.Delay > 0 {
		limiter = rate.NewLimiter(rate.Every(opts.Delay), 1)
	}

	return &crawler{
		opts:       opts,
		root:       root,
		limiter:    limiter,
		assetCache: make(map[string]assetMeta),
	}, nil
}

func (c *crawler) Run(ctx context.Context) *Report {
	generatedAt := time.Now().UTC().Truncate(time.Second)

	visited := make(map[string]struct{})
	pages := make([]Page, 0)

	type queueItem struct {
		url   *url.URL
		depth int
	}

	queue := make([]queueItem, 0)
	visited[canonicalURL(c.root)] = struct{}{}
	queue = append(queue, queueItem{url: c.root, depth: 0})

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

		if current.depth+1 >= c.opts.Depth {
			continue
		}

		if len(body) == 0 {
			continue
		}

		for _, link := range extractLinks(body) {
			if link.Kind != linkNavigation {
				continue
			}
			if ctx.Err() != nil {
				break
			}
			linkURL, ok := resolveLink(current.url, link.URL)
			if !ok {
				continue
			}
			if c.root.Host != "" && normalizedHost(linkURL) != normalizedHost(c.root) {
				continue
			}
			if linkURL.Scheme != schemeHTTP && linkURL.Scheme != schemeHTTPS {
				continue
			}

			key := canonicalURL(linkURL)
			if _, exists := visited[key]; exists {
				continue
			}
			visited[key] = struct{}{}
			queue = append(queue, queueItem{url: linkURL, depth: current.depth + 1})
		}
	}

	slices.SortFunc(pages, func(a, b Page) int {
		if compared := cmp.Compare(a.Depth, b.Depth); compared != 0 {
			return compared
		}
		return cmp.Compare(a.URL, b.URL)
	})

	return &Report{
		RootURL:     c.opts.URL,
		Depth:       c.opts.Depth,
		GeneratedAt: generatedAt,
		Pages:       pages,
	}
}

func (c *crawler) visit(ctx context.Context, pageURL *url.URL, depth int) (p Page, body []byte) {
	discoveredAt := time.Now().UTC().Truncate(time.Second)
	rawURL := pageURL.String()
	result := c.fetch(ctx, http.MethodGet, rawURL, fetchFullBody)

	p = Page{
		URL:          rawURL,
		Depth:        depth,
		HTTPStatus:   result.StatusCode,
		Status:       statusOK,
		DiscoveredAt: discoveredAt,
	}
	if result.Err != nil {
		p.Status = statusError
		p.Error = result.Err.Error()
		return p, nil
	}

	if result.StatusCode >= http.StatusBadRequest {
		p.Status = statusError
		p.Error = statusErrorText(result.StatusCode)
	}

	p.SEO = extractSEO(result.Body)
	p.Assets = c.collectAssets(ctx, pageURL, result.Body)
	p.BrokenLinks = c.findBrokenLinks(ctx, pageURL, depth, result.Body)
	return p, result.Body
}
