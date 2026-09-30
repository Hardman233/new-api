package doubao

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
	"github.com/samber/lo"
)

// ============================
// Request / Response structures
// ============================

type ContentItem struct {
	Type     string    `json:"type,omitempty"`
	Text     string    `json:"text,omitempty"`
	ImageURL *MediaURL `json:"image_url,omitempty"`
	VideoURL *MediaURL `json:"video_url,omitempty"`
	AudioURL *MediaURL `json:"audio_url,omitempty"`
	Role     string    `json:"role,omitempty"`
}

type MediaURL struct {
	URL string `json:"url,omitempty"`
}

type requestPayload struct {
	Model                 string         `json:"model"`
	Content               []ContentItem  `json:"content,omitempty"`
	CallbackURL           string         `json:"callback_url,omitempty"`
	ReturnLastFrame       *dto.BoolValue `json:"return_last_frame,omitempty"`
	ServiceTier           string         `json:"service_tier,omitempty"`
	ExecutionExpiresAfter *dto.IntValue  `json:"execution_expires_after,omitempty"`
	GenerateAudio         *dto.BoolValue `json:"generate_audio,omitempty"`
	Draft                 *dto.BoolValue `json:"draft,omitempty"`
	Tools                 []struct {
		Type string `json:"type,omitempty"`
	} `json:"tools,omitempty"`
	Resolution  string         `json:"resolution,omitempty"`
	Ratio       string         `json:"ratio,omitempty"`
	Duration    *dto.IntValue  `json:"duration,omitempty"`
	Frames      *dto.IntValue  `json:"frames,omitempty"`
	Seed        *dto.IntValue  `json:"seed,omitempty"`
	CameraFixed *dto.BoolValue `json:"camera_fixed,omitempty"`
	Watermark   *dto.BoolValue `json:"watermark,omitempty"`
	ExtraBody   *struct {
		OutputFormat string `json:"output_format,omitempty"`
	} `json:"extra_body,omitempty"`
}

type responsePayload struct {
	ID     string `json:"id"`      // task_id
	TaskID string `json:"task_id"` // compatibility for wrapped/variant responses
	Data   *struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
	} `json:"data,omitempty"`
}

type responseTask struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Status  string `json:"status"`
	Content struct {
		VideoURL     string `json:"video_url"`
		FileURL      string `json:"file_url"`
		LastFrameURL string `json:"last_frame_url"`
	} `json:"content"`
	Seed                  int     `json:"seed"`
	Resolution            string  `json:"resolution"`
	Duration              int     `json:"duration"`
	Frames                int     `json:"frames"`
	Ratio                 string  `json:"ratio"`
	FramesPerSecond       int     `json:"framespersecond"`
	FileFormat            *string `json:"fileformat"`
	GenerateAudio         *bool   `json:"generate_audio"`
	RevisedPrompt         *string `json:"revised_prompt"`
	Draft                 *bool   `json:"draft"`
	DraftTaskID           *string `json:"draft_task_id"`
	SubdivisionLevel      *int    `json:"subdivisionlevel"`
	ExecutionExpiresAfter *int    `json:"execution_expires_after"`
	RequestID             *string `json:"_request_id"`
	ServiceTier           string  `json:"service_tier"`
	Tools                 []struct {
		Type string `json:"type"`
	} `json:"tools"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		ToolUsage        struct {
			WebSearch int `json:"web_search"`
		} `json:"tool_usage"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CreatedAt int64 `json:"created_at"`
	UpdatedAt int64 `json:"updated_at"`
}

const (
	officialSeedanceTaskPath     = "/api/v3/contents/generations/tasks"
	xiongdaSeedanceTaskCreate    = "create"
	xiongdaSeedanceTaskGetResult = "get_result"
)

// ============================
// Adaptor implementation
// ============================

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
}

// ValidateRequestAndSetAction parses body, validates fields and sets default action.
func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	// Accept only POST /v1/video/generations as "generate" action.
	if taskErr = relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	body, err := a.convertToRequestPayload(&req)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if err := validateSeedanceRequestPayloadForModel(body.Model, body); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	return nil
}

// BuildRequestURL constructs the upstream URL.
func (a *TaskAdaptor) BuildRequestURL(_ *relaycommon.RelayInfo) (string, error) {
	baseURL := strings.TrimRight(a.baseURL, "/")
	if isXiongdaSeedanceBaseURL(baseURL) {
		return buildXiongdaSeedanceURL(baseURL, xiongdaSeedanceTaskCreate), nil
	}
	return fmt.Sprintf("%s%s", baseURL, officialSeedanceTaskPath), nil
}

// BuildRequestHeader sets required headers.
func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

// EstimateBilling 检测请求 metadata 中的分辨率和视频输入，返回 Seedance 价格档位倍率。
func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	modelName := req.Model
	if info != nil && info.OriginModelName != "" {
		modelName = info.OriginModelName
	}
	metadata := seedanceMetadataFromRequest(&req)
	ratioKey, ratio, ok := GetSeedanceBillingRatio(modelName, seedanceResolutionFromMetadata(metadata), hasVideoInMetadata(metadata))
	if ok {
		return map[string]float64{ratioKey: ratio}
	}
	return nil
}

func seedanceResolutionFromMetadata(metadata map[string]interface{}) string {
	if metadata == nil {
		return ""
	}
	resolution, ok := metadata["resolution"]
	if !ok || resolution == nil {
		return ""
	}
	return fmt.Sprint(resolution)
}

// hasVideoInMetadata 直接检查 metadata 的 content 数组是否包含 video_url 条目，
// 避免构建完整的上游 requestPayload。
func hasVideoInMetadata(metadata map[string]interface{}) bool {
	if metadata == nil {
		return false
	}
	return hasVideoInContent(metadata["content"])
}

func hasVideoInContent(raw interface{}) bool {
	switch items := raw.(type) {
	case []interface{}:
		for _, item := range items {
			if hasVideoInContent(item) {
				return true
			}
		}
	case []map[string]interface{}:
		for _, item := range items {
			if hasVideoInContent(item) {
				return true
			}
		}
	case map[string]interface{}:
		itemType := strings.ToLower(strings.TrimSpace(fmt.Sprint(items["type"])))
		if itemType == "video_url" {
			return true
		}
		if _, ok := items["video_url"]; ok {
			return true
		}
	case ContentItem:
		return items.VideoURL != nil && strings.TrimSpace(items.VideoURL.URL) != ""
	case *ContentItem:
		return items != nil && items.VideoURL != nil && strings.TrimSpace(items.VideoURL.URL) != ""
	}
	return false
}

// BuildRequestBody converts request into Doubao specific format.
func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}

	body, err := a.convertToRequestPayload(&req)
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}
	if info.IsModelMapped {
		body.Model = info.UpstreamModelName
	} else {
		info.UpstreamModelName = body.Model
	}
	if err := a.applyMaterialAssets(c, info, body); err != nil {
		return nil, err
	}
	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

// DoRequest delegates to common helper.
func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

// DoResponse handles upstream response, returns taskID etc.
func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	// Parse Doubao response
	var dResp responsePayload
	if err := common.Unmarshal(responseBody, &dResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	upstreamTaskID := dResp.taskID()
	if upstreamTaskID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusInternalServerError)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return upstreamTaskID, responseBody, nil
}

// FetchTask fetch task status
func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	var req *http.Request
	var err error
	if isXiongdaSeedanceBaseURL(baseUrl) {
		payload, marshalErr := common.Marshal(map[string]string{"id": taskID})
		if marshalErr != nil {
			return nil, marshalErr
		}
		uri := buildXiongdaSeedanceURL(baseUrl, xiongdaSeedanceTaskGetResult)
		req, err = http.NewRequest(http.MethodPost, uri, bytes.NewReader(payload))
	} else {
		uri := fmt.Sprintf("%s%s/%s", strings.TrimRight(baseUrl, "/"), officialSeedanceTaskPath, taskID)
		req, err = http.NewRequest(http.MethodGet, uri, nil)
	}
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)

	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	if client == nil {
		client = http.DefaultClient
	}
	return client.Do(req)
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq) (*requestPayload, error) {
	r := requestPayload{
		Model:   req.Model,
		Content: []ContentItem{},
	}

	// Add images if present (SD2.0: 默认 role=first_frame，支持首帧场景)
	if req.HasImage() {
		role := "first_frame"
		if len(req.Images) > 1 {
			role = "reference_image"
		}
		for _, imgURL := range req.Images {
			r.Content = append(r.Content, ContentItem{
				Type: "image_url",
				ImageURL: &MediaURL{
					URL: imgURL,
				},
				Role: role,
			})
		}
	}

	metadata := seedanceMetadataFromRequest(req)
	// Seedance 2.0 高级参数白名单过滤：新建 map 仅保留白名单字段，
	// 不就地修改 req.Metadata，避免影响 EstimateBilling 对 video_url 折扣的判断。
	if metadata != nil {
		filtered := make(map[string]any, len(metadata))
		for k, v := range metadata {
			if _, ok := SeedanceAdvancedMetadataAllowlist[k]; ok {
				filtered[k] = v
			} else if common.DebugEnabled {
				common.SysLog(fmt.Sprintf("seedance: drop non-allowlisted metadata key: %s", k))
			}
		}
		metadata = filtered
	}
	if err := taskcommon.UnmarshalMetadata(metadata, &r); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata failed")
	}

	if sec, _ := strconv.Atoi(req.Seconds); sec > 0 {
		r.Duration = lo.ToPtr(dto.IntValue(sec))
	}

	// SD2.0 多模态支持：若 content 已有 text 项（多模态场景），保留不动；
	// 否则追加顶层 prompt（纯文生/图生兼容）
	hasText := lo.ContainsBy(r.Content, func(c ContentItem) bool { return c.Type == "text" })
	if !hasText && req.Prompt != "" {
		r.Content = append(r.Content, ContentItem{
			Type: "text",
			Text: req.Prompt,
		})
	}
	normalizeSeedanceRequestPayload(&r)

	return &r, nil
}

func seedanceMetadataFromRequest(req *relaycommon.TaskSubmitReq) map[string]interface{} {
	if req == nil {
		return nil
	}
	metadata := make(map[string]interface{})
	for k, v := range req.Metadata {
		if _, ok := SeedanceAdvancedMetadataAllowlist[k]; ok {
			metadata[k] = v
		} else if common.DebugEnabled {
			common.SysLog(fmt.Sprintf("seedance: drop non-allowlisted metadata key: %s", k))
		}
	}

	putSeedanceMetadataIfMissing(metadata, "content", req.Content)
	putSeedanceMetadataIfMissing(metadata, "callback_url", req.CallbackURL)
	putSeedanceMetadataIfMissing(metadata, "return_last_frame", req.ReturnLastFrame)
	putSeedanceMetadataIfMissing(metadata, "service_tier", req.ServiceTier)
	putSeedanceMetadataIfMissing(metadata, "execution_expires_after", req.ExecutionExpiresAfter)
	putSeedanceMetadataIfMissing(metadata, "generate_audio", req.GenerateAudio)
	putSeedanceMetadataIfMissing(metadata, "draft", req.Draft)
	putSeedanceMetadataIfMissing(metadata, "tools", req.Tools)
	putSeedanceMetadataIfMissing(metadata, "resolution", req.Resolution)
	putSeedanceMetadataIfMissing(metadata, "ratio", req.Ratio)
	if req.Duration != 0 {
		putSeedanceMetadataIfMissing(metadata, "duration", req.Duration)
	}
	putSeedanceMetadataIfMissing(metadata, "frames", req.Frames)
	putSeedanceMetadataIfMissing(metadata, "seed", req.Seed)
	putSeedanceMetadataIfMissing(metadata, "camera_fixed", req.CameraFixed)
	putSeedanceMetadataIfMissing(metadata, "watermark", req.Watermark)
	putSeedanceMetadataIfMissing(metadata, "extra_body", req.ExtraBody)

	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func putSeedanceMetadataIfMissing(metadata map[string]interface{}, key string, value interface{}) {
	if metadata == nil || seedanceMetadataValueEmpty(value) {
		return
	}
	if _, exists := metadata[key]; exists {
		return
	}
	metadata[key] = value
}

func seedanceMetadataValueEmpty(value interface{}) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(v) == ""
	case []map[string]interface{}:
		return len(v) == 0
	default:
		return false
	}
}

func normalizeSeedanceRequestPayload(r *requestPayload) {
	if r == nil {
		return
	}
	r.Ratio = normalizeSeedanceEnum(r.Ratio)
	r.Resolution = normalizeSeedanceEnum(r.Resolution)
	r.ServiceTier = strings.TrimSpace(r.ServiceTier)
	if !IsSeedance25Model(r.Model) {
		r.ExtraBody = nil
	} else if r.ExtraBody != nil {
		r.ExtraBody.OutputFormat = normalizeSeedanceEnum(r.ExtraBody.OutputFormat)
		if r.ExtraBody.OutputFormat == "" {
			r.ExtraBody = nil
		}
	}
	for i := range r.Content {
		r.Content[i].Type = normalizeSeedanceEnum(seedanceContentType(r.Content[i]))
		r.Content[i].Role = normalizeSeedanceEnum(r.Content[i].Role)
		if r.Content[i].ImageURL != nil {
			r.Content[i].ImageURL.URL = strings.TrimSpace(r.Content[i].ImageURL.URL)
		}
		if r.Content[i].VideoURL != nil {
			r.Content[i].VideoURL.URL = strings.TrimSpace(r.Content[i].VideoURL.URL)
		}
		if r.Content[i].AudioURL != nil {
			r.Content[i].AudioURL.URL = strings.TrimSpace(r.Content[i].AudioURL.URL)
		}
	}
}

func normalizeSeedanceEnum(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
}

func seedanceContentType(item ContentItem) string {
	if strings.TrimSpace(item.Type) != "" {
		return item.Type
	}
	if item.ImageURL != nil {
		return "image_url"
	}
	if item.VideoURL != nil {
		return "video_url"
	}
	if item.AudioURL != nil {
		return "audio_url"
	}
	if strings.TrimSpace(item.Text) != "" {
		return "text"
	}
	return ""
}

func validateSeedanceRequestPayload(r *requestPayload) error {
	return validateSeedanceRequestPayloadForModel(seedance20Model, r)
}

func validateSeedanceRequestPayloadForModel(modelName string, r *requestPayload) error {
	if r == nil {
		return fmt.Errorf("request payload is empty")
	}
	seedance25 := IsSeedance25Model(modelName)
	if !seedance25 && r.Ratio != "" && !seedanceAllowedRatio(r.Ratio) {
		return fmt.Errorf("ratio must be one of 16:9, 4:3, 1:1, 3:4, 9:16, 21:9, adaptive")
	}
	if !seedance25 && r.Resolution != "" && !seedanceAllowedResolution(r.Resolution) {
		return fmt.Errorf("resolution must be one of 480p, 720p, 1080p, 4k")
	}
	if !seedance25 && r.Duration != nil {
		duration := int(*r.Duration)
		if duration != -1 && (duration < 4 || duration > 15) {
			return fmt.Errorf("duration must be -1 or between 4 and 15 seconds")
		}
	}
	if len(r.Content) == 0 {
		return fmt.Errorf("content or prompt is required")
	}

	imageCount := 0
	videoCount := 0
	audioCount := 0
	firstFrameCount := 0
	lastFrameCount := 0
	referenceImageCount := 0

	for i, item := range r.Content {
		switch item.Type {
		case "text":
			if strings.TrimSpace(item.Text) == "" {
				return fmt.Errorf("content[%d].text is required", i)
			}
			if strings.TrimSpace(item.Role) != "" {
				return fmt.Errorf("content[%d].role is not supported for text", i)
			}
		case "image_url":
			if item.ImageURL == nil || item.ImageURL.URL == "" {
				return fmt.Errorf("content[%d].image_url.url is required", i)
			}
			if !seedanceAllowedImageRole(item.Role) {
				return fmt.Errorf("content[%d].role for image_url must be first_frame, last_frame, or reference_image", i)
			}
			imageCount++
			switch item.Role {
			case "first_frame":
				firstFrameCount++
			case "last_frame":
				lastFrameCount++
			case "reference_image":
				referenceImageCount++
			}
		case "video_url":
			if item.VideoURL == nil || item.VideoURL.URL == "" {
				return fmt.Errorf("content[%d].video_url.url is required", i)
			}
			if item.Role != "" && item.Role != "reference_video" {
				return fmt.Errorf("content[%d].role for video_url must be reference_video", i)
			}
			videoCount++
		case "audio_url":
			if item.AudioURL == nil || item.AudioURL.URL == "" {
				return fmt.Errorf("content[%d].audio_url.url is required", i)
			}
			if item.Role != "" && item.Role != "reference_audio" {
				return fmt.Errorf("content[%d].role for audio_url must be reference_audio", i)
			}
			audioCount++
		default:
			return fmt.Errorf("content[%d].type must be text, image_url, video_url, or audio_url", i)
		}
	}

	if seedance25 {
		return nil
	}

	if imageCount > 9 {
		return fmt.Errorf("reference images must not exceed 9")
	}
	if videoCount > 3 {
		return fmt.Errorf("reference videos must not exceed 3")
	}
	if audioCount > 3 {
		return fmt.Errorf("reference audios must not exceed 3")
	}
	if audioCount > 0 && imageCount+videoCount == 0 {
		return fmt.Errorf("audio reference cannot be used alone; include at least one image or video")
	}
	if firstFrameCount > 1 {
		return fmt.Errorf("first_frame image must not exceed 1")
	}
	if lastFrameCount > 1 {
		return fmt.Errorf("last_frame image must not exceed 1")
	}
	if lastFrameCount > 0 && firstFrameCount == 0 {
		return fmt.Errorf("last_frame requires a first_frame image")
	}
	if (firstFrameCount > 0 || lastFrameCount > 0) && (referenceImageCount > 0 || videoCount > 0 || audioCount > 0) {
		return fmt.Errorf("first-frame/last-frame mode cannot be mixed with reference image/video/audio mode")
	}

	return nil
}

func seedanceAllowedRatio(ratio string) bool {
	switch ratio {
	case "16:9", "4:3", "1:1", "3:4", "9:16", "21:9", "adaptive":
		return true
	default:
		return false
	}
}

func seedanceAllowedResolution(resolution string) bool {
	switch resolution {
	case "480p", "720p", "1080p", "4k":
		return true
	default:
		return false
	}
}

func seedanceAllowedImageRole(role string) bool {
	switch role {
	case "", "first_frame", "last_frame", "reference_image":
		return true
	default:
		return false
	}
}

func (a *TaskAdaptor) applyMaterialAssets(c *gin.Context, info *relaycommon.RelayInfo, body *requestPayload) error {
	if body == nil || info == nil || info.ChannelMeta == nil {
		return nil
	}
	settings := info.ChannelOtherSettings
	ctx := c.Request.Context()
	for i := range body.Content {
		item := &body.Content[i]
		if item.ImageURL != nil && item.ImageURL.URL != "" {
			if asset, ok, err := resolveOpenWhiteAssetURL(item.ImageURL.URL, info.ChannelId); ok || err != nil {
				if err != nil {
					return errors.Wrap(err, "seedance open-white image asset resolve failed")
				}
				item.ImageURL.URL = asset
				continue
			}
			if !settings.SD2MaterialEnabled {
				continue
			}
			asset, err := service.EnsureSD2MaterialAsset(ctx, info.ChannelId, item.ImageURL.URL, "image", settings, info.ChannelSetting.Proxy)
			if err != nil {
				return errors.Wrap(err, "sd2 material image asset failed")
			}
			item.ImageURL.URL = asset
		}
		if item.VideoURL != nil && item.VideoURL.URL != "" {
			if asset, ok, err := resolveOpenWhiteAssetURL(item.VideoURL.URL, info.ChannelId); ok || err != nil {
				if err != nil {
					return errors.Wrap(err, "seedance open-white video asset resolve failed")
				}
				item.VideoURL.URL = asset
				continue
			}
			if !settings.SD2MaterialEnabled {
				continue
			}
			asset, err := service.EnsureSD2MaterialAsset(ctx, info.ChannelId, item.VideoURL.URL, "video", settings, info.ChannelSetting.Proxy)
			if err != nil {
				return errors.Wrap(err, "sd2 material video asset failed")
			}
			item.VideoURL.URL = asset
		}
		if item.AudioURL != nil && item.AudioURL.URL != "" {
			if asset, ok, err := resolveOpenWhiteAssetURL(item.AudioURL.URL, info.ChannelId); ok || err != nil {
				if err != nil {
					return errors.Wrap(err, "seedance open-white audio asset resolve failed")
				}
				item.AudioURL.URL = asset
				continue
			}
			if !settings.SD2MaterialEnabled {
				continue
			}
			asset, err := service.EnsureSD2MaterialAsset(ctx, info.ChannelId, item.AudioURL.URL, "audio", settings, info.ChannelSetting.Proxy)
			if err != nil {
				return errors.Wrap(err, "sd2 material audio asset failed")
			}
			item.AudioURL.URL = asset
		}
	}
	return nil
}

func resolveOpenWhiteAssetURL(rawURL string, channelID int) (string, bool, error) {
	openWhiteID, ok := service.NormalizeSeedanceOpenWhiteID(rawURL)
	if !ok {
		return "", false, nil
	}
	asset, err := service.ResolveSeedanceOpenWhiteAssetForChannel(openWhiteID, channelID)
	if err != nil {
		return "", true, err
	}
	return asset, true, nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	resTask, err := decodeResponseTask(respBody)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{
		Code: 0,
	}

	// Map Doubao status to internal status
	switch resTask.Status {
	case "pending", "queued":
		taskResult.Status = model.TaskStatusQueued
		taskResult.Progress = "10%"
	case "processing", "running":
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "50%"
	case "succeeded":
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = resTask.resultURL()
		// 解析 usage 信息用于按倍率计费
		taskResult.CompletionTokens = resTask.Usage.CompletionTokens
		taskResult.TotalTokens = resTask.Usage.TotalTokens
		if taskResult.TotalTokens == 0 {
			taskResult.TotalTokens = resTask.Usage.CompletionTokens
		}
	case "failed", "cancelled", "canceled", "expired":
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		taskResult.Reason = resTask.Error.Message
		if taskResult.Reason == "" {
			taskResult.Reason = resTask.Status
		}
	default:
		// Unknown status, treat as processing
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	dResp, err := decodeResponseTask(originTask.Data)
	if err != nil {
		return nil, errors.Wrap(err, "unmarshal doubao task data failed")
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	resultURL := dResp.resultURL()
	if resultURL == "" {
		resultURL = originTask.PrivateData.ResultURL
	}
	if resultURL != "" {
		openAIVideo.SetMetadata("url", resultURL)
	}
	if dResp.Content.VideoURL != "" {
		openAIVideo.SetMetadata("video_url", dResp.Content.VideoURL)
	}
	if dResp.Content.FileURL != "" {
		openAIVideo.SetMetadata("file_url", dResp.Content.FileURL)
	}
	if dResp.Content.LastFrameURL != "" {
		openAIVideo.SetMetadata("last_frame_url", dResp.Content.LastFrameURL)
	}
	// 回写上游返回的关键结果字段，便于排查与展示（仅在有值时写入）。
	if dResp.Seed != 0 {
		openAIVideo.SetMetadata("seed", dResp.Seed)
	}
	if dResp.Resolution != "" {
		openAIVideo.SetMetadata("resolution", dResp.Resolution)
	}
	if dResp.Duration != 0 {
		openAIVideo.SetMetadata("duration", dResp.Duration)
	}
	if dResp.Frames != 0 {
		openAIVideo.SetMetadata("frames", dResp.Frames)
	}
	if dResp.Ratio != "" {
		openAIVideo.SetMetadata("ratio", dResp.Ratio)
	}
	if dResp.FramesPerSecond != 0 {
		openAIVideo.SetMetadata("framespersecond", dResp.FramesPerSecond)
	}
	if dResp.ServiceTier != "" {
		openAIVideo.SetMetadata("service_tier", dResp.ServiceTier)
	}
	if IsSeedance25Model(originTask.Properties.OriginModelName) {
		setSeedance25ResponseMetadata(openAIVideo, dResp)
	}
	if len(dResp.Tools) > 0 {
		openAIVideo.SetMetadata("tools", dResp.Tools)
	}
	if usage := dResp.usageMetadata(); usage != nil {
		openAIVideo.Usage = usage
		openAIVideo.SetMetadata("usage", usage)
		openAIVideo.SetMetadata("web_search", dResp.Usage.ToolUsage.WebSearch)
	}
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt
	openAIVideo.Model = originTask.Properties.OriginModelName

	if dResp.Status == "failed" {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: dResp.Error.Message,
			Code:    dResp.Error.Code,
		}
	}

	return common.Marshal(openAIVideo)
}

func setSeedance25ResponseMetadata(video *dto.OpenAIVideo, task responseTask) {
	if video == nil {
		return
	}
	if task.FileFormat != nil {
		video.SetMetadata("fileformat", *task.FileFormat)
	}
	if task.GenerateAudio != nil {
		video.SetMetadata("generate_audio", *task.GenerateAudio)
	}
	if task.RevisedPrompt != nil {
		video.SetMetadata("revised_prompt", *task.RevisedPrompt)
	}
	if task.Draft != nil {
		video.SetMetadata("draft", *task.Draft)
	}
	if task.DraftTaskID != nil {
		video.SetMetadata("draft_task_id", *task.DraftTaskID)
	}
	if task.SubdivisionLevel != nil {
		video.SetMetadata("subdivisionlevel", *task.SubdivisionLevel)
	}
	if task.ExecutionExpiresAfter != nil {
		video.SetMetadata("execution_expires_after", *task.ExecutionExpiresAfter)
	}
	if task.RequestID != nil {
		video.SetMetadata("_request_id", *task.RequestID)
	}
}

func (p responsePayload) taskID() string {
	if p.ID != "" {
		return p.ID
	}
	if p.TaskID != "" {
		return p.TaskID
	}
	if p.Data == nil {
		return ""
	}
	if p.Data.ID != "" {
		return p.Data.ID
	}
	return p.Data.TaskID
}

func (r responseTask) resultURL() string {
	if r.Content.VideoURL != "" {
		return r.Content.VideoURL
	}
	return r.Content.FileURL
}

func (r responseTask) hasTaskPayload() bool {
	return r.ID != "" ||
		r.Model != "" ||
		r.Status != "" ||
		r.Content.VideoURL != "" ||
		r.Content.FileURL != "" ||
		r.Content.LastFrameURL != ""
}

func (r responseTask) usageMetadata() map[string]any {
	if r.Usage.CompletionTokens == 0 && r.Usage.TotalTokens == 0 && r.Usage.ToolUsage.WebSearch == 0 {
		return nil
	}
	usage := map[string]any{
		"completion_tokens": r.Usage.CompletionTokens,
		"tool_usage": map[string]any{
			"web_search": r.Usage.ToolUsage.WebSearch,
		},
	}
	if r.Usage.TotalTokens != 0 {
		usage["total_tokens"] = r.Usage.TotalTokens
	}
	return usage
}

func decodeResponseTask(respBody []byte) (responseTask, error) {
	var direct responseTask
	if err := common.Unmarshal(respBody, &direct); err != nil {
		return responseTask{}, err
	}
	if direct.hasTaskPayload() {
		return direct, nil
	}

	var wrapped struct {
		Data responseTask `json:"data"`
	}
	if err := common.Unmarshal(respBody, &wrapped); err != nil {
		return responseTask{}, err
	}
	if wrapped.Data.hasTaskPayload() {
		return wrapped.Data, nil
	}
	return direct, nil
}

func isXiongdaSeedanceBaseURL(baseURL string) bool {
	normalized := strings.ToLower(strings.TrimRight(baseURL, "/"))
	return strings.Contains(normalized, "118.196.64.1") ||
		strings.Contains(normalized, "ai.zhuque.me") ||
		strings.Contains(normalized, "/api/v1/doubao")
}

func buildXiongdaSeedanceURL(baseURL, action string) string {
	base := strings.TrimRight(baseURL, "/")
	base = strings.TrimSuffix(base, "/"+xiongdaSeedanceTaskCreate)
	base = strings.TrimSuffix(base, "/"+xiongdaSeedanceTaskGetResult)
	base = strings.TrimRight(base, "/")

	lowerBase := strings.ToLower(base)
	if strings.HasSuffix(lowerBase, "/api/v1/doubao") {
		return base + "/" + action
	}
	if strings.HasSuffix(lowerBase, "/api/v1") {
		return base + "/doubao/" + action
	}
	return base + "/api/v1/doubao/" + action
}
