package crawler

import (
	"context"
	"net/http"
	"net/url"
)

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

			item, exists := c.assetCache[assetURL]
			if !exists {
				item = c.fetchAsset(ctx, assetURL, typ)
				c.assetCache[assetURL] = item
			}
			assets = append(assets, item)
		}
	}
	return assets
}

func (c *crawler) fetchAsset(ctx context.Context, assetURL, typ string) Asset {
	item := Asset{
		URL:  assetURL,
		Type: typ,
	}

	result := c.fetch(ctx, http.MethodHead, assetURL, fetchAssetMeta)
	if result.Err != nil {
		item.Error = result.Err.Error()
		return item
	}

	item.StatusCode = result.StatusCode
	if result.StatusCode == http.StatusMethodNotAllowed ||
		(result.StatusCode < http.StatusBadRequest && result.Size < 0) {
		result = c.fetch(ctx, http.MethodGet, assetURL, fetchAssetMeta)
		if result.Err != nil {
			item.Error = result.Err.Error()
			return item
		}
		item.StatusCode = result.StatusCode
	}

	switch {
	case result.StatusCode >= http.StatusBadRequest:
		item.Error = statusErrorText(result.StatusCode)
	case result.Size < 0:
		item.Error = "unknown content length"
	default:
		item.SizeBytes = result.Size
	}
	return item
}

func assetType(kind linkKind) string {
	switch kind {
	case linkImage:
		return assetTypeImage
	case linkScript:
		return assetTypeScript
	case linkStyle:
		return assetTypeStyle
	case linkNavigation, linkOther:
		return ""
	default:
		return ""
	}
}
