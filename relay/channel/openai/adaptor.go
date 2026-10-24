package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/ai360"
	"github.com/QuantumNous/new-api/relay/channel/lingyiwanwu"

	//"github.com/QuantumNous/new-api/relay/channel/minimax"
	"github.com/QuantumNous/new-api/relay/channel/openrouter"
	"github.com/QuantumNous/new-api/relay/channel/xinference"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/common_handler"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/QuantumNous/new-api/types"
	"github.com/samber/lo"

	"github.com/gin-gonic/gin"
)

type Adaptor struct {
	ChannelType    int
	ResponseFormat string
}

func (a *Adaptor) ConvertGeminiRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeminiChatRequest) (any, error) {
	// 使用 service.GeminiToOpenAIRequest 转换请求格式
	openaiRequest, err := service.GeminiToOpenAIRequest(request, info)
	if err != nil {
		return nil, err
	}
	return a.ConvertOpenAIRequest(c, info, openaiRequest)
}

func (a *Adaptor) ConvertClaudeRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.ClaudeRequest) (any, error) {
	//if !strings.Contains(request.Model, "claude") {
	//	return nil, fmt.Errorf("you are using openai channel type with path /v1/messages, only claude model supported convert, but got %s", request.Model)
	//}
	//if common.DebugEnabled {
	//	bodyBytes := []byte(common.GetJsonString(request))
	//	err := os.WriteFile(fmt.Sprintf("claude_request_%s.txt", c.GetString(common.RequestIdKey)), bodyBytes, 0644)
	//	if err != nil {
	//		println(fmt.Sprintf("failed to save request body to file: %v", err))
	//	}
	//}
	aiRequest, err := service.ClaudeToOpenAIRequest(*request, info)
	if err != nil {
		return nil, err
	}
	//if common.DebugEnabled {
	//	println(fmt.Sprintf("convert claude to openai request result: %s", common.GetJsonString(aiRequest)))
	//	// Save request body to file for debugging
	//	bodyBytes := []byte(common.GetJsonString(aiRequest))
	//	err = os.WriteFile(fmt.Sprintf("claude_to_openai_request_%s.txt", c.GetString(common.RequestIdKey)), bodyBytes, 0644)
	//	if err != nil {
	//		println(fmt.Sprintf("failed to save request body to file: %v", err))
	//	}
	//}
	if info.SupportStreamOptions && info.IsStream {
		aiRequest.StreamOptions = &dto.StreamOptions{
			IncludeUsage: true,
		}
	}
	return a.ConvertOpenAIRequest(c, info, aiRequest)
}

func (a *Adaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType

	// initialize ThinkingContentInfo when thinking_to_content is enabled
	if info.ChannelSetting.ThinkingToContent {
		info.ThinkingContentInfo = relaycommon.ThinkingContentInfo{
			IsFirstThinkingContent:  true,
			SendLastThinkingContent: false,
			HasSentThinkingContent:  false,
		}
	}
}

func (a *Adaptor) GetRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if info.RelayMode == relayconstant.RelayModeRealtime {
		if strings.HasPrefix(info.ChannelBaseUrl, "https://") {
			baseUrl := strings.TrimPrefix(info.ChannelBaseUrl, "https://")
			baseUrl = "wss://" + baseUrl
			info.ChannelBaseUrl = baseUrl
		} else if strings.HasPrefix(info.ChannelBaseUrl, "http://") {
			baseUrl := strings.TrimPrefix(info.ChannelBaseUrl, "http://")
			baseUrl = "ws://" + baseUrl
			info.ChannelBaseUrl = baseUrl
		}
	}
	switch info.ChannelType {
	case constant.ChannelTypeAzure:
		apiVersion := info.ApiVersion
		if apiVersion == "" {
			apiVersion = constant.AzureDefaultAPIVersion
		}
		// https://learn.microsoft.com/en-us/azure/cognitive-services/openai/chatgpt-quickstart?pivots=rest-api&tabs=command-line#rest-api
		requestURL := strings.Split(info.RequestURLPath, "?")[0]
		requestURL = fmt.Sprintf("%s?api-version=%s", requestURL, apiVersion)
		task := strings.TrimPrefix(requestURL, "/v1/")

		if info.RelayFormat == types.RelayFormatClaude {
			task = strings.TrimPrefix(task, "messages")
			task = "chat/completions" + task
		}

		// 特殊处理 responses API（包含 compact）
		if info.RelayMode == relayconstant.RelayModeResponses || info.RelayMode == relayconstant.RelayModeResponsesCompact {
			responsesApiVersion := "preview"

			subUrl := "/openai/v1/responses"
			if strings.Contains(info.ChannelBaseUrl, "cognitiveservices.azure.com") {
				subUrl = "/openai/responses"
				responsesApiVersion = apiVersion
			}

			if info.ChannelOtherSettings.AzureResponsesVersion != "" {
				responsesApiVersion = info.ChannelOtherSettings.AzureResponsesVersion
			}

			// compact 模式追加 /compact
			if info.RelayMode == relayconstant.RelayModeResponsesCompact {
				subUrl = subUrl + "/compact"
			}

			requestURL = fmt.Sprintf("%s?api-version=%s", subUrl, responsesApiVersion)
			return relaycommon.GetFullRequestURL(info.ChannelBaseUrl, requestURL, info.ChannelType), nil
		}

		model_ := info.UpstreamModelName
		// 2025年5月10日后创建的渠道不移除.
		if info.ChannelCreateTime < constant.AzureNoRemoveDotTime {
			model_ = strings.Replace(model_, ".", "", -1)
		}
		// https://github.com/songquanpeng/one-api/issues/67
		requestURL = fmt.Sprintf("/openai/deployments/%s/%s", model_, task)
		if info.RelayMode == relayconstant.RelayModeRealtime {
			requestURL = fmt.Sprintf("/openai/realtime?deployment=%s&api-version=%s", model_, apiVersion)
		}
		return relaycommon.GetFullRequestURL(info.ChannelBaseUrl, requestURL, info.ChannelType), nil
	//case constant.ChannelTypeMiniMax:
	//	return minimax.GetRequestURL(info)
	case constant.ChannelTypeCustom:
		url := info.ChannelBaseUrl
		url = strings.Replace(url, "{model}", info.UpstreamModelName, -1)
		return url, nil
	default:
		if (info.RelayFormat == types.RelayFormatClaude || info.RelayFormat == types.RelayFormatGemini) &&
			info.RelayMode != relayconstant.RelayModeResponses &&
			info.RelayMode != relayconstant.RelayModeResponsesCompact {
			return fmt.Sprintf("%s/v1/chat/completions", info.ChannelBaseUrl), nil
		}
		// 网宿等完整网关地址：Base URL 已带路径时去掉本地 /v1 前缀
		if relaycommon.BaseURLHasPath(info.ChannelBaseUrl) {
			return relaycommon.JoinUpstreamPath(info.ChannelBaseUrl, info.RequestURLPath), nil
		}
		return relaycommon.GetFullRequestURL(info.ChannelBaseUrl, info.RequestURLPath, info.ChannelType), nil
	}
}

func (a *Adaptor) SetupRequestHeader(c *gin.Context, header *http.Header, info *relaycommon.RelayInfo) error {
	channel.SetupApiRequestHeader(info, c, header)
	if info.RelayMode == relayconstant.RelayModeResponses {
		if beta := strings.TrimSpace(c.GetHeader("OpenAI-Beta")); beta != "" {
			header.Set("OpenAI-Beta", beta)
		}
	}
	if info.ChannelType == constant.ChannelTypeAzure {
		header.Set("api-key", info.ApiKey)
		return nil
	}
	if info.ChannelType == constant.ChannelTypeOpenAI && "" != info.Organization {
		header.Set("OpenAI-Organization", info.Organization)
	}
	// 检查 Header Override 是否已设置 Authorization，如果已设置则跳过默认设置
	// 这样可以避免在 Header Override 应用时被覆盖（虽然 Header Override 会在之后应用，但这里作为额外保护）
	hasAuthOverride := false
	if len(info.HeadersOverride) > 0 {
		for k := range info.HeadersOverride {
			if strings.EqualFold(k, "Authorization") {
				hasAuthOverride = true
				break
			}
		}
	}
	if info.RelayMode == relayconstant.RelayModeRealtime {
		swp := c.Request.Header.Get("Sec-WebSocket-Protocol")
		if swp != "" {
			items := []string{
				"realtime",
				"openai-insecure-api-key." + info.ApiKey,
				"openai-beta.realtime-v1",
			}
			header.Set("Sec-WebSocket-Protocol", strings.Join(items, ","))
			//req.Header.Set("Sec-WebSocket-Key", c.Request.Header.Get("Sec-WebSocket-Key"))
			//req.Header.Set("Sec-Websocket-Extensions", c.Request.Header.Get("Sec-Websocket-Extensions"))
			//req.Header.Set("Sec-Websocket-Version", c.Request.Header.Get("Sec-Websocket-Version"))
		} else {
			header.Set("openai-beta", "realtime=v1")
			if !hasAuthOverride {
				header.Set("Authorization", "Bearer "+info.ApiKey)
			}
		}
	} else {
		if !hasAuthOverride {
			header.Set("Authorization", "Bearer "+info.ApiKey)
		}
	}
	if info.ChannelType == constant.ChannelTypeOpenRouter {
		if header.Get("HTTP-Referer") == "" {
			header.Set("HTTP-Referer", "https://www.newapi.ai")
		}
		if header.Get("X-OpenRouter-Title") == "" {
			header.Set("X-OpenRouter-Title", "New API")
		}
	}
	return nil
}

func (a *Adaptor) ConvertOpenAIRequest(c *gin.Context, info *relaycommon.RelayInfo, request *dto.GeneralOpenAIRequest) (any, error) {
	if request == nil {
		return nil, errors.New("request is nil")
	}
	if info.ChannelType != constant.ChannelTypeOpenAI && info.ChannelType != constant.ChannelTypeAzure {
		request.StreamOptions = nil
	}
	if info.ChannelType == constant.ChannelTypeOpenRouter {
		if len(request.Usage) == 0 {
			request.Usage = json.RawMessage(`{"include":true}`)
		}
		// 适配 OpenRouter 的 thinking 后缀
		if !model_setting.ShouldPreserveThinkingSuffix(info.OriginModelName) &&
			strings.HasSuffix(info.UpstreamModelName, "-thinking") {
			info.UpstreamModelName = strings.TrimSuffix(info.UpstreamModelName, "-thinking")
			request.Model = info.UpstreamModelName
			if len(request.Reasoning) == 0 {
				reasoning := map[string]any{
					"enabled": true,
				}
				if request.ReasoningEffort != "" && request.ReasoningEffort != "none" {
					reasoning["effort"] = request.ReasoningEffort
				}
				marshal, err := common.Marshal(reasoning)
				if err != nil {
					return nil, fmt.Errorf("error marshalling reasoning: %w", err)
				}
				request.Reasoning = marshal
			}
			// 清空多余的ReasoningEffort
			request.ReasoningEffort = ""
		} else {
			if len(request.Reasoning) == 0 {
				// 适配 OpenAI 的 ReasoningEffort 格式
				if request.ReasoningEffort != "" {
					reasoning := map[string]any{
						"enabled": true,
					}
					if request.ReasoningEffort != "none" {
						reasoning["effort"] = request.ReasoningEffort
						marshal, err := common.Marshal(reasoning)
						if err != nil {
							return nil, fmt.Errorf("error marshalling reasoning: %w", err)
						}
						request.Reasoning = marshal
					}
				}
			}
			request.ReasoningEffort = ""
		}

		// https://docs.anthropic.com/en/api/openai-sdk#extended-thinking-support
		// 没有做排除3.5Haiku等，要出问题再加吧，最佳兼容性（不是
		if request.THINKING != nil && strings.HasPrefix(info.UpstreamModelName, "anthropic") {
			var thinking dto.Thinking // Claude标准Thinking格式
			if err := json.Unmarshal(request.THINKING, &thinking); err != nil {
				return nil, fmt.Errorf("error Unmarshal thinking: %w", err)
			}

			// 只有当 thinking.Type 是 "enabled" 时才处理
			if thinking.Type == "enabled" {
				// 检查 BudgetTokens 是否为 nil
				if thinking.BudgetTokens == nil {
					return nil, fmt.Errorf("BudgetTokens is nil when thinking is enabled")
				}

				reasoning := openrouter.RequestReasoning{
					Enabled:   true,
					MaxTokens: *thinking.BudgetTokens,
				}

				marshal, err := common.Marshal(reasoning)
				if err != nil {
					return nil, fmt.Errorf("error marshalling reasoning: %w", err)
				}

				request.Reasoning = marshal
			}

			// 清空 THINKING
			request.THINKING = nil
		}

	}
	if strings.HasPrefix(info.UpstreamModelName, "o") || strings.HasPrefix(info.UpstreamModelName, "gpt-5") {
		if lo.FromPtrOr(request.MaxCompletionTokens, uint(0)) == 0 && lo.FromPtrOr(request.MaxTokens, uint(0)) != 0 {
			request.MaxCompletionTokens = request.MaxTokens
			request.MaxTokens = nil
		}

		if strings.HasPrefix(info.UpstreamModelName, "o") {
			request.Temperature = nil
		}

		// gpt-5系列模型适配 归零不再支持的参数
		if strings.HasPrefix(info.UpstreamModelName, "gpt-5") {
			request.Temperature = nil
			request.TopP = nil
			request.LogProbs = nil
		}

		// 转换模型推理力度后缀
		effort, originModel := reasoning.ParseOpenAIReasoningEffortFromModelSuffix(info.UpstreamModelName)
		if effort != "" {
			request.ReasoningEffort = effort
			info.UpstreamModelName = originModel
			request.Model = originModel
		}

		info.ReasoningEffort = request.ReasoningEffort

		// o系列模型developer适配（o1-mini除外）
		if !strings.HasPrefix(info.UpstreamModelName, "o1-mini") && !strings.HasPrefix(info.UpstreamModelName, "o1-preview") {
			//修改第一个Message的内容，将system改为developer
			if len(request.Messages) > 0 && request.Messages[0].Role == "system" {
				request.Messages[0].Role = "developer"
			}
		}
	}
	if info.ChannelType == constant.ChannelTypeOpenAI {
		if err := applyOpenAIChannelGeminiImageCompat(request, info); err != nil {
			return nil, err
		}
	}

	return request, nil
}

func (a *Adaptor) ConvertRerankRequest(c *gin.Context, relayMode int, request dto.RerankRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertEmbeddingRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.EmbeddingRequest) (any, error) {
	return request, nil
}

func (a *Adaptor) ConvertAudioRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.AudioRequest) (io.Reader, error) {
	a.ResponseFormat = request.ResponseFormat
	if info.RelayMode == relayconstant.RelayModeAudioSpeech {
		jsonData, err := common.Marshal(request)
		if err != nil {
			return nil, fmt.Errorf("error marshalling object: %w", err)
		}
		return bytes.NewReader(jsonData), nil
	} else {
		var requestBody bytes.Buffer
		writer := multipart.NewWriter(&requestBody)

		writer.WriteField("model", request.Model)

		formData, err2 := common.ParseMultipartFormReusable(c)
		if err2 != nil {
			return nil, fmt.Errorf("error parsing multipart form: %w", err2)
		}

		// 打印类似 curl 命令格式的信息
		logger.LogDebug(c.Request.Context(), fmt.Sprintf("--form 'model=\"%s\"'", request.Model))

		// 遍历表单字段并打印输出
		for key, values := range formData.Value {
			if key == "model" {
				continue
			}
			for _, value := range values {
				writer.WriteField(key, value)
				logger.LogDebug(c.Request.Context(), fmt.Sprintf("--form '%s=\"%s\"'", key, value))
			}
		}

		// 从 formData 中获取文件
		fileHeaders := formData.File["file"]
		if len(fileHeaders) == 0 {
			return nil, errors.New("file is required")
		}

		// 使用 formData 中的第一个文件
		fileHeader := fileHeaders[0]
		logger.LogDebug(c.Request.Context(), fmt.Sprintf("--form 'file=@\"%s\"' (size: %d bytes, content-type: %s)",
			fileHeader.Filename, fileHeader.Size, fileHeader.Header.Get("Content-Type")))

		file, err := fileHeader.Open()
		if err != nil {
			return nil, fmt.Errorf("error opening audio file: %v", err)
		}
		defer file.Close()

		part, err := writer.CreateFormFile("file", fileHeader.Filename)
		if err != nil {
			return nil, errors.New("create form file failed")
		}
		if _, err := io.Copy(part, file); err != nil {
			return nil, errors.New("copy file failed")
		}

		// 关闭 multipart 编写器以设置分界线
		writer.Close()
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		logger.LogDebug(c.Request.Context(), fmt.Sprintf("--header 'Content-Type: %s'", writer.FormDataContentType()))
		return &requestBody, nil
	}
}

func (a *Adaptor) ConvertImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest) (any, error) {
	apiProChannel := isAPIProImageChannel(info)
	if !apiProChannel {
		request.ResponseFormat = ""
	}

	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations:
		if apiProChannel {
			return buildAPIProImageJSONRequest(request, false)
		}
		return request, nil

	case relayconstant.RelayModeImagesEdits:
		if isJSONRequest(c) {
			if apiProChannel {
				return buildAPIProImageJSONRequest(request, true)
			}
			if imageInputs := imageRequestURLInputs(request); len(imageInputs) > 0 {
				return buildImageURLMultipartRequest(c, info, request, imageInputs)
			}
			return request, nil
		}

		var requestBody bytes.Buffer
		writer := multipart.NewWriter(&requestBody)

		writer.WriteField("model", request.Model)
		// 使用已解析的 multipart 表单，避免重复解析
		mf := c.Request.MultipartForm
		if mf == nil {
			if _, err := c.MultipartForm(); err != nil {
				return nil, errors.New("failed to parse multipart form")
			}
			mf = c.Request.MultipartForm
		}

		var imageURLValues []string

		// 写入所有非文件字段
		if mf != nil {
			for key, values := range mf.Value {
				if key == "model" {
					continue
				}
				if shouldSkipOpenAIImageUpstreamField(info, key) {
					continue
				}
				if isImageURLFormField(key) {
					for _, value := range values {
						if cleaned := cleanImageInput(value); cleaned != "" {
							imageURLValues = append(imageURLValues, cleaned)
						}
					}
					continue
				}
				for _, value := range values {
					writer.WriteField(key, value)
				}
			}
		}

		var imageFiles []*multipart.FileHeader
		if mf != nil && mf.File != nil {
			// Check if "image" field exists in any form, including array notation
			var exists bool

			// First check for standard "image" field
			if imageFiles, exists = mf.File["image"]; !exists || len(imageFiles) == 0 {
				// If not found, check for "image[]" field
				if imageFiles, exists = mf.File["image[]"]; !exists || len(imageFiles) == 0 {
					// If still not found, iterate through all fields to find any that start with "image["
					foundArrayImages := false
					for fieldName, files := range mf.File {
						if strings.HasPrefix(fieldName, "image[") && len(files) > 0 {
							foundArrayImages = true
							imageFiles = append(imageFiles, files...)
						}
					}

					// If no image fields found at all
					if !foundArrayImages && len(imageFiles) == 0 && len(imageURLValues) == 0 {
						return nil, errors.New("image is required")
					}
				}
			}
		}

		totalImageInputs := len(imageURLValues) + len(imageFiles)
		if totalImageInputs == 0 {
			return nil, errors.New("image is required")
		}
		if err := writeImageURLFields(writer, imageURLValues, totalImageInputs); err != nil {
			return nil, err
		}

		if len(imageFiles) > 0 {
			// Process all image files
			for i, fileHeader := range imageFiles {
				file, err := fileHeader.Open()
				if err != nil {
					return nil, fmt.Errorf("failed to open image file %d: %w", i, err)
				}

				// If multiple images, use image[] as the field name
				fieldName := "image"
				if totalImageInputs > 1 {
					fieldName = "image[]"
				}

				// Determine MIME type based on file extension
				mimeType := detectImageMimeType(fileHeader.Filename)

				// Create a form file with the appropriate content type
				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, fileHeader.Filename))
				h.Set("Content-Type", mimeType)

				part, err := writer.CreatePart(h)
				if err != nil {
					return nil, fmt.Errorf("create form part failed for image %d: %w", i, err)
				}

				if _, err := io.Copy(part, file); err != nil {
					return nil, fmt.Errorf("copy file failed for image %d: %w", i, err)
				}

				// 复制完立即关闭，避免在循环内使用 defer 占用资源
				_ = file.Close()
			}

			// Handle mask file if present
			if maskFiles, exists := mf.File["mask"]; exists && len(maskFiles) > 0 {
				maskFile, err := maskFiles[0].Open()
				if err != nil {
					return nil, errors.New("failed to open mask file")
				}
				// 复制完立即关闭，避免在循环内使用 defer 占用资源

				// Determine MIME type for mask file
				mimeType := detectImageMimeType(maskFiles[0].Filename)

				// Create a form file with the appropriate content type
				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="mask"; filename="%s"`, maskFiles[0].Filename))
				h.Set("Content-Type", mimeType)

				maskPart, err := writer.CreatePart(h)
				if err != nil {
					return nil, errors.New("create form file failed for mask")
				}

				if _, err := io.Copy(maskPart, maskFile); err != nil {
					return nil, errors.New("copy mask file failed")
				}
				_ = maskFile.Close()
			}
		}

		// 关闭 multipart 编写器以设置分界线
		writer.Close()
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
		return &requestBody, nil

	default:
		return request, nil
	}
}

func buildImageURLMultipartRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest, imageInputs []string) (*bytes.Buffer, error) {
	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)

	if err := writeImageRequestMultipartFields(writer, info, request); err != nil {
		return nil, err
	}
	if err := writeImageURLFields(writer, imageInputs, len(imageInputs)); err != nil {
		return nil, err
	}

	if err := writer.Close(); err != nil {
		return nil, err
	}
	if c != nil && c.Request != nil {
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	}
	return &requestBody, nil
}

func writeImageRequestMultipartFields(writer *multipart.Writer, info *relaycommon.RelayInfo, request dto.ImageRequest) error {
	if request.Model != "" {
		if err := writer.WriteField("model", request.Model); err != nil {
			return err
		}
	}
	if request.Prompt != "" {
		if err := writer.WriteField("prompt", request.Prompt); err != nil {
			return err
		}
	}
	if request.N != nil {
		if err := writer.WriteField("n", strconv.FormatUint(uint64(*request.N), 10)); err != nil {
			return err
		}
	}
	if request.Size != "" {
		if err := writer.WriteField("size", request.Size); err != nil {
			return err
		}
	}
	if request.Quality != "" {
		if err := writer.WriteField("quality", request.Quality); err != nil {
			return err
		}
	}
	if request.Watermark != nil {
		if err := writer.WriteField("watermark", strconv.FormatBool(*request.Watermark)); err != nil {
			return err
		}
	}

	rawFields := map[string]json.RawMessage{
		"style":              request.Style,
		"user":               request.User,
		"extra_fields":       request.ExtraFields,
		"background":         request.Background,
		"moderation":         request.Moderation,
		"output_format":      request.OutputFormat,
		"output_compression": request.OutputCompression,
		"partial_images":     request.PartialImages,
		"mask":               request.Mask,
		"input_fidelity":     request.InputFidelity,
		"watermark_enabled":  request.WatermarkEnabled,
		"user_id":            request.UserId,
	}
	for key, raw := range rawFields {
		if err := writeRawMultipartField(writer, key, raw); err != nil {
			return err
		}
	}

	for key, raw := range request.Extra {
		if shouldSkipOpenAIImageUpstreamField(info, key) || isImageURLFormField(key) {
			continue
		}
		if err := writeRawMultipartField(writer, key, raw); err != nil {
			return err
		}
	}
	return nil
}

func shouldSkipOpenAIImageUpstreamField(info *relaycommon.RelayInfo, key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "response_format" {
		if !isAPIProImageChannel(info) {
			return true
		}
		return isAPIProGPTImage2(info.UpstreamModelName)
	}
	if !isAPIProImageChannel(info) {
		return false
	}
	if key == "mode" {
		return true
	}
	return isAPIProGrokOnlyImageField(key) && !isGrokImageModel(info.UpstreamModelName)
}

func isAPIProImageChannel(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(info.ChannelBaseUrl))
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Hostname()) {
	case "api.apipro.ai", "api.wenwen-ai.com":
		return true
	default:
		return false
	}
}

func buildAPIProImageJSONRequest(request dto.ImageRequest, normalizeImages bool) (map[string]json.RawMessage, error) {
	base, err := request.MarshalJSON()
	if err != nil {
		return nil, err
	}
	body := make(map[string]json.RawMessage)
	if err := json.Unmarshal(base, &body); err != nil {
		return nil, err
	}
	if shouldSkipAPIProImageJSONField(request.Model, "response_format") {
		delete(body, "response_format")
	}

	for key, raw := range request.Extra {
		if shouldSkipAPIProImageJSONField(request.Model, key) || isImageURLFormField(key) {
			continue
		}
		body[key] = raw
	}
	if !normalizeImages {
		return body, nil
	}

	imageRaw := request.Images
	if isEmptyJSONRaw(imageRaw) {
		imageRaw = request.Image
	}
	if isEmptyJSONRaw(imageRaw) {
		for _, key := range []string{"referenceImages", "reference_images", "image_urls", "imageUrls"} {
			if raw := request.Extra[key]; !isEmptyJSONRaw(raw) {
				imageRaw = raw
				break
			}
		}
	}
	if !isEmptyJSONRaw(imageRaw) {
		normalized, err := normalizeAPIProImageInputs(imageRaw)
		if err != nil {
			return nil, err
		}
		body["images"] = normalized
		delete(body, "image")
	}
	return body, nil
}

func shouldSkipAPIProImageJSONField(modelName, key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "mode" {
		return true
	}
	if key == "response_format" {
		return isAPIProGPTImage2(modelName)
	}
	return isAPIProGrokOnlyImageField(key) && !isGrokImageModel(modelName)
}

func isAPIProGPTImage2(modelName string) bool {
	return strings.EqualFold(strings.TrimSpace(modelName), "gpt-image-2")
}

func isAPIProGrokOnlyImageField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "aspect_ratio", "resolution":
		return true
	default:
		return false
	}
}

func isGrokImageModel(modelName string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(modelName)), "grok")
}

func normalizeAPIProImageInputs(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	items := []json.RawMessage{trimmed}
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return nil, fmt.Errorf("invalid images array: %w", err)
		}
	}

	normalized := make([]json.RawMessage, 0, len(items))
	for index, item := range items {
		item = bytes.TrimSpace(item)
		if len(item) == 0 || bytes.Equal(item, []byte("null")) {
			continue
		}
		if item[0] == '{' {
			normalized = append(normalized, item)
			continue
		}
		var imageURL string
		if err := json.Unmarshal(item, &imageURL); err != nil {
			return nil, fmt.Errorf("images[%d] must be a URL string or object", index)
		}
		imageURL = cleanImageInput(imageURL)
		if imageURL == "" {
			return nil, fmt.Errorf("images[%d] URL is empty", index)
		}
		encoded, err := json.Marshal(map[string]string{"image_url": imageURL})
		if err != nil {
			return nil, err
		}
		normalized = append(normalized, encoded)
	}
	return json.Marshal(normalized)
}

func isEmptyJSONRaw(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

func writeRawMultipartField(writer *multipart.Writer, key string, raw json.RawMessage) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	value := string(trimmed)
	if trimmed[0] == '"' {
		if err := json.Unmarshal(trimmed, &value); err != nil {
			value = string(trimmed)
		}
	}
	return writer.WriteField(key, value)
}

func imageRequestURLInputs(request dto.ImageRequest) []string {
	var inputs []string
	inputs = appendImageInputsFromRaw(inputs, request.Images)
	inputs = appendImageInputsFromRaw(inputs, request.Image)
	for _, key := range []string{"referenceImages", "reference_images", "image_urls", "imageUrls"} {
		if request.Extra != nil {
			inputs = appendImageInputsFromRaw(inputs, request.Extra[key])
		}
	}
	return uniqueImageInputs(inputs)
}

func appendImageInputsFromRaw(inputs []string, raw json.RawMessage) []string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return inputs
	}

	switch trimmed[0] {
	case '"':
		var value string
		if err := json.Unmarshal(trimmed, &value); err == nil {
			if cleaned := cleanImageInput(value); cleaned != "" {
				inputs = append(inputs, cleaned)
			}
		}
	case '[':
		var values []json.RawMessage
		if err := json.Unmarshal(trimmed, &values); err == nil {
			for _, value := range values {
				inputs = appendImageInputsFromRaw(inputs, value)
			}
		}
	case '{':
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &obj); err == nil {
			for _, key := range []string{"url", "image_url", "imageUrl"} {
				inputs = appendImageInputsFromRaw(inputs, obj[key])
			}
		}
	default:
		if cleaned := cleanImageInput(string(trimmed)); cleaned != "" {
			inputs = append(inputs, cleaned)
		}
	}
	return inputs
}

func cleanImageInput(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "[") {
		if mid := strings.Index(value, "]("); mid > 0 && strings.HasSuffix(value, ")") {
			value = value[mid+2 : len(value)-1]
		}
	}
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") {
		value = value[1 : len(value)-1]
	}
	return strings.TrimSpace(value)
}

func uniqueImageInputs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = cleanImageInput(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func isImageURLFormField(key string) bool {
	key = strings.TrimSpace(key)
	lower := strings.ToLower(key)
	if strings.HasPrefix(lower, "image[") {
		return true
	}
	lower = strings.TrimSuffix(lower, "[]")
	switch lower {
	case "image", "images", "image_url", "image_urls", "imageurls", "referenceimages", "reference_images":
		return true
	default:
		return false
	}
}

func writeImageURLFields(writer *multipart.Writer, imageURLValues []string, totalImageInputs int) error {
	imageURLValues = uniqueImageInputs(imageURLValues)
	fieldName := "image"
	if totalImageInputs > 1 {
		fieldName = "image[]"
	}
	for i, imageURL := range imageURLValues {
		if err := writeImageURLFilePart(writer, fieldName, imageURL, i); err != nil {
			return err
		}
	}
	return nil
}

func writeImageURLFilePart(writer *multipart.Writer, fieldName string, imageURL string, index int) error {
	resp, err := service.DoDownloadRequest(imageURL, "openai_image_edit_reference")
	if err != nil {
		return fmt.Errorf("download image url %d failed: %w", index+1, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("download image url %d failed: HTTP %d", index+1, resp.StatusCode)
	}

	maxBytes := imageURLMultipartMaxBytes()
	if resp.ContentLength > maxBytes {
		return fmt.Errorf("download image url %d exceeds maximum size: %d > %d", index+1, resp.ContentLength, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read image url %d failed: %w", index+1, err)
	}
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("download image url %d exceeds maximum size: %d > %d", index+1, len(data), maxBytes)
	}

	contentType := normalizeDownloadedImageContentType(resp.Header.Get("Content-Type"), data)
	if !strings.HasPrefix(contentType, "image/") {
		return fmt.Errorf("download image url %d has invalid content type: %s", index+1, contentType)
	}
	filename := filenameFromImageURL(imageURL, index, contentType)

	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, filename))
	h.Set("Content-Type", contentType)
	part, err := writer.CreatePart(h)
	if err != nil {
		return fmt.Errorf("create image url form part %d failed: %w", index+1, err)
	}
	if _, err := part.Write(data); err != nil {
		return fmt.Errorf("write image url form part %d failed: %w", index+1, err)
	}
	return nil
}

func imageURLMultipartMaxBytes() int64 {
	mb := constant.MaxFileDownloadMB
	if mb <= 0 {
		mb = 64
	}
	return int64(mb) * 1024 * 1024
}

func normalizeDownloadedImageContentType(raw string, data []byte) string {
	contentType := strings.TrimSpace(raw)
	if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
		contentType = parsed
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if contentType == "" || contentType == "application/octet-stream" || contentType == "binary/octet-stream" {
		contentType = strings.ToLower(http.DetectContentType(data))
	}
	return contentType
}

func filenameFromImageURL(imageURL string, index int, contentType string) string {
	filename := ""
	if parsed, err := url.Parse(imageURL); err == nil {
		filename = path.Base(parsed.Path)
	}
	if filename == "." || filename == "/" || filename == "" {
		filename = fmt.Sprintf("image-%d", index+1)
	}
	filename = sanitizeMultipartFilename(filename)
	if filepath.Ext(filename) == "" {
		filename += extensionFromContentType(contentType)
	}
	return filename
}

func sanitizeMultipartFilename(filename string) string {
	filename = strings.TrimSpace(filename)
	filename = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, filename)
	filename = strings.Trim(filename, ".-")
	if filename == "" {
		return "image"
	}
	return filename
}

func extensionFromContentType(contentType string) string {
	if contentType == "image/jpeg" {
		return ".jpg"
	}
	if exts, err := mime.ExtensionsByType(contentType); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".png"
}

func isJSONRequest(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	return strings.HasPrefix(c.Request.Header.Get("Content-Type"), "application/json")
}

// detectImageMimeType determines the MIME type based on the file extension
func detectImageMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	default:
		// Try to detect from extension if possible
		if strings.HasPrefix(ext, ".jp") {
			return "image/jpeg"
		}
		// Default to png as a fallback
		return "image/png"
	}
}

func (a *Adaptor) ConvertOpenAIResponsesRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.OpenAIResponsesRequest) (any, error) {
	//  转换模型推理力度后缀
	effort, originModel := reasoning.ParseOpenAIReasoningEffortFromModelSuffix(request.Model)
	if effort != "" {
		if request.Reasoning == nil {
			request.Reasoning = &dto.Reasoning{
				Effort: effort,
			}
		} else {
			request.Reasoning.Effort = effort
		}
		request.Model = originModel
	}
	if info != nil && request.Reasoning != nil && request.Reasoning.Effort != "" {
		info.ReasoningEffort = request.Reasoning.Effort
	}
	return request, nil
}

func (a *Adaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (any, error) {
	if info.RelayMode == relayconstant.RelayModeAudioTranscription ||
		info.RelayMode == relayconstant.RelayModeAudioTranslation ||
		(info.RelayMode == relayconstant.RelayModeImagesEdits && !isJSONRequest(c)) {
		return channel.DoFormRequest(a, c, info, requestBody)
	} else if info.RelayMode == relayconstant.RelayModeRealtime {
		return channel.DoWssRequest(a, c, info, requestBody)
	} else {
		return channel.DoApiRequest(a, c, info, requestBody)
	}
}

func (a *Adaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (usage any, err *types.NewAPIError) {
	switch info.RelayMode {
	case relayconstant.RelayModeRealtime:
		err, usage = OpenaiRealtimeHandler(c, info)
	case relayconstant.RelayModeAudioSpeech:
		usage = OpenaiTTSHandler(c, resp, info)
	case relayconstant.RelayModeAudioTranslation:
		fallthrough
	case relayconstant.RelayModeAudioTranscription:
		err, usage = OpenaiSTTHandler(c, resp, info, a.ResponseFormat)
	case relayconstant.RelayModeImagesGenerations, relayconstant.RelayModeImagesEdits:
		usage, err = OpenaiHandlerWithUsage(c, info, resp)
	case relayconstant.RelayModeRerank:
		usage, err = common_handler.RerankHandler(c, info, resp)
	case relayconstant.RelayModeResponses:
		if info.IsStream {
			usage, err = OaiResponsesStreamHandler(c, info, resp)
		} else {
			usage, err = OaiResponsesHandler(c, info, resp)
		}
	case relayconstant.RelayModeResponsesCompact:
		usage, err = OaiResponsesCompactionHandler(c, resp)
	default:
		if info.IsStream {
			usage, err = OaiStreamHandler(c, info, resp)
		} else {
			usage, err = OpenaiHandler(c, info, resp)
		}
	}
	return
}

func (a *Adaptor) GetModelList() []string {
	switch a.ChannelType {
	case constant.ChannelType360:
		return ai360.ModelList
	case constant.ChannelTypeLingYiWanWu:
		return lingyiwanwu.ModelList
	//case constant.ChannelTypeMiniMax:
	//	return minimax.ModelList
	case constant.ChannelTypeXinference:
		return xinference.ModelList
	case constant.ChannelTypeOpenRouter:
		return openrouter.ModelList
	default:
		return ModelList
	}
}

func (a *Adaptor) GetChannelName() string {
	switch a.ChannelType {
	case constant.ChannelType360:
		return ai360.ChannelName
	case constant.ChannelTypeLingYiWanWu:
		return lingyiwanwu.ChannelName
	//case constant.ChannelTypeMiniMax:
	//	return minimax.ChannelName
	case constant.ChannelTypeXinference:
		return xinference.ChannelName
	case constant.ChannelTypeOpenRouter:
		return openrouter.ChannelName
	default:
		return ChannelName
	}
}
