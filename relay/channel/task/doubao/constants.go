package doubao

import "strings"

var ModelList = []string{
	"doubao-seedance-1-0-pro-250528",
	"doubao-seedance-1-0-lite-t2v",
	"doubao-seedance-1-0-lite-i2v",
	"doubao-seedance-1-5-pro-251215",
	"doubao-seedance-2-0-260128",
	"doubao-seedance-2-0-fast-260128",
	"doubao-seedance-2-0-mini",
	"doubao-seedance-2-5-260628",
}

var ChannelName = "doubao-video"

const (
	seedance20Model     = "doubao-seedance-2-0-260128"
	seedance20FastModel = "doubao-seedance-2-0-fast-260128"
	seedance25Model     = "doubao-seedance-2-5-260628"
)

func IsSeedance25Model(modelName string) bool {
	return strings.EqualFold(strings.TrimSpace(modelName), seedance25Model)
}

// GetVideoInputRatio keeps the legacy helper for callers that only care about
// video-input discounts. Full Seedance 2.0 billing should use
// GetSeedanceBillingRatio so 1080p can be priced exactly.
func GetVideoInputRatio(modelName string) (float64, bool) {
	switch modelName {
	case seedance20Model:
		return 28.0 / 46.0, true
	case seedance20FastModel:
		return 22.0 / 37.0, true
	case seedance25Model:
		return 42.0 / 70.0, true
	default:
		return 1, false
	}
}

// GetSeedanceBillingRatio returns the single final multiplier relative to the
// admin-configured base price:
//   - Seedance 2.0 base: 46 / 1M tokens for 480p/720p without video input
//   - Seedance 2.0 Fast base: 37 / 1M tokens without video input
//
// Use one combined multiplier instead of multiplying independent "video" and
// "1080p" ratios, because 1080p+video is officially 31/1M, not
// 28/46 * 51/46 * 46 = 31.04.
func GetSeedanceBillingRatio(modelName string, resolution string, hasVideoInput bool) (string, float64, bool) {
	switch modelName {
	case seedance20Model:
		const base = 46.0
		if isSeedance1080p(resolution) {
			if hasVideoInput {
				return "seedance_1080p_video_input", 31.0 / base, true
			}
			return "seedance_1080p", 51.0 / base, true
		}
		if hasVideoInput {
			return "video_input", 28.0 / base, true
		}
	case seedance20FastModel:
		if hasVideoInput {
			return "video_input", 22.0 / 37.0, true
		}
	case seedance25Model:
		// Seedance 2.5 is priced at 70 CNY/M completion tokens without
		// input video and 42 CNY/M when any input video is present.
		if hasVideoInput {
			return "seedance_2_5_video_input", 42.0 / 70.0, true
		}
		return "seedance_2_5_base", 1, true
	}
	return "", 1, false
}

func isSeedance1080p(resolution string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(resolution), " ", ""))
	return strings.Contains(normalized, "1080")
}

// SeedanceAdvancedMetadataAllowlist Seedance 2.0 高级参数白名单：
// 仅这些 metadata key 允许进入上游请求体，其余字段丢弃，避免危险或拼写错误字段透传。
var SeedanceAdvancedMetadataAllowlist = map[string]struct{}{
	"content":                 {},
	"callback_url":            {},
	"return_last_frame":       {},
	"service_tier":            {},
	"execution_expires_after": {},
	"generate_audio":          {},
	"draft":                   {},
	"tools":                   {},
	"resolution":              {},
	"ratio":                   {},
	"duration":                {},
	"frames":                  {},
	"seed":                    {},
	"camera_fixed":            {},
	"watermark":               {},
	"extra_body":              {},
}
