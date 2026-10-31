package crawler

import (
	"bytes"
	"context"
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
	linkOther
)

type extractedLink struct {
	URL  string
	Kind linkKind
}

func (c *crawler) findBrokenLinks(ctx context.Context, pageURL string, depth int, body []byte) []BrokenLink {
	base, err := url.Parse(pageURL)
	if err != nil {
		return []BrokenLink{}
	}

	root, err := url.Parse(c.opts.URL)
	if err != nil {
		return []BrokenLink{}
	}

	rawLinks := extractLinks(body)
	broken := make([]BrokenLink, 0, len(rawLinks))
	canSchedule := depth+1 < c.opts.Depth
	for _, link := range rawLinks {
		if link.Kind != linkNavigation {
			continue
		}

		linkURL, ok := resolveLink(base, link.URL)
		if !ok {
			continue
		}
		linkParsed, err := url.Parse(linkURL)
		if err != nil {
			continue
		}
		if canonicalURL(linkParsed) == canonicalURL(base) {
			continue
		}

		internal := linkParsed.Host == root.Host
		if internal && canSchedule {
			continue
		}

		item, isBroken := c.checkLink(ctx, linkURL)
		if isBroken {
			broken = append(broken, item)
		}
	}
	return broken
}

func (c *crawler) checkLink(ctx context.Context, linkURL string) (BrokenLink, bool) {
	result := c.fetch(ctx, http.MethodHead, linkURL, fetchStatusOnly)
	if result.Err != nil {
		return BrokenLink{URL: linkURL, Error: result.Err.Error()}, true
	}

	if result.StatusCode == http.StatusMethodNotAllowed {
		result = c.fetch(ctx, http.MethodGet, linkURL, fetchStatusOnly)
		if result.Err != nil {
			return BrokenLink{URL: linkURL, Error: result.Err.Error()}, true
		}
	}

	if result.StatusCode >= http.StatusBadRequest {
		return BrokenLink{
			URL:        linkURL,
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
		tag := string(tagName)
		attrName := linkAttribute(tag)
		if attrName == "" {
			continue
		}

		var rawURL, rel string
		for {
			key, value, more := tokenizer.TagAttr()
			switch string(key) {
			case attrName:
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
		kind, ok := linkKindFor(tag, rel)
		if !ok {
			continue
		}
		links = append(links, extractedLink{URL: rawURL, Kind: kind})
	}
}

func linkAttribute(tagName string) string {
	switch tagName {
	case "a", "link":
		return "href"
	case "script", "img":
		return "src"
	default:
		return ""
	}
}

func linkKindFor(tagName, rel string) (linkKind, bool) {
	switch tagName {
	case "a":
		return linkNavigation, true
	case "img":
		return linkImage, true
	case "script":
		return linkScript, true
	case "link":
		if hasRelToken(rel, "stylesheet") {
			return linkStyle, true
		}
		return linkOther, true
	default:
		return linkOther, false
	}
}

func hasRelToken(rel, token string) bool {
	return slices.Contains(strings.Fields(strings.ToLower(rel)), token)
}

func resolveLink(base *url.URL, raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return "", false
	}

	ref, err := url.Parse(raw)
	if err != nil {
		return "", false
	}

	absolute := base.ResolveReference(ref)
	absolute.Fragment = ""
	absolute.RawFragment = ""
	if absolute.Scheme != schemeHTTP && absolute.Scheme != schemeHTTPS {
		return "", false
	}
	if absolute.Host == "" {
		return "", false
	}
	return absolute.String(), true
}

func canonicalURL(u *url.URL) string {
	canonical := *u
	canonical.Fragment = ""
	canonical.RawFragment = ""
	if canonical.Path == "" {
		canonical.Path = "/"
	} else if canonical.Path != "/" {
		canonical.Path = strings.TrimSuffix(canonical.Path, "/")
	}
	return canonical.String()
}

func normalizeURLForKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return canonicalURL(u)
}
