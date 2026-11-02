package crawler

import (
	"context"
	"net/http"
	"net/url"
)

const (
	assetTypeImage  = "image"
	assetTypeScript = "script"
	assetTypeStyle  = "style"
)

type assetMeta struct {
	StatusCode int
	SizeBytes  int64
	Error      string
}

func (c *crawler) collectAssets(ctx context.Context, pageURL string, body []byte) []Asset {
	base, err := url.Parse(pageURL)
	if err != nil {
		return []Asset{}
	}

	rawLinks := extractLinks(body)
	seen := make(map[string]struct{}, len(rawLinks))
	assets := make([]Asset, 0, len(rawLinks))

	for _, kind := range []linkKind{linkImage, linkScript, linkStyle} {
		for _, link := range rawLinks {
			if link.Kind != kind {
				continue
			}
			if ctx.Err() != nil {
				return assets
			}

			typ := assetType(kind)
			assetURL, ok := resolveLink(base, link.URL)
			if !ok {
				continue
			}

			key := typ + "::" + assetURL
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}

			meta, exists := c.assetCache[assetURL]
			if !exists {
				meta = c.fetchAsset(ctx, assetURL)
				if ctx.Err() != nil {
					return assets
				}
				c.assetCache[assetURL] = meta
			}

			assets = append(assets, Asset{
				URL:        assetURL,
				Type:       typ,
				SizeBytes:  meta.SizeBytes,
				StatusCode: meta.StatusCode,
				Error:      meta.Error,
			})
		}
	}
	return assets
}

func (c *crawler) fetchAsset(ctx context.Context, assetURL string) assetMeta {
	var meta assetMeta

	result := c.fetch(ctx, http.MethodHead, assetURL, fetchAssetMeta)
	if result.Err != nil {
		meta.Error = result.Err.Error()
		return meta
	}

	meta.StatusCode = result.StatusCode
	if result.StatusCode == http.StatusMethodNotAllowed ||
		(result.StatusCode < http.StatusBadRequest && result.Size < 0) {
		result = c.fetch(ctx, http.MethodGet, assetURL, fetchAssetMeta)
		if result.Err != nil {
			meta.Error = result.Err.Error()
			return meta
		}
		meta.StatusCode = result.StatusCode
	}

	switch {
	case result.StatusCode >= http.StatusBadRequest:
		meta.Error = statusErrorText(result.StatusCode)
	case result.Size < 0:
		meta.Error = "unknown content length"
	default:
		meta.SizeBytes = result.Size
	}
	return meta
}

func assetType(kind linkKind) string {
	switch kind {
	case linkImage:
		return assetTypeImage
	case linkScript:
		return assetTypeScript
	case linkStyle:
		return assetTypeStyle
	case linkNavigation:
		return ""
	default:
		return ""
	}
}
