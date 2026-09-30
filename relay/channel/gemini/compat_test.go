package gemini

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func TestResolveGeminiOpenAIOptions(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.1-flash-image",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-flash-image",
		},
	}
	request := dto.GeneralOpenAIRequest{
		Model: "gemini-3.1-flash-image",
		ExtraBody: json.RawMessage(`{
			"google": {
				"image_config": {"aspect_ratio": "16:9", "image_size": "2K"},
				"thinking_config": {"thinking_level": "minimal"}
			}
		}`),
	}

	options, err := resolveGeminiOpenAIOptions(request, info)
	require.NoError(t, err)
	require.Equal(t, "16:9", options.AspectRatio)
	require.Equal(t, "2K", options.ImageSize)
	require.Equal(t, "minimal", options.ThinkingLevel)
	require.True(t, options.ThinkingExplicit)

	geminiRequest := &dto.GeminiChatRequest{}
	require.NoError(t, applyGeminiCompatOptions(geminiRequest, options))
	require.JSONEq(t, `{"aspectRatio":"16:9","imageSize":"2K"}`, string(geminiRequest.GenerationConfig.ImageConfig))
	require.JSONEq(t, `{"image":{"aspectRatio":"16:9","imageSize":"2K"}}`, string(geminiRequest.GenerationConfig.ResponseFormat))
	require.Equal(t, "minimal", geminiRequest.GenerationConfig.ThinkingConfig.ThinkingLevel)
}

func TestGeminiFlash31AllowsHalfKImageSize(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.1-flash-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}
	request := dto.GeneralOpenAIRequest{
		Model: "gemini-3.1-flash-image-preview",
		ExtraBody: json.RawMessage(`{
			"google": {
				"image_config": {"image_size": "0.5K"}
			}
		}`),
	}

	options, err := resolveGeminiOpenAIOptions(request, info)
	require.NoError(t, err)
	require.Equal(t, "512", options.ImageSize)

	geminiRequest := &dto.GeminiChatRequest{}
	require.NoError(t, applyGeminiCompatOptions(geminiRequest, options))
	require.JSONEq(t, `{"imageSize":"512"}`, string(geminiRequest.GenerationConfig.ImageConfig))
	require.JSONEq(t, `{"image":{"imageSize":"512"}}`, string(geminiRequest.GenerationConfig.ResponseFormat))
}

func TestGeminiNonFlash31RejectsHalfKImageSize(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3-pro-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}
	request := dto.GeneralOpenAIRequest{
		Model: "gemini-3-pro-image-preview",
		ExtraBody: json.RawMessage(`{
			"google": {
				"image_config": {"image_size": "0.5K"}
			}
		}`),
	}

	_, err := resolveGeminiOpenAIOptions(request, info)
	require.Error(t, err)
	require.Contains(t, err.Error(), "image_size")
}

func TestGeminiReasoningEffortMapping(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.1-flash-image",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-flash-image",
		},
	}

	options, err := resolveGeminiOpenAIOptions(dto.GeneralOpenAIRequest{
		Model:           "gemini-3.1-flash-image",
		ReasoningEffort: "low",
	}, info)
	require.NoError(t, err)
	require.Equal(t, "minimal", options.ThinkingLevel)

	_, err = resolveGeminiOpenAIOptions(dto.GeneralOpenAIRequest{
		Model:           "gemini-3.1-flash-image",
		ReasoningEffort: "medium",
	}, info)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported reasoning_effort")
}

func TestGeminiProImageReasoningEffortLowStaysLow(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3-pro-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}

	options, err := resolveGeminiOpenAIOptions(dto.GeneralOpenAIRequest{
		Model:           "gemini-3-pro-image-preview",
		ReasoningEffort: "low",
	}, info)

	require.NoError(t, err)
	require.Equal(t, "low", options.ThinkingLevel)
}

func TestGeminiReferenceImageLimits(t *testing.T) {
	require.NoError(t, validateGeminiReferenceImageCount("gemini-2.5-flash-image", dto.ChannelOtherSettings{}, 3))
	require.Error(t, validateGeminiReferenceImageCount("gemini-2.5-flash-image", dto.ChannelOtherSettings{}, 4))
	require.NoError(t, validateGeminiReferenceImageCount("gemini-3-pro-image-preview", dto.ChannelOtherSettings{}, 14))
	require.Error(t, validateGeminiReferenceImageCount("gemini-3-pro-image-preview", dto.ChannelOtherSettings{}, 15))
	require.NoError(t, validateGeminiReferenceImageCount("gemini-3.1-flash-lite-image", dto.ChannelOtherSettings{}, 1))
	require.Error(t, validateGeminiReferenceImageCount("gemini-3.1-flash-lite-image", dto.ChannelOtherSettings{}, 2))
	require.NoError(t, validateGeminiReferenceImageCount("gemini-3.1-flash-lite-image", dto.ChannelOtherSettings{
		GeminiReferenceImageMax: 2,
	}, 2))
}

func TestGeminiProPreviewAllowsMultipleInputImages(t *testing.T) {
	require.NoError(t, validateGeminiReferenceImageCount("gemini-3.1-pro-preview", dto.ChannelOtherSettings{}, 2))
	require.NoError(t, validateGeminiReferenceImageCount("gemini-3.1-pro-preview", dto.ChannelOtherSettings{}, 3600))

	err := validateGeminiReferenceImageCount("gemini-3.1-pro-preview", dto.ChannelOtherSettings{}, 3601)
	require.Error(t, err)
	require.Contains(t, err.Error(), "too_many_input_images")
}

func TestGeminiProPreviewRejectsImageOutputConfig(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.1-pro-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-pro-preview",
		},
	}
	request := dto.GeneralOpenAIRequest{
		Model: "gemini-3.1-pro-preview",
		ExtraBody: json.RawMessage(`{
			"google": {
				"image_config": {"aspect_ratio": "16:9", "image_size": "2K"}
			}
		}`),
	}

	_, err := resolveGeminiOpenAIOptions(request, info)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported_image_output")
}

func TestNativeGeminiProPreviewRejectsImageOutputConfig(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.1-pro-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-pro-preview",
		},
	}
	request := &dto.GeminiChatRequest{}
	request.GenerationConfig.ImageConfig = json.RawMessage(`{"imageSize":"2K"}`)

	err := validateNativeGeminiRequest(request, info)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported_image_output")
}
