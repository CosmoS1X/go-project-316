package crawler

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"golang.org/x/net/html"
)

const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

type linkKind int

const (
	linkNavigation linkKind = iota
	linkImage
	linkScript
	linkStyle
)

type linkSpec struct {
	attr string
	kind linkKind
}

var linkSpecs = map[string]linkSpec{
	"a":      {attr: "href", kind: linkNavigation},
	"img":    {attr: "src", kind: linkImage},
	"script": {attr: "src", kind: linkScript},
	"link":   {attr: "href", kind: linkStyle},
}

type extractedLink struct {
	URL  string
	Kind linkKind
}

func (c *crawler) findBrokenLinks(
	ctx context.Context,
	pageURL *url.URL,
	depth int,
	rawLinks []extractedLink,
) []BrokenLink {
	broken := make([]BrokenLink, 0, len(rawLinks))
	seen := make(map[string]struct{}, len(rawLinks))
	canSchedule := depth+1 < c.opts.Depth
	for _, link := range rawLinks {
		if ctx.Err() != nil {
			return broken
		}

		linkURL, ok := c.resolveNavigationLink(pageURL, link)
		if !ok {
			continue
		}
		if canonicalURL(linkURL) == canonicalURL(pageURL) {
			continue
		}
		linkKey := linkURL.String()
		if _, exists := seen[linkKey]; exists {
			continue
		}
		seen[linkKey] = struct{}{}

		if c.isInternal(linkURL) && canSchedule {
			continue
		}

		item, isBroken := c.probeLink(ctx, linkURL)
		if ctx.Err() != nil {
			return broken
		}
		if isBroken {
			broken = append(broken, item)
		}
	}
	return broken
}

func (c *crawler) probeLink(ctx context.Context, linkURL *url.URL) (BrokenLink, bool) {
	rawURL := linkURL.String()
	result := c.fetchWithGETFallback(ctx, rawURL, fetchStatusOnly, func(result fetchResult) bool {
		return result.StatusCode == http.StatusMethodNotAllowed
	})
	if result.Err != nil {
		return BrokenLink{URL: rawURL, Error: result.Err.Error()}, true
	}

	if result.StatusCode >= http.StatusBadRequest {
		return BrokenLink{
			URL:        rawURL,
			StatusCode: result.StatusCode,
			Error:      statusErrorText(result.StatusCode),
		}, true
	}
	return BrokenLink{}, false
}

func extractLinks(body []byte) []extractedLink {
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	links := make([]extractedLink, 0)

	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			return links
		}
		if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken {
			continue
		}

		tagName, _ := tokenizer.TagName()
		spec, ok := linkSpecs[string(tagName)]
		if !ok {
			continue
		}

		var rawURL, rel string
		for {
			key, value, more := tokenizer.TagAttr()
			switch string(key) {
			case spec.attr:
				rawURL = string(value)
			case "rel":
				rel = string(value)
			}
			if !more {
				break
			}
		}

		if rawURL == "" {
			continue
		}
		if spec.kind == linkStyle && !hasRelToken(rel, "stylesheet") {
			continue
		}
		links = append(links, extractedLink{URL: rawURL, Kind: spec.kind})
	}
}

func hasRelToken(rel, token string) bool {
	return slices.Contains(strings.Fields(strings.ToLower(rel)), token)
}

func resolveLink(base *url.URL, raw string) (*url.URL, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return nil, false
	}

	ref, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}

	absolute := base.ResolveReference(ref)
	absolute.Fragment = ""
	absolute.RawFragment = ""
	if absolute.Scheme != schemeHTTP && absolute.Scheme != schemeHTTPS {
		return nil, false
	}
	if absolute.Host == "" {
		return nil, false
	}
	return absolute, true
}

func canonicalURL(u *url.URL) string {
	canonical := *u
	canonical.Fragment = ""
	canonical.RawFragment = ""
	canonical.Host = normalizedHost(u)
	if canonical.Path == "" {
		canonical.Path = "/"
	} else if canonical.Path != "/" {
		canonical.Path = strings.TrimSuffix(canonical.Path, "/")
	}
	return canonical.String()
}

func normalizedHost(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "" ||
		(u.Scheme == schemeHTTP && port == "80") ||
		(u.Scheme == schemeHTTPS && port == "443") {
		if strings.Contains(host, ":") {
			return "[" + host + "]"
		}
		return host
	}
	return net.JoinHostPort(host, port)
}
