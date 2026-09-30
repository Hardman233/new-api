package service

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

var geminiMarkdownImagePattern = regexp.MustCompile(`!\[[^\]]*\]\((https?://[^)\s]+|data:image/[^)\s]+)\)`)

// NormalizeGeminiProImageResponse puts the largest generated image first when
// Gemini image models return a low-resolution preview and a final image in the
// same content array. The name is kept for existing call sites.
func NormalizeGeminiProImageResponse(body []byte, models ...string) ([]byte, bool, error) {
	if !isGeminiProImageResponseModel(models...) || len(body) == 0 || !json.Valid(body) {
		return body, false, nil
	}

	var root map[string]any
	if err := common.Unmarshal(body, &root); err != nil {
		return body, false, err
	}

	choices, ok := root["choices"].([]any)
	if !ok {
		return body, false, nil
	}

	changed := false
	for _, rawChoice := range choices {
		choice, ok := rawChoice.(map[string]any)
		if !ok {
			continue
		}
		message, ok := choice["message"].(map[string]any)
		if !ok {
			continue
		}
		switch content := message["content"].(type) {
		case string:
			normalized, itemChanged := normalizeGeminiMarkdownImageStringContent(content)
			if itemChanged {
				message["content"] = normalized
				changed = true
			}
			continue
		case []any:
			normalizedMarkdown, markdownChanged := normalizeGeminiMarkdownImageContent(content)
			if markdownChanged {
				content = normalizedMarkdown
				message["content"] = content
				changed = true
			}
			normalized, itemChanged := normalizeGeminiProImageContentOrder(content)
			if itemChanged {
				message["content"] = normalized
				changed = true
			}
		default:
			continue
		}
	}

	if !changed {
		return body, false, nil
	}
	rewritten, err := common.Marshal(root)
	if err != nil {
		return body, false, err
	}
	return rewritten, true, nil
}

func isGeminiProImageResponseModel(models ...string) bool {
	for _, model := range models {
		model = strings.ToLower(strings.TrimSpace(model))
		model = strings.TrimPrefix(model, "gemini.")
		if strings.Contains(model, "gemini-3-pro-image") ||
			strings.Contains(model, "gemini-3.1-flash-image") ||
			strings.Contains(model, "gemini-3.1-flash-lite-image") ||
			strings.Contains(model, "gemini-2.5-flash-image") ||
			strings.Contains(model, "nano-banana") {
			return true
		}
	}
	return false
}

func normalizeGeminiMarkdownImageContent(content []any) ([]any, bool) {
	changed := false
	normalized := make([]any, 0, len(content))
	for _, rawItem := range content {
		item, ok := rawItem.(map[string]any)
		if !ok {
			normalized = append(normalized, rawItem)
			continue
		}
		itemType, _ := item["type"].(string)
		text, _ := item["text"].(string)
		if !strings.EqualFold(itemType, "text") || text == "" {
			normalized = append(normalized, rawItem)
			continue
		}
		parts, itemChanged := normalizeGeminiMarkdownImageStringContent(text)
		if !itemChanged {
			normalized = append(normalized, rawItem)
			continue
		}
		normalized = append(normalized, parts...)
		changed = true
	}
	return normalized, changed
}

func normalizeGeminiMarkdownImageStringContent(text string) ([]any, bool) {
	matches := geminiMarkdownImagePattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return nil, false
	}
	parts := make([]any, 0, len(matches)*2+1)
	pos := 0
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		parts = appendGeminiTextPart(parts, text[pos:match[0]])
		url := strings.TrimSpace(text[match[2]:match[3]])
		if url != "" {
			parts = append(parts, geminiImageURLPart(url))
		}
		pos = match[1]
	}
	parts = appendGeminiTextPart(parts, text[pos:])
	return parts, len(parts) > 0
}

func appendGeminiTextPart(parts []any, text string) []any {
	if strings.TrimSpace(text) == "" {
		return parts
	}
	return append(parts, map[string]any{
		"type": "text",
		"text": text,
	})
}

func geminiImageURLPart(url string) map[string]any {
	imageURL := map[string]any{
		"url": url,
	}
	if mimeType := mimeTypeFromDataURL(url); mimeType != "" {
		imageURL["mime_type"] = mimeType
	}
	return map[string]any{
		"type":      "image_url",
		"image_url": imageURL,
	}
}

func mimeTypeFromDataURL(url string) string {
	lower := strings.ToLower(url)
	if !strings.HasPrefix(lower, "data:") {
		return ""
	}
	end := strings.Index(url, ";")
	if end <= len("data:") {
		return ""
	}
	return url[len("data:"):end]
}

func normalizeGeminiProImageContentOrder(content []any) ([]any, bool) {
	type imageItem struct {
		contentIndex int
		item         any
		sizeBytes    int64
	}

	images := make([]imageItem, 0)
	for i, rawItem := range content {
		sizeBytes, ok := geminiProImageContentSizeBytes(rawItem)
		if !ok || sizeBytes <= 0 {
			continue
		}
		images = append(images, imageItem{
			contentIndex: i,
			item:         rawItem,
			sizeBytes:    sizeBytes,
		})
	}
	if len(images) < 2 {
		return content, false
	}

	order := make([]int, len(images))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return images[order[i]].sizeBytes > images[order[j]].sizeBytes
	})

	changed := false
	for i, originalIndex := range order {
		if i != originalIndex {
			changed = true
			break
		}
	}
	if !changed {
		return content, false
	}

	normalized := make([]any, len(content))
	copy(normalized, content)
	for targetPos, imageOrderIndex := range order {
		normalized[images[targetPos].contentIndex] = images[imageOrderIndex].item
	}
	return normalized, true
}

func geminiProImageContentSizeBytes(rawItem any) (int64, bool) {
	item, ok := rawItem.(map[string]any)
	if !ok {
		return 0, false
	}
	rawImageURL, ok := item["image_url"]
	if !ok {
		return 0, false
	}

	imageURL, ok := rawImageURL.(map[string]any)
	if !ok {
		return 0, false
	}
	if _, ok = imageURL["url"].(string); !ok {
		return 0, false
	}
	return int64FromAny(imageURL["size_bytes"])
}

func int64FromAny(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case float64:
		return int64(v), true
	case json.Number:
		i, err := v.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}
