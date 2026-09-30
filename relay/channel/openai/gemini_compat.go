package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func applyOpenAIChannelGeminiImageCompat(request *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) error {
	if request == nil || !isOpenAIChannelGeminiImageModel(request, info) {
		return nil
	}

	allowHalfK := openAIChannelGeminiAllowsHalfK(request, info)
	imageConfig, hasImageConfig, thinkingConfig, hasThinkingConfig, err := parseOpenAIChannelGeminiExtraBody(request.ExtraBody, allowHalfK)
	if err != nil {
		return err
	}
	if !hasThinkingConfig {
		if mappedThinking := mapOpenAIChannelGeminiReasoningEffort(request, info, request.ReasoningEffort); len(mappedThinking) > 0 {
			thinkingConfig = mappedThinking
			hasThinkingConfig = true
		}
	}
	if isWangsuOpenAIGeminiImageChannel(info) {
		if hasImageConfig && len(request.EcaImageConfig) == 0 {
			ecaImageConfig, err := marshalWangsuEcaImageConfig(imageConfig)
			if err != nil {
				return err
			}
			request.EcaImageConfig = ecaImageConfig
		}
		if (hasImageConfig || len(request.EcaImageConfig) > 0) && len(request.Modalities) == 0 {
			request.Modalities = json.RawMessage(`["text","image"]`)
		}
	}
	if !hasImageConfig && !hasThinkingConfig {
		return nil
	}

	generationConfig := map[string]interface{}{}
	if len(request.GenerationConfig) > 0 {
		if err := common.Unmarshal(request.GenerationConfig, &generationConfig); err != nil {
			return fmt.Errorf("invalid generationConfig: %w", err)
		}
	}

	if hasImageConfig {
		responseFormat := map[string]interface{}{}
		if rawResponseFormat, ok := generationConfig["responseFormat"].(map[string]interface{}); ok {
			responseFormat = rawResponseFormat
		}
		responseFormat["image"] = imageConfig
		generationConfig["responseFormat"] = responseFormat
		generationConfig["imageConfig"] = imageConfig
	}
	if hasThinkingConfig {
		generationConfig["thinkingConfig"] = thinkingConfig
	}

	data, err := common.Marshal(generationConfig)
	if err != nil {
		return fmt.Errorf("marshal generationConfig: %w", err)
	}
	request.GenerationConfig = data
	return nil
}

func isOpenAIChannelGeminiImageModel(request *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) bool {
	models := []string{}
	if request != nil {
		models = append(models, request.Model)
	}
	if info != nil {
		models = append(models, info.UpstreamModelName, info.OriginModelName)
	}
	for _, model := range models {
		if isOpenAIChannelGeminiImageModelName(model) {
			return true
		}
	}
	return false
}

func isOpenAIChannelGeminiImageModelName(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return (strings.HasPrefix(model, "gemini-") || strings.HasPrefix(model, "gemini.gemini-")) &&
		strings.Contains(model, "image")
}

func isWangsuOpenAIGeminiImageChannel(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	baseURL := strings.ToLower(strings.TrimSpace(info.ChannelBaseUrl))
	return strings.Contains(baseURL, "edgecloudapp.com") ||
		strings.Contains(baseURL, "/v2/gws/") ||
		strings.Contains(baseURL, "/v1/gws/")
}

func marshalWangsuEcaImageConfig(imageConfig map[string]interface{}) (json.RawMessage, error) {
	ecaImageConfig := map[string]interface{}{}
	if value, ok := imageConfig["aspectRatio"]; ok {
		ecaImageConfig["aspect_ratio"] = value
	}
	if value, ok := imageConfig["imageSize"]; ok {
		ecaImageConfig["image_size"] = value
	}
	data, err := common.Marshal(ecaImageConfig)
	if err != nil {
		return nil, fmt.Errorf("marshal eca_image_config: %w", err)
	}
	return data, nil
}

func parseOpenAIChannelGeminiExtraBody(raw []byte, allowHalfK bool) (map[string]interface{}, bool, map[string]interface{}, bool, error) {
	if len(raw) == 0 {
		return nil, false, nil, false, nil
	}

	var extraBody map[string]interface{}
	if err := common.Unmarshal(raw, &extraBody); err != nil {
		return nil, false, nil, false, fmt.Errorf("invalid extra_body: %w", err)
	}
	googleValue, ok := extraBody["google"]
	if !ok {
		return nil, false, nil, false, nil
	}
	googleBody, ok := googleValue.(map[string]interface{})
	if !ok {
		return nil, false, nil, false, fmt.Errorf("extra_body.google must be an object")
	}

	imageConfig, hasImageConfig, err := parseOpenAIChannelGeminiImageConfig(googleBody, allowHalfK)
	if err != nil {
		return nil, false, nil, false, err
	}
	thinkingConfig, hasThinkingConfig, err := parseOpenAIChannelGeminiThinkingConfig(googleBody)
	if err != nil {
		return nil, false, nil, false, err
	}
	return imageConfig, hasImageConfig, thinkingConfig, hasThinkingConfig, nil
}

func parseOpenAIChannelGeminiImageConfig(googleBody map[string]interface{}, allowHalfK bool) (map[string]interface{}, bool, error) {
	imageValue, exists := googleBody["image_config"]
	if !exists {
		return nil, false, nil
	}
	imageBody, ok := imageValue.(map[string]interface{})
	if !ok {
		return nil, false, fmt.Errorf("extra_body.google.image_config must be an object")
	}

	imageConfig := map[string]interface{}{}
	if value, exists := imageBody["aspect_ratio"]; exists {
		aspectRatio, ok := value.(string)
		if !ok || strings.TrimSpace(aspectRatio) == "" {
			return nil, false, fmt.Errorf("extra_body.google.image_config.aspect_ratio must be a non-empty string")
		}
		imageConfig["aspectRatio"] = strings.TrimSpace(aspectRatio)
	}
	if value, exists := imageBody["image_size"]; exists {
		imageSize, ok := value.(string)
		if !ok || !isOpenAIChannelGeminiImageSize(imageSize, allowHalfK) {
			return nil, false, openAIChannelGeminiImageSizeError(allowHalfK)
		}
		imageConfig["imageSize"] = normalizeOpenAIChannelGeminiImageSize(imageSize)
	}
	if len(imageConfig) == 0 {
		return nil, false, nil
	}
	return imageConfig, true, nil
}

func parseOpenAIChannelGeminiThinkingConfig(googleBody map[string]interface{}) (map[string]interface{}, bool, error) {
	thinkingValue, exists := googleBody["thinking_config"]
	if !exists {
		return nil, false, nil
	}
	thinkingBody, ok := thinkingValue.(map[string]interface{})
	if !ok {
		return nil, false, fmt.Errorf("extra_body.google.thinking_config must be an object")
	}

	thinkingConfig := map[string]interface{}{}
	if value, exists := thinkingBody["thinking_level"]; exists {
		thinkingLevel, ok := value.(string)
		if !ok || strings.TrimSpace(thinkingLevel) == "" {
			return nil, false, fmt.Errorf("extra_body.google.thinking_config.thinking_level must be a non-empty string")
		}
		thinkingConfig["thinkingLevel"] = strings.TrimSpace(thinkingLevel)
	}
	if value, exists := thinkingBody["thinking_budget"]; exists {
		thinkingConfig["thinkingBudget"] = value
	}
	if value, exists := thinkingBody["include_thoughts"]; exists {
		includeThoughts, ok := value.(bool)
		if !ok {
			return nil, false, fmt.Errorf("extra_body.google.thinking_config.include_thoughts must be a boolean")
		}
		thinkingConfig["includeThoughts"] = includeThoughts
	}
	if len(thinkingConfig) == 0 {
		return nil, false, nil
	}
	return thinkingConfig, true, nil
}

func isOpenAIChannelGeminiImageSize(value string, allowHalfK bool) bool {
	switch normalizeOpenAIChannelGeminiImageSize(value) {
	case "512":
		return allowHalfK
	case "1K", "2K", "4K":
		return true
	default:
		return false
	}
}

func normalizeOpenAIChannelGeminiImageSize(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	switch value {
	case "0.5K", "512PX":
		return "512"
	default:
		return value
	}
}

func openAIChannelGeminiImageSizeError(allowHalfK bool) error {
	if allowHalfK {
		return fmt.Errorf("extra_body.google.image_config.image_size must be one of 0.5K, 1K, 2K, or 4K")
	}
	return fmt.Errorf("extra_body.google.image_config.image_size must be one of 1K, 2K, or 4K")
}

func openAIChannelGeminiAllowsHalfK(request *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) bool {
	models := []string{}
	if request != nil {
		models = append(models, request.Model)
	}
	if info != nil {
		models = append(models, info.UpstreamModelName, info.OriginModelName)
	}
	for _, model := range models {
		if isOpenAIChannelGeminiFlash31ImageModel(model) {
			return true
		}
	}
	return false
}

func isOpenAIChannelGeminiFlash31ImageModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	model = strings.TrimPrefix(model, "gemini.")
	return model == "gemini-3.1-flash-image" || model == "gemini-3.1-flash-image-preview"
}

func isOpenAIChannelGeminiProImageModel(request *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo) bool {
	models := []string{}
	if request != nil {
		models = append(models, request.Model)
	}
	if info != nil {
		models = append(models, info.UpstreamModelName, info.OriginModelName)
	}
	for _, model := range models {
		model = strings.ToLower(strings.TrimSpace(model))
		model = strings.TrimPrefix(model, "gemini.")
		if model == "gemini-3-pro-image" || model == "gemini-3-pro-image-preview" {
			return true
		}
	}
	return false
}

func mapOpenAIChannelGeminiReasoningEffort(request *dto.GeneralOpenAIRequest, info *relaycommon.RelayInfo, effort string) map[string]interface{} {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low":
		if isOpenAIChannelGeminiProImageModel(request, info) {
			return map[string]interface{}{"thinkingLevel": "low"}
		}
		return map[string]interface{}{"thinkingLevel": "minimal"}
	case "minimal":
		return map[string]interface{}{"thinkingLevel": "minimal"}
	case "high":
		return map[string]interface{}{"thinkingLevel": "high"}
	default:
		return nil
	}
}
