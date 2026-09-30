package model_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/setting/config"
)

// GeminiSettings defines Gemini model configuration. 注意bool要以enabled结尾才可以生效编辑
type GeminiSettings struct {
	SafetySettings                        map[string]string   `json:"safety_settings"`
	VersionSettings                       map[string]string   `json:"version_settings"`
	SupportedImagineModels                []string            `json:"supported_imagine_models"`
	ModelAliases                          map[string]string   `json:"model_aliases"`
	ImageReferenceLimits                  map[string]int      `json:"image_reference_limits"`
	ImageThinkingLevels                   map[string][]string `json:"image_thinking_levels"`
	ReferenceImageMaxFileSizeMB           int                 `json:"reference_image_max_file_size_mb"`
	ThinkingAdapterEnabled                bool                `json:"thinking_adapter_enabled"`
	ThinkingAdapterBudgetTokensPercentage float64             `json:"thinking_adapter_budget_tokens_percentage"`
	FunctionCallThoughtSignatureEnabled   bool                `json:"function_call_thought_signature_enabled"`
	RemoveFunctionResponseIdEnabled       bool                `json:"remove_function_response_id_enabled"`
}

// 默认配置
var defaultGeminiSettings = GeminiSettings{
	SafetySettings: map[string]string{
		"default": "OFF",
	},
	VersionSettings: map[string]string{
		"default":        "v1beta",
		"gemini-1.0-pro": "v1",
	},
	SupportedImagineModels: []string{
		"gemini-2.0-flash-exp-image-generation",
		"gemini-2.0-flash-exp",
		"gemini-3-pro-image",
		"gemini-3-pro-image-preview",
		"gemini-2.5-flash-image",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-image-preview",
		"gemini-3.1-flash-lite-image",
		"gemini-3.1-flash-lite-image-preview",
	},
	ModelAliases: map[string]string{},
	ImageReferenceLimits: map[string]int{
		"gemini-2.5-flash-image":      3,
		"gemini-3-pro-image":          14,
		"gemini-3.1-flash-image":      14,
		"gemini-3.1-flash-lite-image": 1,
	},
	ImageThinkingLevels: map[string][]string{
		"gemini-3-pro-image":          {"low", "high"},
		"gemini-3.1-flash-image":      {"minimal", "high"},
		"gemini-3.1-flash-lite-image": {"minimal", "high"},
	},
	ReferenceImageMaxFileSizeMB:           64,
	ThinkingAdapterEnabled:                false,
	ThinkingAdapterBudgetTokensPercentage: 0.6,
	FunctionCallThoughtSignatureEnabled:   true,
	RemoveFunctionResponseIdEnabled:       true,
}

// 全局实例
var geminiSettings = defaultGeminiSettings

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("gemini", &geminiSettings)
}

// GetGeminiSettings 获取Gemini配置
func GetGeminiSettings() *GeminiSettings {
	return &geminiSettings
}

// GetGeminiSafetySetting 获取安全设置
func GetGeminiSafetySetting(key string) string {
	if value, ok := geminiSettings.SafetySettings[key]; ok {
		return value
	}
	return geminiSettings.SafetySettings["default"]
}

// GetGeminiVersionSetting 获取版本设置
func GetGeminiVersionSetting(key string) string {
	if value, ok := geminiSettings.VersionSettings[key]; ok {
		return value
	}
	return geminiSettings.VersionSettings["default"]
}

// ResolveGeminiModelAlias resolves explicitly configured stable/preview aliases.
// It intentionally does not fall back to a different model when an alias is missing.
func ResolveGeminiModelAlias(model string) string {
	current := model
	visited := map[string]bool{}
	for i := 0; i < 8; i++ {
		mapped, ok := geminiSettings.ModelAliases[current]
		if !ok || mapped == "" {
			return current
		}
		if visited[current] || visited[mapped] {
			return current
		}
		visited[current] = true
		current = mapped
	}
	return current
}

func geminiImageCapabilityKey(model string) string {
	model = strings.TrimSpace(model)
	switch model {
	case "gemini-3-pro-image", "gemini-3-pro-image-preview":
		return "gemini-3-pro-image"
	case "gemini-3.1-flash-image", "gemini-3.1-flash-image-preview":
		return "gemini-3.1-flash-image"
	case "gemini-3.1-flash-lite-image", "gemini-3.1-flash-lite-image-preview":
		return "gemini-3.1-flash-lite-image"
	case "gemini-2.5-flash-image":
		return model
	default:
		return model
	}
}

func GetGeminiImageReferenceLimit(model string) int {
	key := geminiImageCapabilityKey(model)
	if limit, ok := geminiSettings.ImageReferenceLimits[key]; ok && limit > 0 {
		return limit
	}
	return 1
}

func GetGeminiImageThinkingLevels(model string) []string {
	key := geminiImageCapabilityKey(model)
	return append([]string(nil), geminiSettings.ImageThinkingLevels[key]...)
}

func GetGeminiReferenceImageMaxFileSizeMB() int {
	if geminiSettings.ReferenceImageMaxFileSizeMB > 0 {
		return geminiSettings.ReferenceImageMaxFileSizeMB
	}
	return 64
}

func IsGeminiModelSupportImagine(model string) bool {
	model = ResolveGeminiModelAlias(model)
	for _, v := range geminiSettings.SupportedImagineModels {
		if ResolveGeminiModelAlias(v) == model {
			return true
		}
	}
	return false
}
