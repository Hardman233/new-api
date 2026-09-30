package gemini

import (
	"encoding/base64"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
)

// GeminiCompatOptions is the internal representation shared by OpenAI and
// native Gemini request conversion.
type GeminiCompatOptions struct {
	AspectRatio         string
	ImageSize           string
	ThinkingLevel       string
	ThinkingBudget      *int
	IncludeThoughts     *bool
	ReferenceImageMax   int
	MaxFileSize         int64
	ThinkingExplicit    bool
	ImageConfigExplicit bool
}

func newGeminiCompatOptions(model string, channelSettings dto.ChannelOtherSettings) GeminiCompatOptions {
	model = geminiCapabilityModel(model)
	maxImages := model_setting.GetGeminiImageReferenceLimit(model)
	if channelSettings.GeminiReferenceImageMax > 0 {
		maxImages = channelSettings.GeminiReferenceImageMax
	}

	maxFileSizeMB := model_setting.GetGeminiReferenceImageMaxFileSizeMB()
	if channelSettings.GeminiReferenceImageMaxFileSizeMB > 0 {
		maxFileSizeMB = channelSettings.GeminiReferenceImageMaxFileSizeMB
	}

	return GeminiCompatOptions{
		ReferenceImageMax: maxImages,
		MaxFileSize:       int64(maxFileSizeMB) * 1024 * 1024,
	}
}

func geminiCapabilityModel(model string) string {
	model = strings.TrimSpace(model)
	if strings.Contains(model, "-thinking-") {
		model = strings.SplitN(model, "-thinking-", 2)[0]
	}
	for _, suffix := range []string{"-thinking", "-nothinking"} {
		model = strings.TrimSuffix(model, suffix)
	}
	if baseModel, _, ok := reasoning.TrimEffortSuffix(model); ok {
		model = baseModel
	}
	return model_setting.ResolveGeminiModelAlias(model)
}

func geminiSupportsImageOutput(model string) bool {
	return model_setting.IsGeminiModelSupportImagine(geminiCapabilityModel(model))
}

func geminiInputImageLimit(model string, channelSettings dto.ChannelOtherSettings) (int, string) {
	if geminiSupportsImageOutput(model) {
		return newGeminiCompatOptions(model, channelSettings).ReferenceImageMax, "reference"
	}
	if channelSettings.GeminiReferenceImageMax > 0 {
		return channelSettings.GeminiReferenceImageMax, "input"
	}
	return 3600, "input"
}

func validateGeminiImageOutputModel(model string) error {
	if geminiSupportsImageOutput(model) {
		return nil
	}
	return fmt.Errorf("unsupported_image_output: model %s supports image input with text output, but does not support image generation; use a Gemini image model such as gemini-3-pro-image-preview", geminiCapabilityModel(model))
}

func parseGeminiGoogleExtraBody(raw []byte) (GeminiCompatOptions, bool, bool, error) {
	var extraBody map[string]interface{}
	if len(raw) == 0 {
		return GeminiCompatOptions{}, false, false, nil
	}
	if err := common.Unmarshal(raw, &extraBody); err != nil {
		return GeminiCompatOptions{}, false, false, fmt.Errorf("invalid extra body: %w", err)
	}

	googleValue, exists := extraBody["google"]
	if !exists {
		return GeminiCompatOptions{}, false, false, nil
	}
	googleBody, ok := googleValue.(map[string]interface{})
	if !ok {
		return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google must be an object")
	}

	if _, exists := googleBody["thinkingConfig"]; exists {
		return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.thinkingConfig is not supported, use extra_body.google.thinking_config instead")
	}
	if _, exists := googleBody["imageConfig"]; exists {
		return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.imageConfig is not supported, use extra_body.google.image_config instead")
	}

	options := GeminiCompatOptions{}
	hasThinking := false
	hasImage := false

	if imageValue, exists := googleBody["image_config"]; exists {
		imageConfig, ok := imageValue.(map[string]interface{})
		if !ok {
			return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.image_config must be an object")
		}
		if _, exists := imageConfig["aspectRatio"]; exists {
			return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.image_config.aspectRatio is not supported, use extra_body.google.image_config.aspect_ratio instead")
		}
		if _, exists := imageConfig["imageSize"]; exists {
			return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.image_config.imageSize is not supported, use extra_body.google.image_config.image_size instead")
		}
		if value, exists := imageConfig["aspect_ratio"]; exists {
			aspectRatio, ok := value.(string)
			if !ok || strings.TrimSpace(aspectRatio) == "" {
				return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.image_config.aspect_ratio must be a non-empty string")
			}
			options.AspectRatio = strings.TrimSpace(aspectRatio)
			hasImage = true
		}
		if value, exists := imageConfig["image_size"]; exists {
			imageSize, ok := value.(string)
			if !ok || strings.TrimSpace(imageSize) == "" {
				return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.image_config.image_size must be a non-empty string")
			}
			options.ImageSize = normalizeGeminiImageSize(imageSize)
			hasImage = true
		}
	}

	if thinkingValue, exists := googleBody["thinking_config"]; exists {
		thinkingConfig, ok := thinkingValue.(map[string]interface{})
		if !ok {
			return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.thinking_config must be an object")
		}
		if _, exists := thinkingConfig["thinkingBudget"]; exists {
			return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.thinking_config.thinkingBudget is not supported, use extra_body.google.thinking_config.thinking_budget instead")
		}
		if _, exists := thinkingConfig["thinkingLevel"]; exists {
			return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.thinking_config.thinkingLevel is not supported, use extra_body.google.thinking_config.thinking_level instead")
		}
		if _, exists := thinkingConfig["includeThoughts"]; exists {
			return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.thinking_config.includeThoughts is not supported, use extra_body.google.thinking_config.include_thoughts instead")
		}

		if value, exists := thinkingConfig["thinking_budget"]; exists {
			budget, err := parseGeminiInteger(value)
			if err != nil {
				return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.thinking_config.thinking_budget must be an integer")
			}
			options.ThinkingBudget = common.GetPointer(budget)
			hasThinking = true
		}
		if value, exists := thinkingConfig["thinking_level"]; exists {
			level, ok := value.(string)
			if !ok || strings.TrimSpace(level) == "" {
				return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.thinking_config.thinking_level must be a string")
			}
			options.ThinkingLevel = strings.ToLower(strings.TrimSpace(level))
			hasThinking = true
		}
		if options.ThinkingBudget != nil && options.ThinkingLevel != "" {
			return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.thinking_config cannot set thinking_budget and thinking_level together")
		}
		if value, exists := thinkingConfig["include_thoughts"]; exists {
			includeThoughts, ok := value.(bool)
			if !ok {
				return GeminiCompatOptions{}, false, false, fmt.Errorf("extra_body.google.thinking_config.include_thoughts must be a boolean")
			}
			options.IncludeThoughts = common.GetPointer(includeThoughts)
			hasThinking = true
		}
	}

	options.ThinkingExplicit = hasThinking
	options.ImageConfigExplicit = hasImage
	return options, hasThinking, hasImage, nil
}

func parseGeminiInteger(value interface{}) (int, error) {
	number, ok := value.(float64)
	if !ok || math.Trunc(number) != number {
		return 0, fmt.Errorf("not an integer")
	}
	return int(number), nil
}

func isGeminiImageSize(value string) bool {
	switch normalizeGeminiImageSize(value) {
	case "512", "1K", "2K", "4K":
		return true
	default:
		return false
	}
}

func geminiImageSizeSupportedByModel(model, imageSize string) bool {
	imageSize = normalizeGeminiImageSize(imageSize)
	if imageSize == "" {
		return true
	}
	if imageSize == "512" {
		return isGeminiFlash31ImageModel(model)
	}
	return isGeminiImageSize(imageSize)
}

func normalizeGeminiImageSize(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	switch value {
	case "0.5K", "512PX":
		return "512"
	default:
		return value
	}
}

func isGeminiFlash31ImageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(geminiCapabilityModel(model)))
	return model == "gemini-3.1-flash-image" || model == "gemini-3.1-flash-image-preview"
}

func geminiImageSizeError(model string) error {
	if isGeminiFlash31ImageModel(model) {
		return fmt.Errorf("image_size must be one of 0.5K, 1K, 2K, or 4K for gemini-3.1-flash-image")
	}
	return fmt.Errorf("image_size must be one of 1K, 2K, or 4K")
}

func mapGeminiReasoningEffort(model, effort string) (GeminiCompatOptions, error) {
	effort = strings.ToLower(strings.TrimSpace(effort))
	if effort == "" {
		return GeminiCompatOptions{}, nil
	}
	if effort == "none" {
		includeThoughts := false
		budget := 0
		return GeminiCompatOptions{ThinkingBudget: &budget, IncludeThoughts: &includeThoughts, ThinkingExplicit: true}, nil
	}

	model = geminiCapabilityModel(model)
	if strings.HasPrefix(model, "gemini-3") {
		level := effort
		if effort != "minimal" && effort != "low" && effort != "medium" && effort != "high" {
			return GeminiCompatOptions{}, fmt.Errorf("unsupported reasoning_effort=%s", effort)
		}
		levels := model_setting.GetGeminiImageThinkingLevels(model)
		if len(levels) == 0 {
			levels = []string{"minimal", "low", "medium", "high"}
		}
		if effort == "low" && !containsString(levels, "low") && containsString(levels, "minimal") {
			level = "minimal"
		}
		if !containsString(levels, level) {
			return GeminiCompatOptions{}, fmt.Errorf("unsupported reasoning_effort: the selected Gemini model does not support reasoning_effort=%s", effort)
		}
		includeThoughts := true
		return GeminiCompatOptions{ThinkingLevel: level, IncludeThoughts: &includeThoughts, ThinkingExplicit: true}, nil
	}

	budget := clampThinkingBudgetByEffort(model, effort)
	includeThoughts := true
	return GeminiCompatOptions{ThinkingBudget: &budget, IncludeThoughts: &includeThoughts, ThinkingExplicit: true}, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func mergeGeminiThinkingOptions(base, explicit GeminiCompatOptions) (GeminiCompatOptions, error) {
	if base.ThinkingLevel != "" && explicit.ThinkingLevel != "" && !strings.EqualFold(base.ThinkingLevel, explicit.ThinkingLevel) {
		return GeminiCompatOptions{}, fmt.Errorf("conflicting Gemini thinking parameters")
	}
	if base.IncludeThoughts != nil && explicit.IncludeThoughts != nil && *base.IncludeThoughts != *explicit.IncludeThoughts {
		return GeminiCompatOptions{}, fmt.Errorf("conflicting Gemini thinking parameters")
	}
	if base.ThinkingBudget != nil && explicit.ThinkingBudget != nil && *base.ThinkingBudget != *explicit.ThinkingBudget {
		return GeminiCompatOptions{}, fmt.Errorf("conflicting Gemini thinking parameters")
	}
	if base.ThinkingLevel != "" && explicit.ThinkingBudget != nil || base.ThinkingBudget != nil && explicit.ThinkingLevel != "" {
		return GeminiCompatOptions{}, fmt.Errorf("conflicting Gemini thinking parameters")
	}
	return base, nil
}

func resolveGeminiOpenAIOptions(request dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) (GeminiCompatOptions, error) {
	if request.AspectRatio != "" {
		return GeminiCompatOptions{}, fmt.Errorf("Use extra_body.google.image_config.aspect_ratio")
	}
	if request.ImageSize != "" {
		return GeminiCompatOptions{}, fmt.Errorf("Use extra_body.google.image_config.image_size")
	}

	var channelSettings dto.ChannelOtherSettings
	model := request.Model
	if info != nil {
		model = info.UpstreamModelName
		if info.ChannelMeta != nil {
			channelSettings = info.ChannelOtherSettings
		}
	}
	options := newGeminiCompatOptions(model, channelSettings)
	extraOptions, hasExtraThinking, hasExtraImage, err := parseGeminiGoogleExtraBody(request.ExtraBody)
	if err != nil {
		return GeminiCompatOptions{}, err
	}
	if hasExtraImage {
		if err := validateGeminiImageOutputModel(model); err != nil {
			return GeminiCompatOptions{}, err
		}
		if !geminiImageSizeSupportedByModel(model, extraOptions.ImageSize) {
			return GeminiCompatOptions{}, geminiImageSizeError(model)
		}
		options.AspectRatio = extraOptions.AspectRatio
		options.ImageSize = extraOptions.ImageSize
		options.ImageConfigExplicit = true
	}

	if request.ReasoningEffort != "" {
		reasoningOptions, mapErr := mapGeminiReasoningEffort(model, request.ReasoningEffort)
		if mapErr != nil {
			return GeminiCompatOptions{}, mapErr
		}
		if hasExtraThinking {
			if _, mergeErr := mergeGeminiThinkingOptions(extraOptions, reasoningOptions); mergeErr != nil {
				return GeminiCompatOptions{}, mergeErr
			}
		} else {
			options.ThinkingLevel = reasoningOptions.ThinkingLevel
			options.ThinkingBudget = reasoningOptions.ThinkingBudget
			options.IncludeThoughts = reasoningOptions.IncludeThoughts
			options.ThinkingExplicit = true
		}
	}
	if hasExtraThinking {
		options.ThinkingLevel = extraOptions.ThinkingLevel
		options.ThinkingBudget = extraOptions.ThinkingBudget
		options.IncludeThoughts = extraOptions.IncludeThoughts
		options.ThinkingExplicit = true
	}

	if options.ThinkingLevel != "" {
		levels := model_setting.GetGeminiImageThinkingLevels(model)
		if len(levels) > 0 && !containsString(levels, options.ThinkingLevel) {
			return GeminiCompatOptions{}, fmt.Errorf("unsupported_thinking_level: the selected Gemini model does not support thinking_level=%s", options.ThinkingLevel)
		}
	}
	if options.ThinkingBudget != nil && strings.HasPrefix(geminiCapabilityModel(model), "gemini-3") && len(model_setting.GetGeminiImageThinkingLevels(model)) > 0 {
		return GeminiCompatOptions{}, fmt.Errorf("unsupported_thinking_config: Gemini 3 image models require thinking_level")
	}
	return options, nil
}

func applyGeminiCompatOptions(request *dto.GeminiChatRequest, options GeminiCompatOptions) error {
	if options.ImageConfigExplicit || options.AspectRatio != "" || options.ImageSize != "" {
		imageConfig := map[string]string{}
		if options.AspectRatio != "" {
			imageConfig["aspectRatio"] = options.AspectRatio
		}
		if options.ImageSize != "" {
			imageConfig["imageSize"] = options.ImageSize
		}
		data, err := common.Marshal(imageConfig)
		if err != nil {
			return fmt.Errorf("marshal Gemini image config: %w", err)
		}
		request.GenerationConfig.ImageConfig = data

		responseFormat, err := common.Marshal(map[string]interface{}{
			"image": imageConfig,
		})
		if err != nil {
			return fmt.Errorf("marshal Gemini response format: %w", err)
		}
		request.GenerationConfig.ResponseFormat = responseFormat
	}
	if options.ThinkingExplicit {
		request.GenerationConfig.ThinkingConfig = &dto.GeminiThinkingConfig{
			ThinkingLevel:   options.ThinkingLevel,
			ThinkingBudget:  options.ThinkingBudget,
			IncludeThoughts: false,
		}
		if options.IncludeThoughts != nil {
			request.GenerationConfig.ThinkingConfig.IncludeThoughts = *options.IncludeThoughts
		}
	}
	return nil
}

func validateGeminiImageConfig(model string, config map[string]interface{}) error {
	if len(config) > 0 {
		if err := validateGeminiImageOutputModel(model); err != nil {
			return err
		}
	}
	if size, ok := config["imageSize"].(string); ok && !geminiImageSizeSupportedByModel(model, size) {
		return geminiImageSizeError(model)
	}
	if level, ok := config["thinkingLevel"].(string); ok {
		levels := model_setting.GetGeminiImageThinkingLevels(model)
		if len(levels) > 0 && !containsString(levels, level) {
			return fmt.Errorf("unsupported_thinking_level: the selected Gemini model does not support thinkingLevel=%s", level)
		}
	}
	return nil
}

func validateGeminiReferenceImageCount(model string, channelSettings dto.ChannelOtherSettings, imageCount int) error {
	limit, imageKind := geminiInputImageLimit(model, channelSettings)
	if imageCount > limit {
		return fmt.Errorf("too_many_%s_images: the selected model supports at most %d %s images", imageKind, limit, imageKind)
	}
	return nil
}

func validateGeminiReferenceImageSize(options GeminiCompatOptions, size int64) error {
	if options.MaxFileSize > 0 && size > options.MaxFileSize {
		return fmt.Errorf("reference image exceeds maximum size of %d MB", options.MaxFileSize/(1024*1024))
	}
	return nil
}

func validateNativeGeminiRequest(request *dto.GeminiChatRequest, info *relaycommon.RelayInfo) error {
	if request == nil || info == nil {
		return nil
	}
	options := newGeminiCompatOptions(info.UpstreamModelName, info.ChannelOtherSettings)
	imageCount := 0
	for _, content := range request.Contents {
		for _, part := range content.Parts {
			if part.InlineData != nil && strings.HasPrefix(strings.ToLower(part.InlineData.MimeType), "image/") {
				imageCount++
				if err := validateGeminiReferenceImageSize(options, int64(base64.StdEncoding.DecodedLen(len(part.InlineData.Data)))); err != nil {
					return err
				}
			}
			if part.FileData != nil && strings.HasPrefix(strings.ToLower(part.FileData.MimeType), "image/") {
				imageCount++
			}
		}
	}
	if err := validateGeminiReferenceImageCount(info.UpstreamModelName, info.ChannelOtherSettings, imageCount); err != nil {
		return err
	}

	if request.GenerationConfig.ThinkingConfig != nil {
		thinking := request.GenerationConfig.ThinkingConfig
		if thinking.ThinkingLevel != "" {
			levels := model_setting.GetGeminiImageThinkingLevels(info.UpstreamModelName)
			if len(levels) > 0 && !containsString(levels, thinking.ThinkingLevel) {
				return fmt.Errorf("unsupported_thinking_level: the selected Gemini model does not support thinkingLevel=%s", thinking.ThinkingLevel)
			}
		}
		if thinking.ThinkingBudget != nil && strings.HasPrefix(geminiCapabilityModel(info.UpstreamModelName), "gemini-3") && len(model_setting.GetGeminiImageThinkingLevels(info.UpstreamModelName)) > 0 {
			return fmt.Errorf("unsupported_thinking_config: Gemini 3 image models require thinkingLevel")
		}
	}
	if len(request.GenerationConfig.ImageConfig) > 0 {
		var imageConfig map[string]interface{}
		if err := common.Unmarshal(request.GenerationConfig.ImageConfig, &imageConfig); err != nil {
			return fmt.Errorf("invalid Gemini imageConfig: %w", err)
		}
		if err := validateGeminiImageConfig(info.UpstreamModelName, imageConfig); err != nil {
			return err
		}
	}
	if len(request.GenerationConfig.ResponseFormat) > 0 {
		var responseFormat map[string]interface{}
		if err := common.Unmarshal(request.GenerationConfig.ResponseFormat, &responseFormat); err != nil {
			return fmt.Errorf("invalid Gemini responseFormat: %w", err)
		}
		if imageConfig, ok := responseFormat["image"].(map[string]interface{}); ok {
			if err := validateGeminiImageConfig(info.UpstreamModelName, imageConfig); err != nil {
				return err
			}
		}
	}
	return nil
}
