package vidu

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const (
	viduQ3ProModel     = "viduq3-pro"
	viduQ3ProFastModel = "viduq3-pro-fast"
	viduQ3MixModel     = "viduq3-mix"
	viduQ3DramaModel   = "viduq3-drama"
	viduQ3AdModel      = "viduq3-ad"
	viduQ3TurboModel   = "viduq3-turbo"
	viduQ3Model        = "viduq3"

	viduModeText      = "text2video"
	viduModeImage     = "img2video"
	viduModeStartEnd  = "start-end2video"
	viduModeReference = "reference2video"

	viduDefaultDuration   = 5
	viduDefaultResolution = "720p"
	viduMaxPayloadLength  = 1048576
)

type requestPayload struct {
	Model             string           `json:"model,omitempty"`
	Mode              string           `json:"mode,omitempty"`
	Action            string           `json:"action,omitempty"`
	Style             string           `json:"style,omitempty"`
	Prompt            string           `json:"prompt,omitempty"`
	Image             string           `json:"image,omitempty"`
	Images            []string         `json:"images,omitempty"`
	Videos            []string         `json:"videos,omitempty"`
	Subjects          []subjectPayload `json:"subjects,omitempty"`
	AutoSubjects      *bool            `json:"auto_subjects,omitempty"`
	Audio             *bool            `json:"audio,omitempty"`
	AudioType         string           `json:"audio_type,omitempty"`
	VoiceID           string           `json:"voice_id,omitempty"`
	IsRec             *bool            `json:"is_rec,omitempty"`
	BGM               *bool            `json:"bgm,omitempty"`
	Duration          *int             `json:"duration,omitempty"`
	Seed              *int             `json:"seed,omitempty"`
	AspectRatio       string           `json:"aspect_ratio,omitempty"`
	Resolution        string           `json:"resolution,omitempty"`
	Size              string           `json:"size,omitempty"`
	MovementAmplitude string           `json:"movement_amplitude,omitempty"`
	Payload           string           `json:"payload,omitempty"`
	OffPeak           *bool            `json:"off_peak,omitempty"`
	Watermark         *bool            `json:"watermark,omitempty"`
	WMPosition        *int             `json:"wm_position,omitempty"`
	WMURL             string           `json:"wm_url,omitempty"`
	MetaData          string           `json:"meta_data,omitempty"`
	CallbackURL       string           `json:"callback_url,omitempty"`
}

type subjectPayload struct {
	Name     string   `json:"name,omitempty"`
	Images   []string `json:"images,omitempty"`
	Videos   []string `json:"videos,omitempty"`
	VoiceID  string   `json:"voice_id,omitempty"`
	ServerID string   `json:"server_id,omitempty"`
}

type responsePayload struct {
	TaskID            string   `json:"task_id"`
	State             string   `json:"state"`
	Model             string   `json:"model"`
	Images            []string `json:"images,omitempty"`
	Prompt            string   `json:"prompt,omitempty"`
	Duration          int      `json:"duration,omitempty"`
	Seed              int      `json:"seed,omitempty"`
	Resolution        string   `json:"resolution,omitempty"`
	BGM               bool     `json:"bgm,omitempty"`
	MovementAmplitude string   `json:"movement_amplitude,omitempty"`
	Payload           string   `json:"payload,omitempty"`
	CreatedAt         string   `json:"created_at,omitempty"`
	Reason            string   `json:"reason,omitempty"`
	Message           string   `json:"message,omitempty"`
	ErrCode           string   `json:"err_code,omitempty"`
}

type taskResultResponse struct {
	State     string                   `json:"state"`
	ErrCode   string                   `json:"err_code"`
	Reason    string                   `json:"reason,omitempty"`
	Message   string                   `json:"message,omitempty"`
	Credits   int                      `json:"credits"`
	Payload   string                   `json:"payload"`
	Creations []map[string]interface{} `json:"creations"`
}

type viduErrorResponse struct {
	Reason   string                 `json:"reason"`
	Message  string                 `json:"message"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	req.FillPromptFromContent()
	normalizeTaskImages(&req)
	c.Set("task_request", req)

	body, err := a.convertToRequestPayload(c, &req, info)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	action := determineViduAction(&req, body)
	info.Action = action
	if err := validateViduRequestPayload(body, action); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}
	body, err := a.convertToRequestPayload(c, &req, info)
	if err != nil {
		return nil, err
	}
	if info != nil && info.UpstreamModelName != "" {
		body.Model = info.UpstreamModelName
	}
	action := ""
	if info != nil {
		action = info.Action
	}
	if action == "" {
		action = determineViduAction(&req, body)
	}
	if err := validateViduRequestPayload(body, action); err != nil {
		return nil, err
	}

	data, err := common.Marshal(buildViduRequestMap(body, action))
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	var path string
	switch info.Action {
	case constant.TaskActionGenerate:
		path = "/" + viduModeImage
	case constant.TaskActionFirstTailGenerate:
		path = "/" + viduModeStartEnd
	case constant.TaskActionReferenceGenerate:
		path = "/" + viduModeReference
	default:
		path = "/" + viduModeText
	}
	return fmt.Sprintf("%s/ent/v2%s", strings.TrimRight(a.baseURL, "/"), path), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+info.ApiKey)
	return nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", responseBody, viduUpstreamTaskError(resp.StatusCode, responseBody)
	}

	var vResp responsePayload
	if err := common.Unmarshal(responseBody, &vResp); err != nil {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("unmarshal vidu response failed: %w; body: %s", err, responseBody), "unmarshal_response_failed", http.StatusInternalServerError)
	}
	if vResp.State == "failed" {
		return "", responseBody, viduResponseTaskError(vResp)
	}
	if strings.TrimSpace(vResp.TaskID) == "" {
		return "", responseBody, service.TaskErrorWrapper(fmt.Errorf("task_id is empty"), "invalid_response", http.StatusBadGateway)
	}
	upstreamTaskID := vResp.TaskID

	if c != nil && (c.GetBool("vidu_official_api") || c.GetBool("vidu_official_compat")) {
		publicTaskID := strings.TrimSpace(vResp.TaskID)
		if info != nil && strings.TrimSpace(info.PublicTaskID) != "" {
			publicTaskID = strings.TrimSpace(info.PublicTaskID)
		}
		out, err := viduBodyWithPublicTaskID(responseBody, publicTaskID, model.TaskStatusSubmitted, 0, 0, "")
		if err != nil {
			return "", nil, service.TaskErrorWrapper(fmt.Errorf("marshal vidu official response failed: %w", err), "marshal_response_failed", http.StatusInternalServerError)
		}
		c.Data(http.StatusOK, "application/json; charset=utf-8", out)
		return upstreamTaskID, responseBody, nil
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName
	if c != nil {
		c.JSON(http.StatusOK, ov)
	}
	return upstreamTaskID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		return nil, fmt.Errorf("invalid task_id")
	}

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/ent/v2/tasks/%s/creations", strings.TrimRight(baseUrl, "/"), taskID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Token "+key)

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
	return []string{
		viduQ3ProModel,
		viduQ3ProFastModel,
		viduQ3MixModel,
		viduQ3DramaModel,
		viduQ3AdModel,
		viduQ3TurboModel,
		viduQ3Model,
		"viduq2",
		"viduq1",
		"vidu2.0",
		"vidu1.5",
	}
}

func (a *TaskAdaptor) GetChannelName() string {
	return "vidu"
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, info *relaycommon.RelayInfo) map[string]float64 {
	if info != nil && info.TieredBillingSnapshot != nil {
		return nil
	}
	params, ok := a.EstimateTieredBillingTokens(c, info)
	if !ok || params.C <= 0 {
		return nil
	}
	return map[string]float64{
		"vidu_q3_estimated_credits": params.C,
	}
}

func (a *TaskAdaptor) EstimateTieredBillingTokens(c *gin.Context, info *relaycommon.RelayInfo) (billingexpr.TokenParams, bool) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return billingexpr.TokenParams{}, false
	}
	body, err := a.convertToRequestPayload(c, &req, info)
	if err != nil || !isViduQ3Model(body.Model) {
		return billingexpr.TokenParams{}, false
	}
	action := info.Action
	if action == "" {
		action = determineViduAction(&req, body)
	}
	credits, ok := EstimateViduQ3Credits(body, action)
	if !ok || credits <= 0 {
		return billingexpr.TokenParams{}, false
	}
	return billingexpr.TokenParams{C: float64(credits)}, true
}

func (a *TaskAdaptor) AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int {
	if task == nil || taskResult == nil || taskResult.TotalTokens <= 0 {
		return 0
	}
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.TieredBillingSnapshot != nil {
		return 0
	}
	estimatedCredits := bc.OtherRatios["vidu_q3_estimated_credits"]
	if estimatedCredits <= 0 {
		return 0
	}
	actualQuota := int(math.Round(float64(task.Quota) * float64(taskResult.TotalTokens) / estimatedCredits))
	if actualQuota <= 0 {
		return 0
	}
	return actualQuota
}

func (a *TaskAdaptor) convertToRequestPayload(c *gin.Context, req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*requestPayload, error) {
	body := &requestPayload{
		Model:  taskcommon.DefaultString(req.Model, relayInfoModel(info)),
		Images: append([]string(nil), req.Images...),
		Prompt: req.Prompt,
	}
	if body.Model == "" {
		body.Model = "viduq1"
	}
	if body.Prompt == "" {
		body.Prompt = firstTextFromContent(req.Content)
	}
	applyTaskSubmitReq(body, req)
	if err := applyMetadata(body, req.Metadata); err != nil {
		return nil, err
	}
	if c != nil {
		raw, err := rawViduPayloadFromContext(c)
		if err != nil {
			return nil, err
		}
		mergePayload(body, raw, false)
	}
	applyViduDefaults(body)
	normalizeViduPayload(body)
	if err := resolveViduSubjectServerIDs(c, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	taskInfo := &relaycommon.TaskInfo{}

	var taskResp taskResultResponse
	if err := common.Unmarshal(respBody, &taskResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response body: %w", err)
	}

	switch strings.ToLower(strings.TrimSpace(taskResp.State)) {
	case "created", "submitted":
		taskInfo.Status = model.TaskStatusSubmitted
		taskInfo.Progress = taskcommon.ProgressSubmitted
	case "queueing", "queued":
		taskInfo.Status = model.TaskStatusQueued
		taskInfo.Progress = taskcommon.ProgressQueued
	case "processing":
		taskInfo.Status = model.TaskStatusInProgress
		taskInfo.Progress = taskcommon.ProgressInProgress
	case "success", "succeeded":
		taskInfo.Status = model.TaskStatusSuccess
		taskInfo.Progress = taskcommon.ProgressComplete
		taskInfo.Url = firstCreationValue(taskResp.Creations, "url")
		if taskInfo.Url == "" {
			taskInfo.Url = firstCreationValue(taskResp.Creations, "video_url")
		}
		taskInfo.CompletionTokens = taskResp.Credits
		taskInfo.TotalTokens = taskResp.Credits
	case "failed", "failure", "canceled", "cancelled":
		taskInfo.Status = model.TaskStatusFailure
		taskInfo.Progress = taskcommon.ProgressComplete
		taskInfo.Reason = firstNonEmpty(taskResp.ErrCode, taskResp.Reason, taskResp.Message, "task failed")
	default:
		return nil, fmt.Errorf("unknown task state: %s", taskResp.State)
	}
	return taskInfo, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	var viduResp taskResultResponse
	if err := common.Unmarshal(originTask.Data, &viduResp); err != nil {
		return nil, fmt.Errorf("unmarshal vidu task data failed: %w", err)
	}

	openAIVideo := dto.NewOpenAIVideo()
	openAIVideo.ID = originTask.TaskID
	openAIVideo.TaskID = originTask.TaskID
	openAIVideo.Status = originTask.Status.ToVideoStatus()
	openAIVideo.SetProgressStr(originTask.Progress)
	openAIVideo.CreatedAt = originTask.CreatedAt
	openAIVideo.CompletedAt = originTask.UpdatedAt

	if viduResp.Credits > 0 {
		openAIVideo.Usage = map[string]any{
			"completion_tokens": viduResp.Credits,
			"total_tokens":      viduResp.Credits,
			"credits":           viduResp.Credits,
		}
		openAIVideo.SetMetadata("credits", viduResp.Credits)
	}
	if viduResp.Payload != "" {
		openAIVideo.SetMetadata("payload", viduResp.Payload)
	}
	if len(viduResp.Creations) > 0 {
		openAIVideo.SetMetadata("creations", viduResp.Creations)
		for k, v := range viduResp.Creations[0] {
			if v != nil {
				openAIVideo.SetMetadata(k, v)
			}
		}
		if u := firstCreationValue(viduResp.Creations, "url"); u != "" {
			openAIVideo.SetMetadata("url", u)
		}
	}
	if viduResp.State == "failed" {
		code := firstNonEmpty(viduResp.ErrCode, viduResp.Reason, "task_failed")
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: firstNonEmpty(viduResp.Message, code),
			Code:    code,
		}
	}
	return common.Marshal(openAIVideo)
}

func (a *TaskAdaptor) ConvertToViduOfficial(originTask *model.Task) ([]byte, error) {
	if originTask == nil {
		return nil, fmt.Errorf("task is nil")
	}
	return viduBodyWithPublicTaskID(originTask.Data, originTask.TaskID, originTask.Status, originTask.CreatedAt, originTask.UpdatedAt, originTask.FailReason)
}

func viduBodyWithPublicTaskID(body []byte, publicTaskID string, status model.TaskStatus, createdAt, updatedAt int64, failReason string) ([]byte, error) {
	payload := map[string]interface{}{}
	if len(body) > 0 && string(body) != "null" {
		if err := common.Unmarshal(body, &payload); err != nil {
			return nil, err
		}
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	if publicTaskID != "" {
		payload["task_id"] = publicTaskID
	}
	if shouldOverrideViduState(status) || strings.TrimSpace(fmt.Sprint(payload["state"])) == "" {
		payload["state"] = viduStateFromTaskStatus(status)
	}
	if createdAt > 0 && strings.TrimSpace(fmt.Sprint(payload["created_at"])) == "" {
		payload["created_at"] = time.Unix(createdAt, 0).UTC().Format(time.RFC3339)
	}
	if updatedAt > 0 && strings.TrimSpace(fmt.Sprint(payload["updated_at"])) == "" {
		payload["updated_at"] = time.Unix(updatedAt, 0).UTC().Format(time.RFC3339)
	}
	if status == model.TaskStatusFailure && strings.TrimSpace(fmt.Sprint(payload["reason"])) == "" && strings.TrimSpace(failReason) != "" {
		payload["reason"] = strings.TrimSpace(failReason)
	}
	return common.Marshal(payload)
}

func shouldOverrideViduState(status model.TaskStatus) bool {
	return status == model.TaskStatusSuccess || status == model.TaskStatusFailure
}

func viduStateFromTaskStatus(status model.TaskStatus) string {
	switch status {
	case model.TaskStatusSuccess:
		return "success"
	case model.TaskStatusFailure:
		return "failed"
	case model.TaskStatusInProgress:
		return "processing"
	case model.TaskStatusQueued:
		return "queueing"
	case model.TaskStatusSubmitted, model.TaskStatusNotStart:
		return "created"
	default:
		return "created"
	}
}

func normalizeTaskImages(req *relaycommon.TaskSubmitReq) {
	if len(req.Images) == 0 && strings.TrimSpace(req.Image) != "" {
		req.Images = []string{strings.TrimSpace(req.Image)}
	}
	if len(req.Images) == 0 && req.InputReference != "" {
		req.Images = []string{strings.TrimSpace(req.InputReference)}
	}
}

func relayInfoModel(info *relaycommon.RelayInfo) string {
	if info == nil {
		return ""
	}
	if info.UpstreamModelName != "" {
		return info.UpstreamModelName
	}
	return info.OriginModelName
}

func applyTaskSubmitReq(body *requestPayload, req *relaycommon.TaskSubmitReq) {
	if req.Duration > 0 {
		body.Duration = intPtr(req.Duration)
	}
	if req.Size != "" {
		body.Resolution = req.Size
	}
	if req.Resolution != "" {
		body.Resolution = req.Resolution
	}
	if req.Ratio != "" {
		body.AspectRatio = req.Ratio
	}
	if req.CallbackURL != "" {
		body.CallbackURL = req.CallbackURL
	}
	if seed, ok := intFromAny(req.Seed); ok {
		body.Seed = intPtr(seed)
	}
	if watermark, ok := boolFromAny(req.Watermark); ok {
		body.Watermark = boolPtr(watermark)
	}
	images, videos := mediaFromContent(req.Content)
	if len(body.Images) == 0 && len(images) > 0 {
		body.Images = images
	}
	if len(body.Videos) == 0 && len(videos) > 0 {
		body.Videos = videos
	}
}

func applyMetadata(body *requestPayload, metadata map[string]interface{}) error {
	if len(metadata) == 0 {
		return nil
	}
	copied := make(map[string]interface{}, len(metadata))
	for k, v := range metadata {
		if strings.EqualFold(k, "model") {
			continue
		}
		copied[k] = v
	}
	raw, err := common.Marshal(copied)
	if err != nil {
		return fmt.Errorf("marshal metadata failed: %w", err)
	}
	var meta requestPayload
	if err := common.Unmarshal(raw, &meta); err != nil {
		return fmt.Errorf("unmarshal metadata failed: %w", err)
	}
	mergePayload(body, &meta, true)
	applyLooseMetadata(body, copied)
	return nil
}

func rawViduPayloadFromContext(c *gin.Context) (*requestPayload, error) {
	var raw requestPayload
	if c == nil || c.Request == nil || !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return &raw, nil
	}
	if err := common.UnmarshalBodyReusable(c, &raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

func mergePayload(dst, src *requestPayload, allowModel bool) {
	if src == nil {
		return
	}
	if allowModel && src.Model != "" {
		dst.Model = src.Model
	}
	if src.Mode != "" {
		dst.Mode = src.Mode
	}
	if src.Action != "" {
		dst.Action = src.Action
	}
	if src.Style != "" {
		dst.Style = src.Style
	}
	if src.Prompt != "" {
		dst.Prompt = src.Prompt
	}
	if src.Image != "" && len(src.Images) == 0 {
		src.Images = []string{src.Image}
	}
	if len(src.Images) > 0 {
		dst.Images = append([]string(nil), src.Images...)
	}
	if len(src.Videos) > 0 {
		dst.Videos = append([]string(nil), src.Videos...)
	}
	if len(src.Subjects) > 0 {
		dst.Subjects = append([]subjectPayload(nil), src.Subjects...)
	}
	if src.AutoSubjects != nil {
		dst.AutoSubjects = boolPtr(*src.AutoSubjects)
	}
	if src.Audio != nil {
		dst.Audio = boolPtr(*src.Audio)
	}
	if src.AudioType != "" {
		dst.AudioType = src.AudioType
	}
	if src.VoiceID != "" {
		dst.VoiceID = src.VoiceID
	}
	if src.IsRec != nil {
		dst.IsRec = boolPtr(*src.IsRec)
	}
	if src.BGM != nil {
		dst.BGM = boolPtr(*src.BGM)
	}
	if src.Duration != nil {
		dst.Duration = intPtr(*src.Duration)
	}
	if src.Seed != nil {
		dst.Seed = intPtr(*src.Seed)
	}
	if src.AspectRatio != "" {
		dst.AspectRatio = src.AspectRatio
	}
	if src.Resolution != "" {
		dst.Resolution = src.Resolution
	}
	if src.Size != "" {
		dst.Resolution = src.Size
	}
	if src.MovementAmplitude != "" {
		dst.MovementAmplitude = src.MovementAmplitude
	}
	if src.Payload != "" {
		dst.Payload = src.Payload
	}
	if src.OffPeak != nil {
		dst.OffPeak = boolPtr(*src.OffPeak)
	}
	if src.Watermark != nil {
		dst.Watermark = boolPtr(*src.Watermark)
	}
	if src.WMPosition != nil {
		dst.WMPosition = intPtr(*src.WMPosition)
	}
	if src.WMURL != "" {
		dst.WMURL = src.WMURL
	}
	if src.MetaData != "" {
		dst.MetaData = src.MetaData
	}
	if src.CallbackURL != "" {
		dst.CallbackURL = src.CallbackURL
	}
}

func applyLooseMetadata(body *requestPayload, metadata map[string]interface{}) {
	if v, ok := boolFromAny(metadata["audio"]); ok {
		body.Audio = boolPtr(v)
	}
	if v, ok := boolFromAny(metadata["off_peak"]); ok {
		body.OffPeak = boolPtr(v)
	}
	if v, ok := boolFromAny(metadata["watermark"]); ok {
		body.Watermark = boolPtr(v)
	}
	if v, ok := boolFromAny(metadata["bgm"]); ok {
		body.BGM = boolPtr(v)
	}
	if v, ok := boolFromAny(metadata["is_rec"]); ok {
		body.IsRec = boolPtr(v)
	}
	if v, ok := boolFromAny(metadata["auto_subjects"]); ok {
		body.AutoSubjects = boolPtr(v)
	}
	if v, ok := intFromAny(metadata["duration"]); ok {
		body.Duration = intPtr(v)
	}
	if v, ok := intFromAny(metadata["seed"]); ok {
		body.Seed = intPtr(v)
	}
	if v, ok := intFromAny(metadata["wm_position"]); ok {
		body.WMPosition = intPtr(v)
	}
}

func applyViduDefaults(body *requestPayload) {
	if isViduQ3Model(body.Model) {
		if body.Duration == nil {
			body.Duration = intPtr(viduDefaultDuration)
		}
		if body.Resolution == "" {
			body.Resolution = viduDefaultResolution
		}
		return
	}
	if body.Duration == nil {
		body.Duration = intPtr(viduDefaultDuration)
	}
	if body.Resolution == "" {
		body.Resolution = "1080p"
	}
	if body.MovementAmplitude == "" {
		body.MovementAmplitude = "auto"
	}
}

func normalizeViduPayload(body *requestPayload) {
	body.Model = strings.TrimSpace(body.Model)
	body.Mode = strings.TrimSpace(body.Mode)
	body.Action = strings.TrimSpace(body.Action)
	body.Prompt = strings.TrimSpace(body.Prompt)
	body.Resolution = normalizeViduResolution(body.Resolution)
	body.AspectRatio = strings.TrimSpace(body.AspectRatio)
	body.AudioType = strings.TrimSpace(body.AudioType)
	body.WMURL = strings.TrimSpace(body.WMURL)
	body.MetaData = strings.TrimSpace(body.MetaData)
	body.CallbackURL = strings.TrimSpace(body.CallbackURL)
	for i := range body.Subjects {
		body.Subjects[i].Name = strings.TrimSpace(body.Subjects[i].Name)
		body.Subjects[i].VoiceID = strings.TrimSpace(body.Subjects[i].VoiceID)
		body.Subjects[i].ServerID = strings.TrimSpace(body.Subjects[i].ServerID)
	}
}

func resolveViduSubjectServerIDs(c *gin.Context, body *requestPayload) error {
	if c == nil || body == nil || len(body.Subjects) == 0 {
		return nil
	}
	userID := c.GetInt("id")
	channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	if userID <= 0 || channelID <= 0 {
		return nil
	}
	for i := range body.Subjects {
		serverID := strings.TrimSpace(body.Subjects[i].ServerID)
		if serverID == "" {
			continue
		}
		subject, ok, err := model.GetViduSubjectByPublicID(userID, serverID)
		if err != nil {
			return fmt.Errorf("query vidu subject %s failed: %w", serverID, err)
		}
		if !ok || subject == nil {
			continue
		}
		if subject.ChannelID != channelID {
			return fmt.Errorf("vidu subject %s belongs to channel %d, current channel is %d", serverID, subject.ChannelID, channelID)
		}
		body.Subjects[i].ServerID = subject.UpstreamSubjectID
	}
	return nil
}

func determineViduAction(req *relaycommon.TaskSubmitReq, body *requestPayload) string {
	for _, raw := range []string{
		body.Action,
		body.Mode,
		req.Mode,
		stringFromMap(req.Metadata, "action"),
		stringFromMap(req.Metadata, "mode"),
	} {
		if action, ok := viduActionFromString(raw); ok {
			return action
		}
	}
	if len(body.Subjects) > 0 {
		return constant.TaskActionReferenceGenerate
	}
	modelName := strings.ToLower(strings.TrimSpace(body.Model))
	if isViduQ3ReferenceModel(modelName) {
		return constant.TaskActionReferenceGenerate
	}
	if isViduQ3ImageOnlyModel(modelName) {
		return constant.TaskActionGenerate
	}
	switch len(body.Images) {
	case 0:
		return constant.TaskActionTextGenerate
	case 1:
		return constant.TaskActionGenerate
	case 2:
		return constant.TaskActionFirstTailGenerate
	default:
		return constant.TaskActionReferenceGenerate
	}
}

func viduActionFromString(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "":
		return "", false
	case viduModeText, "text", "textgenerate", constant.TaskActionTextGenerate:
		return constant.TaskActionTextGenerate, true
	case viduModeImage, "image", "image2video", constant.TaskActionGenerate:
		return constant.TaskActionGenerate, true
	case viduModeStartEnd, "startend2video", "firsttail", "firsttailgenerate", constant.TaskActionFirstTailGenerate:
		return constant.TaskActionFirstTailGenerate, true
	case viduModeReference, "reference", "referencegenerate", constant.TaskActionReferenceGenerate:
		return constant.TaskActionReferenceGenerate, true
	default:
		return "", false
	}
}

func viduModeNameForAction(action string) string {
	switch action {
	case constant.TaskActionTextGenerate:
		return viduModeText
	case constant.TaskActionGenerate:
		return viduModeImage
	case constant.TaskActionFirstTailGenerate:
		return viduModeStartEnd
	case constant.TaskActionReferenceGenerate:
		return viduModeReference
	default:
		return action
	}
}

func validateViduRequestPayload(body *requestPayload, action string) error {
	if body.Model == "" {
		return fmt.Errorf("model is required")
	}
	if len(body.Payload) > viduMaxPayloadLength {
		return fmt.Errorf("payload must be at most %d characters", viduMaxPayloadLength)
	}
	if body.MetaData != "" && !json.Valid([]byte(body.MetaData)) {
		return fmt.Errorf("meta_data must be a valid JSON string")
	}
	if body.WMURL != "" && !isHTTPURL(body.WMURL) {
		return fmt.Errorf("wm_url must be an http or https URL")
	}
	if body.WMPosition != nil && (*body.WMPosition < 1 || *body.WMPosition > 4) {
		return fmt.Errorf("wm_position must be between 1 and 4")
	}
	if body.AutoSubjects != nil && (action != constant.TaskActionReferenceGenerate || len(body.Subjects) == 0) {
		return fmt.Errorf("auto_subjects is only supported for subject reference2video")
	}

	if !isViduQ3Model(body.Model) {
		return nil
	}
	if len(body.Videos) > 0 {
		return fmt.Errorf("top-level videos is not supported by Vidu Q3")
	}
	if !viduQ3SupportsAction(body.Model, action) {
		return fmt.Errorf("%s is not supported by %s", viduModeNameForAction(action), body.Model)
	}
	if body.Duration == nil {
		body.Duration = intPtr(viduDefaultDuration)
	}
	if body.Resolution == "" {
		body.Resolution = viduDefaultResolution
	}
	allowedResolutions := viduQ3AllowedResolutions(body.Model, action)
	if len(allowedResolutions) == 0 {
		return fmt.Errorf("%s is not supported by %s", action, body.Model)
	}
	if !containsString(allowedResolutions, body.Resolution) {
		return fmt.Errorf("resolution for %s must be one of %s", action, strings.Join(allowedResolutions, ", "))
	}
	if body.Audio == nil {
		if viduQ3DefaultAudio(body.Model, action) {
			body.Audio = boolPtr(true)
		}
	}
	audioEnabled := body.Audio != nil && *body.Audio
	if action == constant.TaskActionReferenceGenerate && len(body.Subjects) > 0 && audioEnabled && body.AudioType == "" {
		body.AudioType = "all"
	}
	if body.AudioType != "" && !audioEnabled {
		return fmt.Errorf("audio_type requires audio=true")
	}
	if body.OffPeak != nil && *body.OffPeak {
		if !viduQ3SupportsOffPeak(body.Model, action) {
			return fmt.Errorf("off_peak is not supported by %s", body.Model)
		}
		if !audioEnabled {
			return fmt.Errorf("off_peak=true requires audio=true")
		}
	}
	if body.IsRec != nil && !viduQ3SupportsIsRec(action) {
		return fmt.Errorf("is_rec is only supported for img2video and start-end2video")
	}

	duration := *body.Duration
	switch action {
	case constant.TaskActionTextGenerate:
		if body.Prompt == "" {
			return fmt.Errorf("prompt is required for text2video")
		}
		if err := validatePromptLength(body.Prompt, 5000, "text2video"); err != nil {
			return err
		}
		if len(body.Images) > 0 || len(body.Subjects) > 0 {
			return fmt.Errorf("text2video does not accept images or subjects")
		}
		if err := validateDuration(duration, viduQ3DurationMin(body.Model, action), viduQ3DurationMax(body.Model, action)); err != nil {
			return err
		}
		return validateAspectRatio(body.AspectRatio, []string{"16:9", "9:16", "3:4", "4:3", "1:1"}, "text2video")
	case constant.TaskActionGenerate:
		if len(body.Images) != 1 {
			return fmt.Errorf("img2video requires exactly 1 image")
		}
		if err := validateOptionalPromptLength(body.Prompt, 5000, "img2video"); err != nil {
			return err
		}
		if len(body.Subjects) > 0 {
			return fmt.Errorf("img2video does not accept subjects")
		}
		if body.AspectRatio != "" {
			return fmt.Errorf("aspect_ratio is not supported for img2video")
		}
		return validateDuration(duration, viduQ3DurationMin(body.Model, action), viduQ3DurationMax(body.Model, action))
	case constant.TaskActionFirstTailGenerate:
		if len(body.Images) != 2 {
			return fmt.Errorf("start-end2video requires exactly 2 images")
		}
		if err := validateOptionalPromptLength(body.Prompt, 5000, "start-end2video"); err != nil {
			return err
		}
		if len(body.Subjects) > 0 {
			return fmt.Errorf("start-end2video does not accept subjects")
		}
		if body.AspectRatio != "" {
			return fmt.Errorf("aspect_ratio is not supported for start-end2video")
		}
		return validateDuration(duration, viduQ3DurationMin(body.Model, action), viduQ3DurationMax(body.Model, action))
	case constant.TaskActionReferenceGenerate:
		if body.Prompt == "" {
			return fmt.Errorf("prompt is required for reference2video")
		}
		maxPromptLength := 2000
		if viduQ3SupportsReferenceSubjects(body.Model) && len(body.Subjects) > 0 {
			maxPromptLength = 5000
		}
		if err := validatePromptLength(body.Prompt, maxPromptLength, "reference2video"); err != nil {
			return err
		}
		if err := validateDuration(duration, viduQ3DurationMin(body.Model, action), viduQ3DurationMax(body.Model, action)); err != nil {
			return err
		}
		return validateReferenceInputs(body)
	default:
		return fmt.Errorf("unsupported vidu action: %s", action)
	}
}

func validateOptionalPromptLength(prompt string, max int, mode string) error {
	if prompt == "" {
		return nil
	}
	return validatePromptLength(prompt, max, mode)
}

func validatePromptLength(prompt string, max int, mode string) error {
	if utf8.RuneCountInString(prompt) > max {
		return fmt.Errorf("prompt for %s must be at most %d characters", mode, max)
	}
	return nil
}

func validateReferenceInputs(body *requestPayload) error {
	modelName := strings.ToLower(strings.TrimSpace(body.Model))
	supportsSubjects := viduQ3SupportsReferenceSubjects(modelName)
	if len(body.Subjects) > 0 {
		if !supportsSubjects {
			return fmt.Errorf("subjects are not supported by %s", body.Model)
		}
		if len(body.Subjects) > 7 {
			return fmt.Errorf("subjects supports at most 7 items")
		}
		for i, subject := range body.Subjects {
			if len(subject.Videos) > 0 {
				return fmt.Errorf("subjects[%d].videos is not supported by Vidu Q3", i)
			}
			if subject.ServerID != "" && len(subject.Images) > 0 {
				return fmt.Errorf("subjects[%d] cannot use both server_id and images", i)
			}
			if len(subject.Images) > 3 {
				return fmt.Errorf("subjects[%d].images supports at most 3 images", i)
			}
			if subject.Name == "" && subject.ServerID == "" && len(subject.Images) == 0 {
				return fmt.Errorf("subjects[%d] must include name, server_id, or images", i)
			}
		}
	}
	if body.AutoSubjects != nil && !supportsSubjects {
		return fmt.Errorf("auto_subjects is not supported by %s", body.Model)
	}
	if len(body.Subjects) == 0 && body.AudioType != "" {
		return fmt.Errorf("audio_type is not supported for ordinary reference2video")
	}
	if body.AudioType != "" && !containsString([]string{"all", "speech_only", "sound_effect_only"}, body.AudioType) {
		return fmt.Errorf("audio_type must be one of all, speech_only, sound_effect_only")
	}
	if len(body.Images) == 0 && len(body.Subjects) == 0 {
		return fmt.Errorf("reference2video requires images or subjects")
	}
	if len(body.Images) > 7 {
		return fmt.Errorf("reference2video supports at most 7 reference images")
	}
	if err := validateAspectRatio(body.AspectRatio, []string{"16:9", "9:16", "1:1"}, "reference2video"); err != nil {
		return err
	}
	return nil
}

func validateDuration(duration, min, max int) error {
	if duration < min || duration > max {
		return fmt.Errorf("duration must be between %d and %d seconds", min, max)
	}
	return nil
}

func validateAspectRatio(value string, allowed []string, mode string) error {
	if value == "" {
		return nil
	}
	if !containsString(allowed, value) {
		return fmt.Errorf("aspect_ratio for %s must be one of %s", mode, strings.Join(allowed, ", "))
	}
	return nil
}

func buildViduRequestMap(body *requestPayload, action string) map[string]interface{} {
	out := map[string]interface{}{
		"model": body.Model,
	}
	addString := func(key, value string) {
		if value != "" {
			out[key] = value
		}
	}
	addStringSlice := func(key string, value []string) {
		if len(value) > 0 {
			out[key] = value
		}
	}
	addBoolPtr := func(key string, value *bool) {
		if value != nil {
			out[key] = *value
		}
	}
	addIntPtr := func(key string, value *int) {
		if value != nil {
			out[key] = *value
		}
	}

	addIntPtr("duration", body.Duration)
	addIntPtr("seed", body.Seed)
	addString("resolution", body.Resolution)
	addString("movement_amplitude", body.MovementAmplitude)
	addString("payload", body.Payload)
	addBoolPtr("off_peak", body.OffPeak)
	addBoolPtr("watermark", body.Watermark)
	addIntPtr("wm_position", body.WMPosition)
	addString("wm_url", body.WMURL)
	addString("meta_data", body.MetaData)
	addString("callback_url", body.CallbackURL)
	addBoolPtr("audio", body.Audio)

	switch action {
	case constant.TaskActionTextGenerate:
		addString("prompt", body.Prompt)
		addString("style", body.Style)
		addString("aspect_ratio", body.AspectRatio)
		if !isViduQ3Model(body.Model) {
			addBoolPtr("bgm", body.BGM)
		}
	case constant.TaskActionGenerate:
		addStringSlice("images", body.Images)
		addString("prompt", body.Prompt)
		if !isViduQ3Model(body.Model) {
			addBoolPtr("bgm", body.BGM)
		}
		addBoolPtr("is_rec", body.IsRec)
	case constant.TaskActionFirstTailGenerate:
		addStringSlice("images", body.Images)
		addString("prompt", body.Prompt)
		addBoolPtr("is_rec", body.IsRec)
	case constant.TaskActionReferenceGenerate:
		addString("prompt", body.Prompt)
		addStringSlice("images", body.Images)
		if len(body.Subjects) > 0 {
			out["subjects"] = body.Subjects
		}
		addBoolPtr("auto_subjects", body.AutoSubjects)
		addString("aspect_ratio", body.AspectRatio)
		addString("audio_type", body.AudioType)
		addString("voice_id", body.VoiceID)
	}
	return out
}

func EstimateViduQ3Credits(body *requestPayload, action string) (int, bool) {
	if body == nil || !isViduQ3Model(body.Model) || body.Duration == nil {
		return 0, false
	}
	rate, ok := viduQ3CreditRate(body.Model, action, body.Resolution, body.OffPeak != nil && *body.OffPeak)
	if !ok {
		return 0, false
	}
	credits := *body.Duration * rate
	if body.IsRec != nil && *body.IsRec && (action == constant.TaskActionGenerate || action == constant.TaskActionFirstTailGenerate) {
		credits += 10
	}
	return credits, true
}

func viduQ3CreditRate(modelName, action, resolution string, offPeak bool) (int, bool) {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	resolution = normalizeViduResolution(resolution)
	switch action {
	case constant.TaskActionReferenceGenerate:
		switch modelName {
		case viduQ3TurboModel:
			if offPeak {
				return map[string]int{"540p": 2, "720p": 5, "1080p": 7}[resolution], containsString([]string{"540p", "720p", "1080p"}, resolution)
			}
			return map[string]int{"540p": 4, "720p": 10, "1080p": 13}[resolution], containsString([]string{"540p", "720p", "1080p"}, resolution)
		case viduQ3Model:
			if offPeak {
				return map[string]int{"540p": 4, "720p": 6, "1080p": 7}[resolution], containsString([]string{"540p", "720p", "1080p"}, resolution)
			}
			return map[string]int{"540p": 7, "720p": 12, "1080p": 15}[resolution], containsString([]string{"540p", "720p", "1080p"}, resolution)
		case viduQ3MixModel, viduQ3DramaModel, viduQ3AdModel:
			if offPeak {
				return 0, false
			}
			return map[string]int{"720p": 24, "1080p": 29}[resolution], containsString([]string{"720p", "1080p"}, resolution)
		default:
			return 0, false
		}
	case constant.TaskActionTextGenerate, constant.TaskActionGenerate, constant.TaskActionFirstTailGenerate:
		switch modelName {
		case viduQ3ProModel:
			if offPeak {
				return map[string]int{"540p": 5, "720p": 10, "1080p": 12}[resolution], containsString([]string{"540p", "720p", "1080p"}, resolution)
			}
			return map[string]int{"540p": 9, "720p": 20, "1080p": 24}[resolution], containsString([]string{"540p", "720p", "1080p"}, resolution)
		case viduQ3TurboModel:
			if offPeak {
				return map[string]int{"540p": 4, "720p": 6, "1080p": 7}[resolution], containsString([]string{"540p", "720p", "1080p"}, resolution)
			}
			return map[string]int{"540p": 7, "720p": 11, "1080p": 13}[resolution], containsString([]string{"540p", "720p", "1080p"}, resolution)
		case viduQ3ProFastModel:
			if offPeak {
				return map[string]int{"720p": 10, "1080p": 13}[resolution], containsString([]string{"720p", "1080p"}, resolution)
			}
			return map[string]int{"720p": 20, "1080p": 25}[resolution], containsString([]string{"720p", "1080p"}, resolution)
		default:
			return 0, false
		}
	default:
		return 0, false
	}
}

func viduUpstreamTaskError(statusCode int, body []byte) *dto.TaskError {
	var upstream viduErrorResponse
	_ = common.Unmarshal(body, &upstream)
	code := firstNonEmpty(upstream.Reason, "vidu_upstream_error")
	message := firstNonEmpty(upstream.Message, strings.TrimSpace(string(body)), "vidu upstream error")
	return &dto.TaskError{
		Code:       code,
		Message:    message,
		Data:       upstream,
		StatusCode: statusCode,
		Error:      fmt.Errorf("%s: %s", code, message),
	}
}

func viduResponseTaskError(resp responsePayload) *dto.TaskError {
	code := firstNonEmpty(resp.ErrCode, resp.Reason, "task_failed")
	message := firstNonEmpty(resp.Message, resp.Reason, resp.ErrCode, "task failed")
	return &dto.TaskError{
		Code:       code,
		Message:    message,
		StatusCode: http.StatusBadGateway,
		Error:      fmt.Errorf("%s: %s", code, message),
	}
}

func isViduQ3Model(modelName string) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	return isViduQ3SeriesModel(modelName)
}

func isViduQ3SeriesModel(modelName string) bool {
	switch strings.ToLower(strings.TrimSpace(modelName)) {
	case viduQ3ProModel, viduQ3ProFastModel, viduQ3MixModel, viduQ3DramaModel, viduQ3AdModel, viduQ3TurboModel, viduQ3Model:
		return true
	default:
		return false
	}
}

func isViduQ3ReferenceModel(modelName string) bool {
	switch strings.ToLower(strings.TrimSpace(modelName)) {
	case viduQ3MixModel, viduQ3DramaModel, viduQ3AdModel, viduQ3Model:
		return true
	default:
		return false
	}
}

func isViduQ3ImageOnlyModel(modelName string) bool {
	return strings.EqualFold(strings.TrimSpace(modelName), viduQ3ProFastModel)
}

func viduQ3SupportsAction(modelName, action string) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	switch action {
	case constant.TaskActionTextGenerate, constant.TaskActionFirstTailGenerate:
		return modelName == viduQ3ProModel || modelName == viduQ3TurboModel
	case constant.TaskActionGenerate:
		return modelName == viduQ3ProModel || modelName == viduQ3TurboModel || modelName == viduQ3ProFastModel
	case constant.TaskActionReferenceGenerate:
		return modelName == viduQ3TurboModel || modelName == viduQ3Model || modelName == viduQ3MixModel || modelName == viduQ3DramaModel || modelName == viduQ3AdModel
	default:
		return false
	}
}

func viduQ3SupportsReferenceSubjects(modelName string) bool {
	switch strings.ToLower(strings.TrimSpace(modelName)) {
	case viduQ3TurboModel, viduQ3Model, viduQ3DramaModel, viduQ3AdModel:
		return true
	default:
		return false
	}
}

func viduQ3SupportsOffPeak(modelName, action string) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	switch action {
	case constant.TaskActionTextGenerate, constant.TaskActionGenerate, constant.TaskActionFirstTailGenerate:
		return modelName == viduQ3ProModel || modelName == viduQ3TurboModel || modelName == viduQ3ProFastModel
	case constant.TaskActionReferenceGenerate:
		return modelName == viduQ3TurboModel || modelName == viduQ3Model
	default:
		return false
	}
}

func viduQ3SupportsIsRec(action string) bool {
	return action == constant.TaskActionGenerate || action == constant.TaskActionFirstTailGenerate
}

func viduQ3DefaultAudio(modelName, action string) bool {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	switch action {
	case constant.TaskActionTextGenerate, constant.TaskActionGenerate, constant.TaskActionFirstTailGenerate:
		return modelName == viduQ3ProModel || modelName == viduQ3TurboModel || modelName == viduQ3ProFastModel
	case constant.TaskActionReferenceGenerate:
		return modelName == viduQ3TurboModel || modelName == viduQ3Model
	default:
		return false
	}
}

func viduQ3AllowedResolutions(modelName, action string) []string {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	switch action {
	case constant.TaskActionTextGenerate, constant.TaskActionGenerate, constant.TaskActionFirstTailGenerate:
		switch modelName {
		case viduQ3ProFastModel:
			return []string{"720p", "1080p"}
		case viduQ3ProModel, viduQ3TurboModel:
			return []string{"540p", "720p", "1080p"}
		default:
			return nil
		}
	case constant.TaskActionReferenceGenerate:
		switch modelName {
		case viduQ3TurboModel, viduQ3Model:
			return []string{"540p", "720p", "1080p"}
		case viduQ3MixModel, viduQ3DramaModel, viduQ3AdModel:
			return []string{"720p", "1080p"}
		default:
			return nil
		}
	default:
		return nil
	}
}

func viduQ3DurationMin(modelName, action string) int {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	switch action {
	case constant.TaskActionReferenceGenerate:
		switch modelName {
		case viduQ3DramaModel, viduQ3AdModel:
			return 3
		case viduQ3TurboModel, viduQ3Model, viduQ3MixModel:
			return 3
		default:
			return 0
		}
	case constant.TaskActionTextGenerate, constant.TaskActionGenerate, constant.TaskActionFirstTailGenerate:
		switch modelName {
		case viduQ3ProModel, viduQ3TurboModel, viduQ3ProFastModel:
			return 1
		default:
			return 0
		}
	default:
		return 0
	}
}

func viduQ3DurationMax(modelName, action string) int {
	modelName = strings.ToLower(strings.TrimSpace(modelName))
	switch action {
	case constant.TaskActionReferenceGenerate:
		switch modelName {
		case viduQ3DramaModel, viduQ3AdModel:
			return 15
		case viduQ3TurboModel, viduQ3Model, viduQ3MixModel:
			return 16
		default:
			return 0
		}
	case constant.TaskActionTextGenerate, constant.TaskActionGenerate, constant.TaskActionFirstTailGenerate:
		switch modelName {
		case viduQ3ProModel, viduQ3TurboModel, viduQ3ProFastModel:
			return 16
		default:
			return 0
		}
	default:
		return 0
	}
}

func IsQ3Model(modelName string) bool {
	return isViduQ3SeriesModel(modelName)
}

func normalizeViduResolution(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "540" || value == "540p" {
		return "540p"
	}
	if value == "720" || value == "720p" {
		return "720p"
	}
	if value == "1080" || value == "1080p" {
		return "1080p"
	}
	return value
}

func mediaFromContent(content []map[string]interface{}) (images []string, videos []string) {
	for _, item := range content {
		itemType := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["type"])))
		switch itemType {
		case "image_url":
			if u := mediaURLFromAny(item["image_url"]); u != "" {
				images = append(images, u)
			}
		case "video_url":
			if u := mediaURLFromAny(item["video_url"]); u != "" {
				videos = append(videos, u)
			}
		}
	}
	return images, videos
}

func mediaURLFromAny(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case map[string]interface{}:
		if u, ok := v["url"].(string); ok {
			return strings.TrimSpace(u)
		}
	}
	return ""
}

func firstTextFromContent(content []map[string]interface{}) string {
	for _, item := range content {
		itemType := strings.ToLower(strings.TrimSpace(fmt.Sprint(item["type"])))
		if itemType != "" && itemType != "text" {
			continue
		}
		if text, ok := item["text"].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func firstCreationValue(creations []map[string]interface{}, key string) string {
	if len(creations) == 0 {
		return ""
	}
	if value, ok := creations[0][key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func stringFromMap(m map[string]interface{}, key string) string {
	if len(m) == 0 {
		return ""
	}
	if v, ok := m[key]; ok {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func boolFromAny(raw interface{}) (bool, bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(v))
		return parsed, err == nil
	default:
		return false, false
	}
}

func intFromAny(raw interface{}) (int, bool) {
	switch v := raw.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		parsed, err := strconv.Atoi(v.String())
		return parsed, err == nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(v))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func OfficialStateFromTaskStatus(status model.TaskStatus) string {
	switch status {
	case model.TaskStatusSuccess:
		return "success"
	case model.TaskStatusFailure:
		return "failed"
	case model.TaskStatusQueued:
		return "queueing"
	case model.TaskStatusSubmitted, model.TaskStatusNotStart:
		return "created"
	case model.TaskStatusInProgress:
		return "processing"
	default:
		return "created"
	}
}

func OfficialTaskResponseFromTask(task *model.Task) map[string]interface{} {
	out := map[string]interface{}{
		"state":     "created",
		"err_code":  "",
		"reason":    "",
		"credits":   0,
		"payload":   "",
		"creations": []interface{}{},
	}
	if task == nil {
		return out
	}
	if len(task.Data) > 0 {
		var raw map[string]interface{}
		if err := common.Unmarshal(task.Data, &raw); err == nil && raw != nil {
			for k, v := range raw {
				out[k] = v
			}
		}
	}
	out["task_id"] = task.TaskID
	out["state"] = firstNonEmpty(common.Interface2String(out["state"]), OfficialStateFromTaskStatus(task.Status))
	if task.Status == model.TaskStatusFailure {
		out["state"] = "failed"
		out["reason"] = firstNonEmpty(common.Interface2String(out["reason"]), task.FailReason)
		out["message"] = firstNonEmpty(common.Interface2String(out["message"]), task.FailReason)
	}
	if task.Status == model.TaskStatusSuccess {
		out["state"] = "success"
		if _, ok := out["creations"]; !ok || out["creations"] == nil {
			out["creations"] = []interface{}{}
		}
		if creations, ok := out["creations"].([]interface{}); (!ok || len(creations) == 0) && strings.TrimSpace(task.GetResultURL()) != "" {
			out["creations"] = []interface{}{
				map[string]interface{}{"url": task.GetResultURL()},
			}
		}
	}
	return out
}

func boolPtr(v bool) *bool {
	return &v
}

func intPtr(v int) *int {
	return &v
}
