package openai

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestConvertOpenAIRequestAddsGeminiGenerationConfig(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:           "gemini-3-pro-image-preview",
		ReasoningEffort: "high",
		ExtraBody: json.RawMessage(`{
			"google": {
				"image_config": {
					"aspect_ratio": "1:1",
					"image_size": "4K"
				}
			}
		}`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3-pro-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}

	gotAny, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	got := gotAny.(*dto.GeneralOpenAIRequest)
	if len(got.ExtraBody) == 0 {
		t.Fatalf("extra_body should be preserved")
	}

	var generationConfig map[string]interface{}
	if err := json.Unmarshal(got.GenerationConfig, &generationConfig); err != nil {
		t.Fatalf("unmarshal generationConfig: %v", err)
	}

	imageConfig := generationConfig["imageConfig"].(map[string]interface{})
	if imageConfig["aspectRatio"] != "1:1" || imageConfig["imageSize"] != "4K" {
		t.Fatalf("imageConfig = %#v", imageConfig)
	}
	responseFormat := generationConfig["responseFormat"].(map[string]interface{})
	responseImage := responseFormat["image"].(map[string]interface{})
	if responseImage["aspectRatio"] != "1:1" || responseImage["imageSize"] != "4K" {
		t.Fatalf("responseFormat.image = %#v", responseImage)
	}
	thinkingConfig := generationConfig["thinkingConfig"].(map[string]interface{})
	if thinkingConfig["thinkingLevel"] != "high" {
		t.Fatalf("thinkingConfig = %#v", thinkingConfig)
	}
}

func TestConvertOpenAIRequestKeepsGeminiProImageLowReasoning(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:           "gemini-3-pro-image-preview",
		ReasoningEffort: "low",
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3-pro-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}

	gotAny, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	got := gotAny.(*dto.GeneralOpenAIRequest)

	var generationConfig map[string]interface{}
	if err := json.Unmarshal(got.GenerationConfig, &generationConfig); err != nil {
		t.Fatalf("unmarshal generationConfig: %v", err)
	}
	thinkingConfig := generationConfig["thinkingConfig"].(map[string]interface{})
	if thinkingConfig["thinkingLevel"] != "low" {
		t.Fatalf("thinkingConfig = %#v", thinkingConfig)
	}
}

func TestConvertOpenAIRequestRejectsInvalidGeminiImageSize(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "gemini-3-pro-image-preview",
		ExtraBody: json.RawMessage(`{
			"google": {
				"image_config": {
					"image_size": "8K"
				}
			}
		}`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3-pro-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}

	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err == nil {
		t.Fatalf("expected invalid image_size error")
	}
}

func TestConvertOpenAIRequestAllowsGeminiFlash31HalfK(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "gemini-3.1-flash-image-preview",
		ExtraBody: json.RawMessage(`{
			"google": {
				"image_config": {
					"image_size": "0.5K"
				}
			}
		}`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.1-flash-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}

	gotAny, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	got := gotAny.(*dto.GeneralOpenAIRequest)

	var generationConfig map[string]interface{}
	if err := json.Unmarshal(got.GenerationConfig, &generationConfig); err != nil {
		t.Fatalf("unmarshal generationConfig: %v", err)
	}
	imageConfig := generationConfig["imageConfig"].(map[string]interface{})
	if imageConfig["imageSize"] != "512" {
		t.Fatalf("imageConfig = %#v", imageConfig)
	}
}

func TestConvertOpenAIRequestMapsGeminiFlash31HalfKTo512ForWangsu(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "gemini-3.1-flash-image-preview",
		ExtraBody: json.RawMessage(`{
			"google": {
				"image_config": {
					"image_size": "0.5K",
					"aspect_ratio": "1:1"
				}
			}
		}`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.1-flash-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    "https://aigateway.edgecloudapp.com/v2/gws/test-gateway",
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}

	gotAny, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	got := gotAny.(*dto.GeneralOpenAIRequest)

	var ecaImageConfig map[string]interface{}
	if err := json.Unmarshal(got.EcaImageConfig, &ecaImageConfig); err != nil {
		t.Fatalf("unmarshal eca_image_config: %v", err)
	}
	if ecaImageConfig["image_size"] != "512" || ecaImageConfig["aspect_ratio"] != "1:1" {
		t.Fatalf("eca_image_config = %#v", ecaImageConfig)
	}
}

func TestConvertOpenAIRequestRejectsGeminiProHalfK(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "gemini-3-pro-image-preview",
		ExtraBody: json.RawMessage(`{
			"google": {
				"image_config": {
					"image_size": "0.5K"
				}
			}
		}`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3-pro-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}

	_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err == nil {
		t.Fatalf("expected invalid image_size error")
	}
}

func TestConvertOpenAIRequestAddsWangsuGeminiImageConfig(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "gemini.gemini-3-pro-image-preview",
		ExtraBody: json.RawMessage(`{
			"google": {
				"image_config": {
					"aspect_ratio": "16:9",
					"image_size": "2K"
				}
			}
		}`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini.gemini-3-pro-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    "https://aigateway.edgecloudapp.com/v2/gws/test-gateway",
			UpstreamModelName: "gemini.gemini-3-pro-image-preview",
		},
	}

	gotAny, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	got := gotAny.(*dto.GeneralOpenAIRequest)

	var ecaImageConfig map[string]interface{}
	if err := json.Unmarshal(got.EcaImageConfig, &ecaImageConfig); err != nil {
		t.Fatalf("unmarshal eca_image_config: %v", err)
	}
	if ecaImageConfig["aspect_ratio"] != "16:9" || ecaImageConfig["image_size"] != "2K" {
		t.Fatalf("eca_image_config = %#v", ecaImageConfig)
	}
	var modalities []string
	if err := json.Unmarshal(got.Modalities, &modalities); err != nil {
		t.Fatalf("unmarshal modalities: %v", err)
	}
	if len(modalities) != 2 || modalities[0] != "text" || modalities[1] != "image" {
		t.Fatalf("modalities = %#v", modalities)
	}
}

func TestConvertOpenAIRequestPreservesExistingWangsuEcaImageConfig(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model:          "gemini-3-pro-image-preview",
		EcaImageConfig: json.RawMessage(`{"aspect_ratio":"1:1","image_size":"4K"}`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3-pro-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    "https://aigateway.edgecloudapp.com/v2/gws/test-gateway",
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}

	gotAny, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertOpenAIRequest returned error: %v", err)
	}
	got := gotAny.(*dto.GeneralOpenAIRequest)
	if string(got.EcaImageConfig) != `{"aspect_ratio":"1:1","image_size":"4K"}` {
		t.Fatalf("eca_image_config = %s", string(got.EcaImageConfig))
	}
	if len(got.Modalities) == 0 {
		t.Fatalf("modalities should be filled for Wangsu Gemini image request")
	}
}
