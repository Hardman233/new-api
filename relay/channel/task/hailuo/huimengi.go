package hailuo

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

const (
	HuimengiDefaultBaseURL = "https://api.huimengi.com"
	HuimengiCreateEndpoint = "/api/v1/tasks"
	HuimengiQueryEndpoint  = "/api/v1/tasks"
	HuimengiH3Model        = "minimax-h3"

	// Huimengi publishes H3 pricing as 0.45 per 768P second and 0.72 per 2K
	// second. BillingUnits uses 768P-equivalent seconds so the platform model
	// price and group ratio remain the source of user-facing billing.
	huimengiH3BasePrice = 0.45
	huimengiH3TwoKRatio = 0.72 / huimengiH3BasePrice
	huimengiH3ImageFee  = 0.18 / huimengiH3BasePrice
)

type HuimengiH3Request struct {
	Model          string           `json:"model"`
	Params         HuimengiH3Params `json:"params"`
	WebhookURL     string           `json:"webhook_url,omitempty"`
	IdempotencyKey string           `json:"idempotency_key,omitempty"`
	Content        []H3ContentItem  `json:"content,omitempty"`
}

type HuimengiH3Params struct {
	Prompt          string   `json:"prompt,omitempty"`
	Duration        int      `json:"duration,omitempty"`
	Resolution      string   `json:"resolution,omitempty"`
	Ratio           string   `json:"ratio,omitempty"`
	ImageURL        string   `json:"image_url,omitempty"`
	FirstFrameImage string   `json:"first_frame_image,omitempty"`
	LastFrameImage  string   `json:"last_frame_image,omitempty"`
	ReferenceImages []string `json:"reference_images,omitempty"`
	ReferenceVideos []string `json:"reference_videos,omitempty"`
	ReferenceAudios []string `json:"reference_audios,omitempty"`
}

type HuimengiH3SubmitResponse struct {
	TaskID string `json:"task_id,omitempty"`
	ID     string `json:"id,omitempty"`
	Status string `json:"status,omitempty"`
}

type HuimengiH3TaskResponse struct {
	ID           string           `json:"id,omitempty"`
	TaskID       string           `json:"task_id,omitempty"`
	Model        string           `json:"model,omitempty"`
	Status       string           `json:"status,omitempty"`
	Result       HuimengiH3Result `json:"result,omitempty"`
	Cost         any              `json:"cost,omitempty"`
	ErrorMessage string           `json:"error_message,omitempty"`
	Error        *HuimengiH3Error `json:"error,omitempty"`
	CreatedAt    string           `json:"created_at,omitempty"`
	CompletedAt  string           `json:"completed_at,omitempty"`
}

type HuimengiH3Result struct {
	VideoURL   string `json:"video_url,omitempty"`
	Duration   int    `json:"duration,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Ratio      string `json:"ratio,omitempty"`
}

type HuimengiH3Error struct {
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

func isHuimengiBaseURL(raw string) bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return false
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return strings.Contains(strings.ToLower(raw), "api.huimengi.com")
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "api.huimengi.com" || strings.HasSuffix(host, ".huimengi.com")
}

func (a *TaskAdaptor) isHuimengiH3(info *relaycommon.RelayInfo) bool {
	if info == nil || !isH3Model(h3ModelFromInfo(info)) {
		return false
	}
	if a != nil && a.huimengi {
		return true
	}
	return isHuimengiBaseURL(info.ChannelBaseUrl)
}

func huimengiH3CreateURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = HuimengiDefaultBaseURL
	}
	return baseURL + HuimengiCreateEndpoint
}

func huimengiH3QueryURL(baseURL, taskID string) string {
	return strings.TrimRight(strings.TrimSpace(baseURL), "/") + HuimengiQueryEndpoint + "/" + url.PathEscape(taskID)
}

func buildHuimengiH3Request(req *relaycommon.TaskSubmitReq) (*HuimengiH3Request, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	body := &HuimengiH3Request{}
	if err := req.UnmarshalMetadata(body); err != nil {
		return nil, err
	}
	body.Model = HuimengiH3Model
	if strings.TrimSpace(req.CallbackURL) != "" {
		body.WebhookURL = strings.TrimSpace(req.CallbackURL)
	}

	params := &body.Params
	if strings.TrimSpace(req.Prompt) != "" {
		params.Prompt = strings.TrimSpace(req.Prompt)
	}
	if req.Duration > 0 {
		params.Duration = req.Duration
	}
	if resolution := firstNonEmpty(req.Resolution, req.Size); resolution != "" {
		params.Resolution = normalizeHuimengiResolution(resolution)
	}
	if ratio := firstNonEmpty(req.Ratio, req.AspectRatio); ratio != "" {
		params.Ratio = strings.ToLower(strings.TrimSpace(ratio))
	}

	if len(req.Content) > 0 {
		content, err := decodeH3Content(req.Content)
		if err != nil {
			return nil, fmt.Errorf("invalid content: %w", err)
		}
		body.Content = append(body.Content, content...)
	}
	if !h3ContentHasText(body.Content) && params.Prompt == "" {
		if text := firstContentText(body.Content); text != "" {
			params.Prompt = text
		}
	}
	mapHuimengiContent(params, body.Content)
	mapHuimengiTopLevelMedia(params, req)
	body.Content = nil

	if params.Duration == 0 {
		params.Duration = h3DefaultDuration
	}
	if strings.TrimSpace(params.Resolution) == "" {
		params.Resolution = Resolution2K
	}
	params.Resolution = normalizeHuimengiResolution(params.Resolution)
	if strings.TrimSpace(params.Ratio) == "" {
		params.Ratio = h3DefaultRatio
	}
	if huimengiHasMedia(*params) {
		params.Ratio = "adaptive"
	}
	if err := validateHuimengiH3Request(body); err != nil {
		return nil, err
	}
	return body, nil
}

func firstContentText(content []H3ContentItem) string {
	for _, item := range content {
		if strings.EqualFold(strings.TrimSpace(item.Type), "text") && strings.TrimSpace(item.Text) != "" {
			return strings.TrimSpace(item.Text)
		}
	}
	return ""
}

func mapHuimengiContent(params *HuimengiH3Params, content []H3ContentItem) {
	imageCount := 0
	for index := range content {
		item := &content[index]
		item.Type = strings.ToLower(strings.TrimSpace(item.Type))
		item.Role = strings.ToLower(strings.TrimSpace(item.Role))
		if item.Type == "text" && params.Prompt == "" {
			params.Prompt = strings.TrimSpace(item.Text)
		}
		if item.Type == "image_url" {
			imageCount++
		}
	}
	for index := range content {
		item := content[index]
		if item.Type == "image_url" && item.Role == "" && imageCount == 1 {
			item.Role = "first_frame"
		}
		var mediaURL string
		switch item.Type {
		case "image_url":
			if item.ImageURL != nil {
				mediaURL = strings.TrimSpace(item.ImageURL.URL)
			}
			switch item.Role {
			case "first_frame":
				if params.FirstFrameImage == "" {
					params.FirstFrameImage = mediaURL
				}
			case "last_frame":
				if params.LastFrameImage == "" {
					params.LastFrameImage = mediaURL
				}
			case "reference_image":
				appendUniqueString(&params.ReferenceImages, mediaURL)
			}
		case "video_url":
			if item.VideoURL != nil {
				mediaURL = strings.TrimSpace(item.VideoURL.URL)
			}
			if item.Role == "reference_video" {
				appendUniqueString(&params.ReferenceVideos, mediaURL)
			}
		case "audio_url":
			if item.AudioURL != nil {
				mediaURL = strings.TrimSpace(item.AudioURL.URL)
			}
			if item.Role == "reference_audio" {
				appendUniqueString(&params.ReferenceAudios, mediaURL)
			}
		}
	}
}

func mapHuimengiTopLevelMedia(params *HuimengiH3Params, req *relaycommon.TaskSubmitReq) {
	images := append([]string(nil), req.Images...)
	if image := firstNonEmpty(req.Image, req.ImageURL); image != "" {
		images = append(images, image)
	}
	for _, media := range req.ReferenceImages {
		if image := media.SourceURL(); image != "" {
			appendUniqueString(&params.ReferenceImages, image)
		}
	}
	if len(images) == 1 && params.FirstFrameImage == "" && params.LastFrameImage == "" {
		params.ImageURL = strings.TrimSpace(images[0])
	}
	if len(images) >= 2 && params.FirstFrameImage == "" && params.LastFrameImage == "" {
		params.FirstFrameImage = strings.TrimSpace(images[0])
		params.LastFrameImage = strings.TrimSpace(images[1])
	}
	if len(images) > 2 {
		for _, image := range images[2:] {
			appendUniqueString(&params.ReferenceImages, image)
		}
	}
}

func appendUniqueString(values *[]string, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	for _, existing := range *values {
		if existing == value {
			return
		}
	}
	*values = append(*values, value)
}

func huimengiHasMedia(params HuimengiH3Params) bool {
	return strings.TrimSpace(params.ImageURL) != "" ||
		strings.TrimSpace(params.FirstFrameImage) != "" ||
		strings.TrimSpace(params.LastFrameImage) != "" ||
		len(params.ReferenceImages) > 0 || len(params.ReferenceVideos) > 0 || len(params.ReferenceAudios) > 0
}

func validateHuimengiH3Request(body *HuimengiH3Request) error {
	if body == nil || !strings.EqualFold(strings.TrimSpace(body.Model), HuimengiH3Model) {
		return fmt.Errorf("Huimengi model must be %s", HuimengiH3Model)
	}
	params := body.Params
	if strings.TrimSpace(params.Prompt) == "" {
		return fmt.Errorf("params.prompt is required")
	}
	if len([]rune(params.Prompt)) > 7000 {
		return fmt.Errorf("params.prompt exceeds 7000 characters")
	}
	if params.Duration < 4 || params.Duration > 15 {
		return fmt.Errorf("params.duration must be an integer from 4 to 15")
	}
	if params.Resolution != Resolution768P && params.Resolution != Resolution2K {
		return fmt.Errorf("params.resolution must be %s or %s", Resolution768P, Resolution2K)
	}
	if _, ok := h3Ratios[params.Ratio]; !ok {
		return fmt.Errorf("unsupported params.ratio %q", params.Ratio)
	}
	if len(params.ReferenceImages) > 9 || len(params.ReferenceVideos) > 3 || len(params.ReferenceAudios) > 3 {
		return fmt.Errorf("reference media exceeds the Huimengi MiniMax H3 limit")
	}
	return nil
}

func normalizeHuimengiResolution(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if strings.Contains(value, "768") {
		return Resolution768P
	}
	if strings.Contains(value, "2K") || strings.Contains(value, "2048") {
		return Resolution2K
	}
	return value
}

func estimateHuimengiH3BillingUnits(body *HuimengiH3Request) float64 {
	if body == nil {
		return 0
	}
	factor := 1.0
	if body.Params.Resolution == Resolution2K {
		factor = huimengiH3TwoKRatio
	}
	imageCount := 0
	if body.Params.ImageURL != "" {
		imageCount++
	}
	if body.Params.FirstFrameImage != "" {
		imageCount++
	}
	if body.Params.LastFrameImage != "" {
		imageCount++
	}
	imageCount += len(body.Params.ReferenceImages)
	excessImages := max(imageCount-5, 0)
	return float64(body.Params.Duration)*factor + float64(excessImages)*huimengiH3ImageFee
}

func parseHuimengiCost(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed
	default:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
		return parsed
	}
}

func huimengiCostToBillingUnits(cost float64) float64 {
	if cost <= 0 {
		return 0
	}
	return cost / huimengiH3BasePrice
}

func isHuimengiTaskResponse(body []byte) bool {
	var response HuimengiH3TaskResponse
	if common.Unmarshal(body, &response) != nil {
		return false
	}
	return strings.TrimSpace(response.ID) != "" ||
		(strings.TrimSpace(response.Model) != "" && strings.TrimSpace(response.Status) != "" && response.Result.VideoURL != "")
}

func parseHuimengiH3TaskResult(body []byte) (*relaycommon.TaskInfo, error) {
	var response HuimengiH3TaskResponse
	if err := common.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("unmarshal Huimengi task result failed: %w", err)
	}
	result := &relaycommon.TaskInfo{
		TaskID:         firstNonEmpty(response.ID, response.TaskID),
		ProviderStatus: strings.ToLower(strings.TrimSpace(response.Status)),
		Duration:       int32(response.Result.Duration),
		BillingUnits:   huimengiCostToBillingUnits(parseHuimengiCost(response.Cost)),
	}
	if result.BillingUnits <= 0 && response.Result.Duration > 0 {
		factor := 1.0
		if normalizeHuimengiResolution(response.Result.Resolution) == Resolution2K {
			factor = huimengiH3TwoKRatio
		}
		result.BillingUnits = float64(response.Result.Duration) * factor
	}

	switch result.ProviderStatus {
	case "pending", "queued":
		result.Status = "QUEUED"
		result.Progress = "20%"
	case "processing", "running":
		result.Status = "IN_PROGRESS"
		result.Progress = "50%"
	case "completed", "succeeded":
		result.Status = "SUCCESS"
		result.Progress = "100%"
		result.Url = strings.TrimSpace(response.Result.VideoURL)
		if result.Url == "" {
			return nil, fmt.Errorf("Huimengi completed task did not contain result.video_url")
		}
	case "failed", "cancelled":
		result.Status = "FAILURE"
		result.Progress = "100%"
		result.Reason = strings.TrimSpace(response.ErrorMessage)
		if response.Error != nil && result.Reason == "" {
			result.ErrorCode = strings.TrimSpace(response.Error.Code)
			result.Reason = strings.TrimSpace(response.Error.Message)
		}
		if result.Reason == "" {
			result.Reason = "Huimengi task " + result.ProviderStatus
		}
	default:
		result.Status = "IN_PROGRESS"
		result.Progress = "30%"
	}
	return result, nil
}

func isHuimengiTaskData(body []byte) bool {
	return isHuimengiTaskResponse(body)
}
