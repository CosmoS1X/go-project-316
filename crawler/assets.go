package crawler

import (
	"context"
	"net/http"
	"net/url"
)

func (c *crawler) collectAssets(ctx context.Context, pageURL string, body []byte) []asset {
	base, err := url.Parse(pageURL)
	if err != nil {
		return []asset{}
	}

	rawLinks := extractLinks(body)
	assets := make([]asset, 0, len(rawLinks))
	seen := make(map[string]struct{}, len(rawLinks))

	for _, link := range rawLinks {
		if link.Kind == linkNavigation {
			continue
		}
		if ctx.Err() != nil {
			break
		}

		assetURL, ok := resolveLink(base, link.URL)
		if !ok {
			continue
		}

		key := normalizeAssetURL(assetURL)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}

		item, exists := c.assetCache[key]
		if !exists {
			item = c.fetchAsset(ctx, key, assetType(link.Kind))
			c.assetCache[key] = item
		}
		assets = append(assets, item)
	}
	return assets
}

func (c *crawler) fetchAsset(ctx context.Context, assetURL, typ string) asset {
	result := c.fetch(ctx, assetURL, fetchAssetMeta)
	item := asset{
		URL:        assetURL,
		Type:       typ,
		StatusCode: result.StatusCode,
	}

	switch {
	case result.Err != nil:
		item.Error = result.Err.Error()
	case result.StatusCode >= http.StatusBadRequest:
		item.SizeBytes = result.Size
		item.Error = statusErrorText(result.StatusCode)
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
		return assetTypeOther
	default:
		return assetTypeOther
	}
}

func normalizeAssetURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.Fragment = ""
	return u.String()
}
