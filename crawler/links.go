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

func (c *crawler) findBrokenLinks(ctx context.Context, pageURL string, body []byte) []brokenLink {
	base, err := url.Parse(pageURL)
	if err != nil {
		return []brokenLink{}
	}

	rawLinks := extractLinks(body)
	broken := make([]brokenLink, 0, len(rawLinks))
	for _, link := range rawLinks {
		if link.Kind != linkNavigation {
			continue
		}

		linkURL, ok := resolveLink(base, link.URL)
		if !ok {
			continue
		}

		result := c.fetch(ctx, linkURL, fetchStatusOnly)
		switch {
		case result.Err != nil:
			broken = append(broken, brokenLink{URL: linkURL, Error: result.Err.Error()})
		case result.StatusCode >= http.StatusBadRequest:
			broken = append(broken, brokenLink{
				URL:        linkURL,
				StatusCode: result.StatusCode,
				Error:      statusErrorText(result.StatusCode),
			})
		}
	}
	return broken
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
	if absolute.Scheme != schemeHTTP && absolute.Scheme != schemeHTTPS {
		return "", false
	}
	if absolute.Host == "" {
		return "", false
	}
	return absolute.String(), true
}

func normalizeURLForKey(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.Fragment = ""
	if u.Path != "/" && strings.HasSuffix(u.Path, "/") {
		u.Path = strings.TrimSuffix(u.Path, "/")
		if u.Path == "" {
			u.Path = "/"
		}
	}
	return u.String()
}
