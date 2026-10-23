package crawler

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

func (c *crawler) findBrokenLinks(ctx context.Context, pageURL string, body []byte) []brokenLink {
	base, err := url.Parse(pageURL)
	if err != nil {
		return []brokenLink{}
	}

	rawLinks := extractLinks(body)
	broken := make([]brokenLink, 0, len(rawLinks))
	for _, raw := range rawLinks {
		linkURL, ok := resolveLink(base, raw)
		if !ok {
			continue
		}

		statusCode, _, err := c.fetch(ctx, linkURL, false)
		switch {
		case err != nil:
			broken = append(broken, brokenLink{URL: linkURL, Error: err.Error()})
		case statusCode >= http.StatusBadRequest:
			broken = append(broken, brokenLink{URL: linkURL, StatusCode: statusCode})
		}
	}
	return broken
}

func extractLinks(body []byte) []string {
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	links := make([]string, 0)

	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			return links
		}
		if tokenType != html.StartTagToken && tokenType != html.SelfClosingTagToken {
			continue
		}

		tagName, hasAttrs := tokenizer.TagName()
		if !hasAttrs {
			continue
		}

		attrName := linkAttribute(string(tagName))
		if attrName == "" {
			continue
		}

		for {
			key, value, more := tokenizer.TagAttr()
			if string(key) == attrName {
				links = append(links, string(value))
			}
			if !more {
				break
			}
		}
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
	if absolute.Scheme != "http" && absolute.Scheme != "https" {
		return "", false
	}
	if absolute.Host == "" {
		return "", false
	}
	return absolute.String(), true
}
