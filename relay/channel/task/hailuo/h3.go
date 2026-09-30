package hailuo

import (
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

const (
	h3DefaultDuration       = 5
	h3DefaultRatio          = "16:9"
	h3DefaultResolution     = Resolution768P
	h3Resolution2KFactor    = 1.6
	h3ExcessImageUnitFactor = 0.4
)

var h3Ratios = map[string]struct{}{
	"adaptive": {},
	"21:9":     {},
	"16:9":     {},
	"4:3":      {},
	"1:1":      {},
	"3:4":      {},
	"9:16":     {},
}

func isH3Model(modelName string) bool {
	modelName = strings.TrimSpace(modelName)
	return strings.EqualFold(modelName, H3Model) || strings.EqualFold(modelName, HuimengiH3Model)
}

func h3ModelFromInfo(info *relaycommon.RelayInfo) string {
	if info == nil {
		return ""
	}
	if strings.TrimSpace(info.UpstreamModelName) != "" {
		return strings.TrimSpace(info.UpstreamModelName)
	}
	return strings.TrimSpace(info.OriginModelName)
}

func h3BaseURL(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || strings.EqualFold(baseURL, "https://api.minimax.chat") {
		return H3DefaultBaseURL
	}
	return baseURL
}

func buildH3VideoRequest(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*H3VideoRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("request is required")
	}

	body := &H3VideoRequest{}
	if err := req.UnmarshalMetadata(body); err != nil {
		return nil, err
	}
	body.Model = H3Model

	if len(req.Content) > 0 {
		content, err := decodeH3Content(req.Content)
		if err != nil {
			return nil, fmt.Errorf("invalid content: %w", err)
		}
		body.Content = content
	}
	if !h3ContentHasText(body.Content) && strings.TrimSpace(req.Prompt) != "" {
		body.Content = append([]H3ContentItem{{Type: "text", Text: strings.TrimSpace(req.Prompt)}}, body.Content...)
	}
	appendH3SimpleImages(body, req)

	if req.Duration > 0 {
		body.Duration = req.Duration
	}
	if resolution := firstNonEmpty(req.Resolution, req.Size); resolution != "" {
		body.Resolution = normalizeH3Resolution(resolution)
	}
	if ratio := firstNonEmpty(req.Ratio, req.AspectRatio); ratio != "" {
		body.Ratio = strings.ToLower(ratio)
	}
	if strings.TrimSpace(req.CallbackURL) != "" {
		body.CallbackURL = strings.TrimSpace(req.CallbackURL)
	}
	if watermark, ok := boolFromAny(req.AIGCWatermark); ok {
		body.AIGCWatermark = &watermark
	}
	if watermark, ok := boolFromAny(req.Watermark); ok {
		body.AIGCWatermark = &watermark
	}

	if body.Duration == 0 {
		body.Duration = h3DefaultDuration
	}
	body.Resolution = normalizeH3Resolution(body.Resolution)
	if body.Resolution == "" {
		body.Resolution = h3DefaultResolution
	}
	normalizeH3Content(body.Content)
	normalizeH3Ratio(body)

	if err := validateH3VideoRequest(body); err != nil {
		return nil, err
	}
	return body, nil
}

func decodeH3Content(raw any) ([]H3ContentItem, error) {
	data, err := common.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var content []H3ContentItem
	if err := common.Unmarshal(data, &content); err != nil {
		return nil, err
	}
	return content, nil
}

func appendH3SimpleImages(body *H3VideoRequest, req *relaycommon.TaskSubmitReq) {
	images := append([]string(nil), req.Images...)
	if strings.TrimSpace(req.Image) != "" && !contains(images, strings.TrimSpace(req.Image)) {
		images = append(images, strings.TrimSpace(req.Image))
	}
	if strings.TrimSpace(req.ImageURL) != "" && !contains(images, strings.TrimSpace(req.ImageURL)) {
		images = append(images, strings.TrimSpace(req.ImageURL))
	}
	if len(images) == 0 {
		return
	}

	referenceMode := strings.Contains(strings.ToLower(req.Mode), "reference") || h3ContentHasReference(body.Content)
	for index, image := range images {
		image = strings.TrimSpace(image)
		if image == "" {
			continue
		}
		role := "reference_image"
		if !referenceMode && len(images) <= 2 {
			role = "first_frame"
			if index == 1 {
				role = "last_frame"
			}
		}
		body.Content = append(body.Content, H3ContentItem{
			Type:     "image_url",
			ImageURL: &H3MediaURL{URL: image},
			Role:     role,
		})
	}
}

func normalizeH3Content(content []H3ContentItem) {
	imageCount := 0
	for index := range content {
		item := &content[index]
		item.Type = strings.ToLower(strings.TrimSpace(item.Type))
		item.Role = strings.ToLower(strings.TrimSpace(item.Role))
		item.Text = strings.TrimSpace(item.Text)
		if item.Type == "image_url" {
			imageCount++
		}
	}
	if imageCount != 1 {
		return
	}
	for index := range content {
		if content[index].Type == "image_url" && content[index].Role == "" {
			content[index].Role = "first_frame"
		}
	}
}

func normalizeH3Ratio(body *H3VideoRequest) {
	hasFrame := false
	hasReference := false
	for _, item := range body.Content {
		switch item.Role {
		case "first_frame", "last_frame":
			hasFrame = true
		case "reference_image", "reference_video", "reference_audio":
			hasReference = true
		}
	}
	if hasFrame {
		body.Ratio = "adaptive"
		return
	}
	if strings.TrimSpace(body.Ratio) == "" {
		if hasReference {
			body.Ratio = "adaptive"
		} else {
			body.Ratio = h3DefaultRatio
		}
	}
}

func validateH3VideoRequest(body *H3VideoRequest) error {
	if !isH3Model(body.Model) {
		return fmt.Errorf("MiniMax H3 request model must be %s", H3Model)
	}
	if body.Duration < 4 || body.Duration > 15 {
		return fmt.Errorf("duration must be an integer from 4 to 15")
	}
	if body.Resolution != Resolution768P && body.Resolution != Resolution2K {
		return fmt.Errorf("resolution must be %s or %s", Resolution768P, Resolution2K)
	}
	if _, ok := h3Ratios[body.Ratio]; !ok {
		return fmt.Errorf("unsupported ratio %q", body.Ratio)
	}

	textCount := 0
	firstFrames := 0
	lastFrames := 0
	referenceImages := 0
	referenceVideos := 0
	referenceAudios := 0
	for index, item := range body.Content {
		switch item.Type {
		case "text":
			textCount++
			if item.Text == "" {
				return fmt.Errorf("content[%d].text is required", index)
			}
			if len([]rune(item.Text)) > 7000 {
				return fmt.Errorf("content[%d].text exceeds 7000 characters", index)
			}
			if item.Role != "" {
				return fmt.Errorf("content[%d] text cannot have a role", index)
			}
		case "image_url":
			if item.ImageURL == nil || strings.TrimSpace(item.ImageURL.URL) == "" {
				return fmt.Errorf("content[%d].image_url.url is required", index)
			}
			switch item.Role {
			case "first_frame":
				firstFrames++
			case "last_frame":
				lastFrames++
			case "reference_image":
				referenceImages++
			default:
				return fmt.Errorf("content[%d] has invalid image role %q", index, item.Role)
			}
		case "video_url":
			if item.VideoURL == nil || strings.TrimSpace(item.VideoURL.URL) == "" {
				return fmt.Errorf("content[%d].video_url.url is required", index)
			}
			if item.Role != "reference_video" {
				return fmt.Errorf("content[%d] video role must be reference_video", index)
			}
			referenceVideos++
		case "audio_url":
			if item.AudioURL == nil || strings.TrimSpace(item.AudioURL.URL) == "" {
				return fmt.Errorf("content[%d].audio_url.url is required", index)
			}
			if item.Role != "reference_audio" {
				return fmt.Errorf("content[%d] audio role must be reference_audio", index)
			}
			referenceAudios++
		default:
			return fmt.Errorf("content[%d] has unsupported type %q", index, item.Type)
		}
	}

	if textCount == 0 {
		return fmt.Errorf("content must contain a non-empty text item")
	}
	if firstFrames > 1 || lastFrames > 1 {
		return fmt.Errorf("content supports at most one first_frame and one last_frame")
	}
	if referenceImages > 9 || referenceVideos > 3 || referenceAudios > 3 {
		return fmt.Errorf("reference media exceeds the MiniMax H3 count limit")
	}
	hasFrames := firstFrames+lastFrames > 0
	hasReferences := referenceImages+referenceVideos+referenceAudios > 0
	if hasFrames && hasReferences {
		return fmt.Errorf("first/last frames cannot be mixed with reference media")
	}
	if !hasFrames && !hasReferences && body.Ratio == "adaptive" {
		return fmt.Errorf("text-to-video ratio cannot be adaptive")
	}
	return nil
}

func h3ContentHasText(content []H3ContentItem) bool {
	for _, item := range content {
		if strings.EqualFold(strings.TrimSpace(item.Type), "text") && strings.TrimSpace(item.Text) != "" {
			return true
		}
	}
	return false
}

func h3ContentHasReference(content []H3ContentItem) bool {
	for _, item := range content {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(item.Role)), "reference_") {
			return true
		}
	}
	return false
}

func normalizeH3Resolution(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	switch {
	case strings.Contains(value, "2K"), strings.Contains(value, "2048"):
		return Resolution2K
	case strings.Contains(value, "768"):
		return Resolution768P
	default:
		return value
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolFromAny(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		if strings.EqualFold(strings.TrimSpace(typed), "true") {
			return true, true
		}
		if strings.EqualFold(strings.TrimSpace(typed), "false") {
			return false, true
		}
	}
	return false, false
}

func estimateH3BillingUnits(body *H3VideoRequest) float64 {
	if body == nil {
		return 0
	}
	factor := 1.0
	if body.Resolution == Resolution2K {
		factor = h3Resolution2KFactor
	}
	imageCount := 0
	for _, item := range body.Content {
		if item.Type == "image_url" {
			imageCount++
		}
	}
	excessImages := max(imageCount-5, 0)
	return float64(body.Duration)*factor + float64(excessImages)*h3ExcessImageUnitFactor
}

func h3ActualBillingUnits(task H3Task) float64 {
	seconds := task.Usage.TotalSeconds
	if seconds <= 0 {
		seconds = task.Usage.InputSeconds + task.Usage.OutputSeconds
	}
	if seconds <= 0 {
		seconds = task.Duration
	}
	factor := 1.0
	if normalizeH3Resolution(task.Resolution) == Resolution2K {
		factor = h3Resolution2KFactor
	}
	excessImages := max(task.Usage.InputImageCount-5, 0)
	return float64(seconds)*factor + float64(excessImages)*h3ExcessImageUnitFactor
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	if a.isHuimengiH3(info) {
		req, err := relaycommon.GetTaskRequest(c)
		if err != nil {
			return nil
		}
		body, err := buildHuimengiH3Request(&req)
		if err != nil {
			return nil
		}
		units := estimateHuimengiH3BillingUnits(body)
		if units <= 0 {
			return nil
		}
		return map[string]float64{"minimax_h3_billing_units": units}
	}
	if info == nil || !isH3Model(h3ModelFromInfo(info)) || info.TieredBillingSnapshot != nil {
		return nil
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	body, err := buildH3VideoRequest(&req, info)
	if err != nil {
		return nil
	}
	units := estimateH3BillingUnits(body)
	if units <= 0 {
		return nil
	}
	return map[string]float64{"minimax_h3_billing_units": units}
}

func (a *TaskAdaptor) EstimateTieredBillingTokens(c *gin.Context, info *relaycommon.RelayInfo) (billingexpr.TokenParams, bool) {
	if a.isHuimengiH3(info) {
		req, err := relaycommon.GetTaskRequest(c)
		if err != nil {
			return billingexpr.TokenParams{}, false
		}
		body, err := buildHuimengiH3Request(&req)
		if err != nil {
			return billingexpr.TokenParams{}, false
		}
		units := estimateHuimengiH3BillingUnits(body)
		return billingexpr.TokenParams{C: units}, units > 0
	}
	if info == nil || !isH3Model(h3ModelFromInfo(info)) {
		return billingexpr.TokenParams{}, false
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return billingexpr.TokenParams{}, false
	}
	body, err := buildH3VideoRequest(&req, info)
	if err != nil {
		return billingexpr.TokenParams{}, false
	}
	units := estimateH3BillingUnits(body)
	return billingexpr.TokenParams{C: units}, units > 0
}

func (a *TaskAdaptor) AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int {
	if task == nil || taskResult == nil || taskResult.BillingUnits <= 0 || !isH3Model(task.Properties.UpstreamModelName) {
		return 0
	}
	context := task.PrivateData.BillingContext
	if context == nil {
		return 0
	}
	if context.TieredBillingSnapshot != nil {
		request := billingexpr.RequestInput{}
		if context.BillingRequestInput != nil {
			request = *context.BillingRequestInput
		}
		result, err := billingexpr.ComputeTieredQuotaWithRequest(
			context.TieredBillingSnapshot,
			billingexpr.TokenParams{C: taskResult.BillingUnits},
			request,
		)
		if err != nil {
			return 0
		}
		return result.ActualQuotaAfterGroup
	}
	if context.ModelPrice <= 0 || context.GroupRatio <= 0 || common.QuotaPerUnit <= 0 {
		return 0
	}
	return int(math.Round(context.ModelPrice * taskResult.BillingUnits * common.QuotaPerUnit * context.GroupRatio))
}
