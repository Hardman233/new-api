package gemini

import (
	"bytes"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiConvertImageRequestMapsOpenAISizeToSupportedImagenAspectRatio(t *testing.T) {
	t.Parallel()

	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "imagen-4.0-generate-preview-06-06",
		},
	}
	cases := []struct {
		name string
		size string
		want string
	}{
		{name: "square", size: "1024x1024", want: "1:1"},
		{name: "landscape", size: "1536x1024", want: "4:3"},
		{name: "portrait", size: "1024x1536", want: "3:4"},
		{name: "wide", size: "1792x1024", want: "16:9"},
		{name: "tall", size: "1024x1792", want: "9:16"},
		{name: "explicit aspect ratio", size: "16:9", want: "16:9"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got, err := (&Adaptor{}).ConvertImageRequest(nil, info, dto.ImageRequest{
				Prompt: "test",
				Size:   tt.size,
			})

			require.NoError(t, err)
			req, ok := got.(dto.GeminiImageRequest)
			require.True(t, ok)
			require.Equal(t, tt.want, req.Parameters.AspectRatio)
		})
	}
}

func TestGeminiImageModelForcesNonStreamRequest(t *testing.T) {
	t.Parallel()

	info := &relaycommon.RelayInfo{
		IsStream: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://generativelanguage.googleapis.com",
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}

	url, err := (&Adaptor{}).GetRequestURL(info)

	require.NoError(t, err)
	require.False(t, info.IsStream)
	require.Contains(t, url, ":generateContent")
	require.NotContains(t, url, "streamGenerateContent")
}

func TestGeminiTextModelKeepsStreamRequest(t *testing.T) {
	t.Parallel()

	info := &relaycommon.RelayInfo{
		IsStream: true,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://generativelanguage.googleapis.com",
			UpstreamModelName: "gemini-3.1-pro-preview",
		},
	}

	url, err := (&Adaptor{}).GetRequestURL(info)

	require.NoError(t, err)
	require.True(t, info.IsStream)
	require.Contains(t, url, ":streamGenerateContent?alt=sse")
}

func TestBuildUsageFromGeminiMetadataFillsUnreportedTextTokens(t *testing.T) {
	t.Parallel()

	usage := buildUsageFromGeminiMetadata(dto.GeminiUsageMetadata{
		PromptTokenCount:     22,
		CandidatesTokenCount: 1303,
		TotalTokenCount:      1325,
		CandidatesTokensDetails: []dto.GeminiPromptTokensDetails{
			{Modality: "IMAGE", TokenCount: 1290},
		},
	}, 0)

	require.Equal(t, 22, usage.PromptTokens)
	require.Equal(t, 1303, usage.CompletionTokens)
	require.Equal(t, 1325, usage.TotalTokens)
	require.Equal(t, 1290, usage.CompletionTokenDetails.ImageTokens)
	require.Equal(t, 13, usage.CompletionTokenDetails.TextTokens)
	require.Equal(t, 0, usage.CompletionTokenDetails.ReasoningTokens)
}

func TestResponseGeminiChat2OpenAIStructuresInlineImages(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	imageData := base64.StdEncoding.EncodeToString([]byte("png-bytes"))
	payload := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{Text: "好的，这是图片："},
						{InlineData: &dto.GeminiInlineData{
							MimeType: "image/png",
							Data:     imageData,
						}},
					},
				},
			},
		},
	}

	got := responseGeminiChat2OpenAI(c, &payload)

	require.Len(t, got.Choices, 1)
	content, ok := got.Choices[0].Message.Content.([]dto.MediaContent)
	require.True(t, ok)
	require.Len(t, content, 2)
	require.Equal(t, dto.ContentTypeText, content[0].Type)
	require.Equal(t, "好的，这是图片：", content[0].Text)
	require.Equal(t, dto.ContentTypeImageURL, content[1].Type)
	imageURL, ok := content[1].ImageUrl.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "data:image/png;base64,"+imageData, imageURL["url"])
	require.Equal(t, "image/png", imageURL["mime_type"])
}

func TestConvertOpenAI2GeminiMapsNToCandidateCountForImageModel(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	n := 3
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.1-flash-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeGemini,
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}
	request := dto.GeneralOpenAIRequest{
		Model: "gemini-3.1-flash-image-preview",
		N:     &n,
		Messages: []dto.Message{
			{Role: "user", Content: "generate product images"},
		},
	}

	got, err := CovertOpenAI2Gemini(c, request, info)
	require.NoError(t, err)
	require.NotNil(t, got.GenerationConfig.CandidateCount)
	require.Equal(t, 3, *got.GenerationConfig.CandidateCount)
	require.Equal(t, []string{"TEXT", "IMAGE"}, got.GenerationConfig.ResponseModalities)
}

func TestConvertOpenAI2GeminiRejectsInvalidImageCandidateCount(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	n := 5
	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.1-flash-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeGemini,
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}
	request := dto.GeneralOpenAIRequest{
		Model: "gemini-3.1-flash-image-preview",
		N:     &n,
		Messages: []dto.Message{
			{Role: "user", Content: "generate product images"},
		},
	}

	_, err := CovertOpenAI2Gemini(c, request, info)
	require.Error(t, err)
	require.Contains(t, err.Error(), "n must be between 1 and 4")
}

func TestResponseGeminiChat2OpenAIPreservesMultipleImageCandidates(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	firstImage := base64.StdEncoding.EncodeToString([]byte("first-png"))
	secondImage := base64.StdEncoding.EncodeToString([]byte("second-png"))
	payload := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Index: 0,
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: firstImage}},
					},
				},
			},
			{
				Index: 1,
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{InlineData: &dto.GeminiInlineData{MimeType: "image/png", Data: secondImage}},
					},
				},
			},
		},
	}

	got := responseGeminiChat2OpenAI(c, &payload)

	require.Len(t, got.Choices, 2)
	require.Equal(t, 0, got.Choices[0].Index)
	require.Equal(t, 1, got.Choices[1].Index)

	firstContent, ok := got.Choices[0].Message.Content.([]dto.MediaContent)
	require.True(t, ok)
	secondContent, ok := got.Choices[1].Message.Content.([]dto.MediaContent)
	require.True(t, ok)
	require.Len(t, firstContent, 1)
	require.Len(t, secondContent, 1)

	firstURL, ok := firstContent[0].ImageUrl.(map[string]any)
	require.True(t, ok)
	secondURL, ok := secondContent[0].ImageUrl.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "data:image/png;base64,"+firstImage, firstURL["url"])
	require.Equal(t, "data:image/png;base64,"+secondImage, secondURL["url"])
}

func TestResponseGeminiChat2OpenAIStructuresFileDataImages(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	payload := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{FileData: &dto.GeminiFileData{
							MimeType: "image/jpeg",
							FileUri:  "https://upstream.example/image.jpg",
						}},
					},
				},
			},
		},
	}

	got := responseGeminiChat2OpenAI(c, &payload)

	require.Len(t, got.Choices, 1)
	content, ok := got.Choices[0].Message.Content.([]dto.MediaContent)
	require.True(t, ok)
	require.Len(t, content, 1)
	require.Equal(t, dto.ContentTypeImageURL, content[0].Type)
	imageURL, ok := content[0].ImageUrl.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://upstream.example/image.jpg", imageURL["url"])
	require.Equal(t, "image/jpeg", imageURL["mime_type"])
}

func TestResponseGeminiChat2OpenAISkipsThoughtImages(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	payload := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{
							Thought: true,
							Text:    "checking preview",
						},
						{
							Thought: true,
							FileData: &dto.GeminiFileData{
								MimeType: "image/jpeg",
								FileUri:  "https://upstream.example/preview.jpg",
							},
						},
						{
							FileData: &dto.GeminiFileData{
								MimeType: "image/jpeg",
								FileUri:  "https://upstream.example/final.jpg",
							},
						},
					},
				},
			},
		},
	}

	got := responseGeminiChat2OpenAI(c, &payload)

	require.Len(t, got.Choices, 1)
	require.Equal(t, "checking preview", got.Choices[0].Message.GetReasoningContent())
	content, ok := got.Choices[0].Message.Content.([]dto.MediaContent)
	require.True(t, ok)
	require.Len(t, content, 1)
	imageURL, ok := content[0].ImageUrl.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "https://upstream.example/final.jpg", imageURL["url"])
}

func TestGeminiChatHandlerCompletionTokensExcludeToolUsePromptTokens(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatGemini,
		OriginModelName: "gemini-3-flash-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3-flash-preview",
		},
	}

	payload := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{Text: "ok"},
					},
				},
			},
		},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:        151,
			ToolUsePromptTokenCount: 18329,
			CandidatesTokenCount:    1089,
			ThoughtsTokenCount:      1120,
			TotalTokenCount:         20689,
		},
	}

	body, err := common.Marshal(payload)
	require.NoError(t, err)

	resp := &http.Response{
		Body: io.NopCloser(bytes.NewReader(body)),
	}

	usage, newAPIError := GeminiChatHandler(c, info, resp)
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Equal(t, 18480, usage.PromptTokens)
	require.Equal(t, 2209, usage.CompletionTokens)
	require.Equal(t, 20689, usage.TotalTokens)
	require.Equal(t, 1120, usage.CompletionTokenDetails.ReasoningTokens)
}

func TestGeminiStreamHandlerCompletionTokensExcludeToolUsePromptTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	t.Cleanup(func() {
		constant.StreamingTimeout = oldStreamingTimeout
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3-flash-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3-flash-preview",
		},
	}

	chunk := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{Text: "partial"},
					},
				},
			},
		},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:        151,
			ToolUsePromptTokenCount: 18329,
			CandidatesTokenCount:    1089,
			ThoughtsTokenCount:      1120,
			TotalTokenCount:         20689,
		},
	}

	chunkData, err := common.Marshal(chunk)
	require.NoError(t, err)

	streamBody := []byte("data: " + string(chunkData) + "\n" + "data: [DONE]\n")
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewReader(streamBody)),
	}

	usage, newAPIError := geminiStreamHandler(c, info, resp, func(_ string, _ *dto.GeminiChatResponse) bool {
		return true
	})
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Equal(t, 18480, usage.PromptTokens)
	require.Equal(t, 2209, usage.CompletionTokens)
	require.Equal(t, 20689, usage.TotalTokens)
	require.Equal(t, 1120, usage.CompletionTokenDetails.ReasoningTokens)
}

func TestGeminiStreamHandlerSkipsThoughtImagesForImageFallbackTokens(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	t.Cleanup(func() {
		constant.StreamingTimeout = oldStreamingTimeout
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3.1-flash-image-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3.1-flash-image-preview",
		},
	}

	chunk := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{
							Thought: true,
							InlineData: &dto.GeminiInlineData{
								MimeType: "image/png",
								Data:     base64.StdEncoding.EncodeToString([]byte("preview")),
							},
						},
						{
							InlineData: &dto.GeminiInlineData{
								MimeType: "image/png",
								Data:     base64.StdEncoding.EncodeToString([]byte("final")),
							},
						},
					},
				},
			},
		},
	}

	chunkData, err := common.Marshal(chunk)
	require.NoError(t, err)

	streamBody := []byte("data: " + string(chunkData) + "\n" + "data: [DONE]\n")
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewReader(streamBody)),
	}

	usage, newAPIError := geminiStreamHandler(c, info, resp, func(_ string, _ *dto.GeminiChatResponse) bool {
		return true
	})
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Equal(t, 1400, usage.CompletionTokens)
}

func TestGeminiTextGenerationHandlerPromptTokensIncludeToolUsePromptTokens(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3-flash-preview:generateContent", nil)

	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3-flash-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3-flash-preview",
		},
	}

	payload := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{Text: "ok"},
					},
				},
			},
		},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:        151,
			ToolUsePromptTokenCount: 18329,
			CandidatesTokenCount:    1089,
			ThoughtsTokenCount:      1120,
			TotalTokenCount:         20689,
		},
	}

	body, err := common.Marshal(payload)
	require.NoError(t, err)

	resp := &http.Response{
		Body: io.NopCloser(bytes.NewReader(body)),
	}

	usage, newAPIError := GeminiTextGenerationHandler(c, info, resp)
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Equal(t, 18480, usage.PromptTokens)
	require.Equal(t, 2209, usage.CompletionTokens)
	require.Equal(t, 20689, usage.TotalTokens)
	require.Equal(t, 1120, usage.CompletionTokenDetails.ReasoningTokens)
}

func TestGeminiChatHandlerUsesEstimatedPromptTokensWhenUsagePromptMissing(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	info := &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatGemini,
		OriginModelName: "gemini-3-flash-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3-flash-preview",
		},
	}
	info.SetEstimatePromptTokens(20)

	payload := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{Text: "ok"},
					},
				},
			},
		},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:        0,
			ToolUsePromptTokenCount: 0,
			CandidatesTokenCount:    90,
			ThoughtsTokenCount:      10,
			TotalTokenCount:         110,
		},
	}

	body, err := common.Marshal(payload)
	require.NoError(t, err)

	resp := &http.Response{
		Body: io.NopCloser(bytes.NewReader(body)),
	}

	usage, newAPIError := GeminiChatHandler(c, info, resp)
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Equal(t, 20, usage.PromptTokens)
	require.Equal(t, 100, usage.CompletionTokens)
	require.Equal(t, 110, usage.TotalTokens)
}

func TestGeminiStreamHandlerUsesEstimatedPromptTokensWhenUsagePromptMissing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	oldStreamingTimeout := constant.StreamingTimeout
	constant.StreamingTimeout = 300
	t.Cleanup(func() {
		constant.StreamingTimeout = oldStreamingTimeout
	})

	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3-flash-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3-flash-preview",
		},
	}
	info.SetEstimatePromptTokens(20)

	chunk := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{Text: "partial"},
					},
				},
			},
		},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:        0,
			ToolUsePromptTokenCount: 0,
			CandidatesTokenCount:    90,
			ThoughtsTokenCount:      10,
			TotalTokenCount:         110,
		},
	}

	chunkData, err := common.Marshal(chunk)
	require.NoError(t, err)

	streamBody := []byte("data: " + string(chunkData) + "\n" + "data: [DONE]\n")
	resp := &http.Response{
		Body: io.NopCloser(bytes.NewReader(streamBody)),
	}

	usage, newAPIError := geminiStreamHandler(c, info, resp, func(_ string, _ *dto.GeminiChatResponse) bool {
		return true
	})
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Equal(t, 20, usage.PromptTokens)
	require.Equal(t, 100, usage.CompletionTokens)
	require.Equal(t, 110, usage.TotalTokens)
}

func TestGeminiTextGenerationHandlerUsesEstimatedPromptTokensWhenUsagePromptMissing(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-3-flash-preview:generateContent", nil)

	info := &relaycommon.RelayInfo{
		OriginModelName: "gemini-3-flash-preview",
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: "gemini-3-flash-preview",
		},
	}
	info.SetEstimatePromptTokens(20)

	payload := dto.GeminiChatResponse{
		Candidates: []dto.GeminiChatCandidate{
			{
				Content: dto.GeminiChatContent{
					Role: "model",
					Parts: []dto.GeminiPart{
						{Text: "ok"},
					},
				},
			},
		},
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:        0,
			ToolUsePromptTokenCount: 0,
			CandidatesTokenCount:    90,
			ThoughtsTokenCount:      10,
			TotalTokenCount:         110,
		},
	}

	body, err := common.Marshal(payload)
	require.NoError(t, err)

	resp := &http.Response{
		Body: io.NopCloser(bytes.NewReader(body)),
	}

	usage, newAPIError := GeminiTextGenerationHandler(c, info, resp)
	require.Nil(t, newAPIError)
	require.NotNil(t, usage)
	require.Equal(t, 20, usage.PromptTokens)
	require.Equal(t, 100, usage.CompletionTokens)
	require.Equal(t, 110, usage.TotalTokens)
}
