package crawler

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

func extractSEO(body []byte) SEO {
	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	var (
		result       SEO
		titleSeen    bool
		titleDepth   int
		titleBuilder strings.Builder
	)

	for {
		tokenType := tokenizer.Next()
		if tokenType == html.ErrorToken {
			result.Title = cleanText(titleBuilder.String())
			result.Description = cleanText(result.Description)
			return result
		}

		if tokenType == html.StartTagToken || tokenType == html.SelfClosingTagToken {
			tagName, _ := tokenizer.TagName()
			switch string(tagName) {
			case "title":
				if titleSeen {
					continue
				}
				titleSeen = true
				result.HasTitle = true
				if tokenType == html.StartTagToken {
					titleDepth++
				}
			case "h1":
				result.HasH1 = true
			case "meta":
				if hasDescription, description := metaDescription(tokenizer); hasDescription && !result.HasDescription {
					result.HasDescription = true
					result.Description = description
				}
			}
			continue
		}

		if tokenType == html.TextToken {
			if titleDepth > 0 {
				titleBuilder.Write(tokenizer.Text())
			}
			continue
		}

		if tokenType == html.EndTagToken {
			tagName, _ := tokenizer.TagName()
			if string(tagName) == "title" && titleDepth > 0 {
				titleDepth--
			}
		}
	}
}

func metaDescription(tokenizer *html.Tokenizer) (hasDescription bool, description string) {
	var name, content string
	for {
		key, value, more := tokenizer.TagAttr()
		switch string(key) {
		case "name":
			name = string(value)
		case "content":
			content = string(value)
		}
		if !more {
			break
		}
	}
	return strings.EqualFold(name, "description"), content
}

func cleanText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
