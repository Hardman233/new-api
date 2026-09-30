package xai

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

type videoTransport uint8

const (
	videoTransportGRPC videoTransport = iota
	videoTransportREST
	defaultUniAPIDuration   = 6
	defaultUniAPIResolution = "480p"
)

type restVideoDialect uint8

const (
	restVideoDialectGeneric restVideoDialect = iota
	restVideoDialectUniAPI
)

type uniAPIVideoGenerateRequest struct {
	Prompt      string             `json:"prompt"`
	AspectRatio string             `json:"aspect_ratio,omitempty"`
	Duration    int                `json:"duration"`
	Image       *uniAPIVideoImage  `json:"image,omitempty"`
	Model       string             `json:"model"`
	Output      *uniAPIVideoOutput `json:"output,omitempty"`
	Resolution  string             `json:"resolution"`
}

type uniAPIVideoImage struct {
	URL string `json:"url"`
}

type uniAPIVideoOutput struct {
	UploadURL string `json:"upload_url"`
}

type uniAPIVideoSubmitResponse struct {
	RequestID string `json:"request_id"`
}

type uniAPIVideoStatusResponse struct {
	Status string            `json:"status"`
	Model  string            `json:"model"`
	Video  *uniAPIVideoFile  `json:"video"`
	Error  *uniAPIVideoError `json:"error"`
}

type uniAPIVideoFile struct {
	URL               string        `json:"url"`
	Duration          flexibleInt   `json:"duration"`
	RespectModeration *flexibleBool `json:"respect_moderation"`
}

type uniAPIVideoError struct {
	Code    any    `json:"code"`
	Message string `json:"message"`
	Type    string `json:"type"`
}

type flexibleInt int64

func (v *flexibleInt) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*v = 0
		return nil
	}
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		parsed, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return err
		}
		*v = flexibleInt(parsed)
		return nil
	}
	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return err
	}
	*v = flexibleInt(parsed)
	return nil
}

type flexibleBool bool

func (v *flexibleBool) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, `"`) {
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return err
		}
		parsed, err := strconv.ParseBool(strings.TrimSpace(text))
		if err != nil {
			return err
		}
		*v = flexibleBool(parsed)
		return nil
	}
	var parsed bool
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*v = flexibleBool(parsed)
	return nil
}

type restVideoExtra struct {
	RequestID      string         `json:"request_id"`
	URL            string         `json:"url"`
	ResultURL      string         `json:"result_url"`
	ProviderStatus string         `json:"provider_status"`
	StorageError   string         `json:"storage_error"`
	Video          *restVideoFile `json:"video"`
}

type restVideoFile struct {
	URL               string `json:"url"`
	Duration          int    `json:"duration"`
	FileID            string `json:"file_id"`
	PublicURL         string `json:"public_url"`
	StorageError      string `json:"storage_error"`
	RespectModeration *bool  `json:"respect_moderation"`
}

func selectVideoTransport(rawBaseURL string) videoTransport {
	rawBaseURL = strings.TrimSpace(rawBaseURL)
	if rawBaseURL == "" {
		return videoTransportGRPC
	}
	lower := strings.ToLower(rawBaseURL)
	if strings.HasPrefix(lower, "grpc://") || strings.HasPrefix(lower, "grpcs://") {
		return videoTransportGRPC
	}
	parsed := rawBaseURL
	if !strings.Contains(parsed, "://") {
		parsed = "https://" + parsed
	}
	u, err := url.Parse(parsed)
	if err == nil && strings.EqualFold(strings.TrimSpace(u.Hostname()), "api.x.ai") {
		return videoTransportGRPC
	}
	return videoTransportREST
}

func buildRESTSubmitURL(baseURL string) (string, error) {
	if selectRESTVideoDialect(baseURL) == restVideoDialectUniAPI {
		return buildUniAPIRESTURL(baseURL, "videos", "generations")
	}
	return buildRESTURL(baseURL, "videos")
}

func buildRESTTaskURL(baseURL, taskID string) (string, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", errors.New("invalid task_id")
	}
	if selectRESTVideoDialect(baseURL) == restVideoDialectUniAPI {
		return buildUniAPIRESTURL(baseURL, "videos", taskID)
	}
	return buildRESTURL(baseURL, "videos", taskID)
}

func selectRESTVideoDialect(rawBaseURL string) restVideoDialect {
	rawBaseURL = strings.TrimSpace(rawBaseURL)
	if rawBaseURL == "" {
		return restVideoDialectGeneric
	}
	if !strings.Contains(rawBaseURL, "://") {
		rawBaseURL = "https://" + rawBaseURL
	}
	u, err := url.Parse(rawBaseURL)
	if err != nil {
		return restVideoDialectGeneric
	}
	if strings.EqualFold(strings.TrimSpace(u.Hostname()), "api.uniapi.io") {
		return restVideoDialectUniAPI
	}
	cleanPath := "/" + strings.Trim(strings.ToLower(u.Path), "/")
	if cleanPath == "/xai" || cleanPath == "/xai/v1" || strings.HasSuffix(cleanPath, "/xai/v1") {
		return restVideoDialectUniAPI
	}
	return restVideoDialectGeneric
}

func buildUniAPIRESTURL(baseURL string, segments ...string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", errors.New("UniAPI REST base URL is required")
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse UniAPI REST base URL: %w", err)
	}
	if u.Host == "" {
		return "", errors.New("UniAPI REST base URL has no host")
	}
	basePath := strings.TrimRight(u.Path, "/")
	lowerPath := strings.ToLower(basePath)
	switch {
	case strings.HasSuffix(lowerPath, "/xai/v1"):
	case strings.HasSuffix(lowerPath, "/xai"):
		basePath = path.Join(basePath, "v1")
	default:
		basePath = path.Join(basePath, "xai", "v1")
	}
	parts := append([]string{basePath}, segments...)
	u.Path = path.Join(parts...)
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func buildRESTURL(baseURL string, segments ...string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", errors.New("xAI REST base URL is required")
	}
	if !strings.Contains(baseURL, "://") {
		baseURL = "https://" + baseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse xAI REST base URL: %w", err)
	}
	if u.Host == "" {
		return "", errors.New("xAI REST base URL has no host")
	}
	basePath := strings.TrimRight(u.Path, "/")
	if basePath == "" {
		basePath = "/v1"
	}
	parts := append([]string{basePath}, segments...)
	u.Path = path.Join(parts...)
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func (a *TaskAdaptor) doRESTResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()
	if a.usesUniAPI() {
		upstreamID, decodeErr := decodeUniAPISubmitResponse(responseBody)
		if decodeErr != nil {
			return "", nil, service.TaskErrorWrapper(decodeErr, "invalid_response", http.StatusBadGateway)
		}
		record := newSubmitRecord(c, info, upstreamID)
		taskData, marshalErr := common.Marshal(record)
		if marshalErr != nil {
			return "", nil, service.TaskErrorWrapper(marshalErr, "marshal_task_data_failed", http.StatusInternalServerError)
		}
		public := dto.NewOpenAIVideo()
		public.ID = info.PublicTaskID
		public.TaskID = info.PublicTaskID
		public.Model = info.OriginModelName
		public.Status = dto.VideoStatusQueued
		public.Progress = 0
		public.CreatedAt = common.GetTimestamp()
		applySubmitRecord(public, record)
		c.JSON(http.StatusOK, public)
		return upstreamID, taskData, nil
	}

	video, recognized, err := decodeRESTVideo(responseBody)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "unmarshal_response_body_failed", http.StatusBadGateway)
	}
	if !recognized {
		return "", nil, service.TaskErrorWrapper(errors.New("xAI REST response has no task data"), "invalid_response", http.StatusBadGateway)
	}
	upstreamID := firstNonEmpty(video.ID, video.TaskID)
	if upstreamID == "" {
		return "", nil, service.TaskErrorWrapper(errors.New("xAI REST task id is empty"), "invalid_response", http.StatusBadGateway)
	}

	public := *video
	public.ID = info.PublicTaskID
	public.TaskID = info.PublicTaskID
	public.Model = firstNonEmpty(info.OriginModelName, public.Model)
	if public.Object == "" {
		public.Object = "video"
	}
	if public.Status == "" {
		public.Status = dto.VideoStatusQueued
	}
	c.JSON(http.StatusOK, &public)
	return upstreamID, responseBody, nil
}

func fetchRESTTask(baseURL, key, taskID, proxy string) (*http.Response, error) {
	requestURL, err := buildRESTTaskURL(baseURL, taskID)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(key))
	request.Header.Set("Accept", "application/json")
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new xAI REST client: %w", err)
	}
	return client.Do(request)
}

func decodeRESTVideo(body []byte) (*dto.OpenAIVideo, bool, error) {
	raw := json.RawMessage(strings.TrimSpace(string(body)))
	if len(raw) == 0 {
		return nil, false, errors.New("empty xAI REST response")
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := common.Unmarshal(raw, &envelope); err != nil {
		return nil, false, err
	}
	if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		var nested map[string]json.RawMessage
		if common.Unmarshal(envelope.Data, &nested) == nil {
			raw = envelope.Data
		}
	}

	var video dto.OpenAIVideo
	if err := common.Unmarshal(raw, &video); err != nil {
		return nil, false, err
	}
	var extra restVideoExtra
	if err := common.Unmarshal(raw, &extra); err != nil {
		return nil, false, err
	}
	if video.ID == "" {
		video.ID = firstNonEmpty(video.TaskID, extra.RequestID)
	}
	if video.TaskID == "" {
		video.TaskID = video.ID
	}
	if video.Metadata == nil {
		video.Metadata = map[string]any{}
	}
	if outputURL := firstNonEmpty(extra.URL, extra.ResultURL, restFileURL(extra.Video)); outputURL != "" {
		if _, exists := video.Metadata["url"]; !exists {
			video.Metadata["url"] = outputURL
		}
	}
	if extra.ProviderStatus != "" {
		video.Metadata["provider_status"] = extra.ProviderStatus
	}
	if extra.StorageError != "" {
		video.Metadata["storage_error"] = extra.StorageError
	}
	if extra.Video != nil {
		if extra.Video.Duration > 0 && video.Seconds == "" {
			video.Seconds = strconv.Itoa(extra.Video.Duration)
		}
		if extra.Video.FileID != "" {
			video.Metadata["upstream_file_id"] = extra.Video.FileID
		}
		if extra.Video.PublicURL != "" {
			video.Metadata["upstream_public_url"] = extra.Video.PublicURL
		}
		if extra.Video.StorageError != "" {
			video.Metadata["storage_error"] = extra.Video.StorageError
		}
		if extra.Video.RespectModeration != nil {
			video.Metadata["respect_moderation"] = *extra.Video.RespectModeration
		}
	}
	recognized := video.ID != "" || video.Status != "" || video.Object == "video"
	return &video, recognized, nil
}

func decodeUniAPISubmitResponse(body []byte) (string, error) {
	raw, err := unwrapRESTData(body)
	if err != nil {
		return "", err
	}
	var response uniAPIVideoSubmitResponse
	if err = common.Unmarshal(raw, &response); err != nil {
		return "", err
	}
	if strings.TrimSpace(response.RequestID) == "" {
		return "", errors.New("UniAPI request_id is empty")
	}
	return strings.TrimSpace(response.RequestID), nil
}

func parseUniAPITaskResult(body []byte) (*relaycommon.TaskInfo, error) {
	raw, err := unwrapRESTData(body)
	if err != nil {
		return nil, err
	}
	var response uniAPIVideoStatusResponse
	if err = common.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	providerStatus := strings.ToLower(strings.TrimSpace(response.Status))
	result := &relaycommon.TaskInfo{Code: 0, ProviderStatus: providerStatus}

	if response.Video != nil {
		result.Url = strings.TrimSpace(response.Video.URL)
		result.RemoteUrl = result.Url
		result.Duration = int32(response.Video.Duration)
		if response.Video.RespectModeration != nil {
			moderation := bool(*response.Video.RespectModeration)
			result.RespectModeration = &moderation
			if !moderation && result.Url == "" {
				result.Status = model.TaskStatusFailure
				result.ProviderStatus = "moderation_rejected"
				result.ErrorCode = "moderation_rejected"
				result.Reason = "video rejected by upstream moderation"
				result.Progress = "100%"
				return result, nil
			}
		}
		if result.Url != "" {
			result.Status = model.TaskStatusSuccess
			if result.ProviderStatus == "" {
				result.ProviderStatus = "completed"
			}
			result.Progress = "100%"
			return result, nil
		}
	}

	if response.Error != nil {
		result.Status = model.TaskStatusFailure
		result.ProviderStatus = firstNonEmpty(providerStatus, "failed")
		result.ErrorCode = stringValue(response.Error.Code)
		result.Reason = firstNonEmpty(strings.TrimSpace(response.Error.Message), strings.TrimSpace(response.Error.Type), result.ErrorCode)
		result.Progress = "100%"
		return result, nil
	}

	switch providerStatus {
	case "queued", "pending", "processing", "in_progress", "running":
		result.Status = model.TaskStatusInProgress
		result.Progress = "0%"
		return result, nil
	case "failed", "cancelled", "canceled", "expired":
		result.Status = model.TaskStatusFailure
		result.Progress = "100%"
		result.Reason = "UniAPI video request " + providerStatus
		return result, nil
	case "completed", "succeeded", "success", "done":
		result.Status = model.TaskStatusSuccess
		result.Progress = "100%"
		return result, nil
	default:
		return nil, errors.New("UniAPI REST response has no recognizable task state")
	}
}

func unwrapRESTData(body []byte) (json.RawMessage, error) {
	raw := json.RawMessage(strings.TrimSpace(string(body)))
	if len(raw) == 0 {
		return nil, errors.New("empty REST response")
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := common.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	if len(envelope.Data) > 0 && string(envelope.Data) != "null" {
		return envelope.Data, nil
	}
	return raw, nil
}

func parseRESTTaskResult(responseBody []byte) (*relaycommon.TaskInfo, error) {
	video, recognized, err := decodeRESTVideo(responseBody)
	if err != nil {
		return nil, err
	}
	if !recognized {
		return nil, errors.New("xAI REST response has no task data")
	}
	providerStatus := strings.TrimSpace(video.Status)
	result := &relaycommon.TaskInfo{
		Code:           0,
		TaskID:         firstNonEmpty(video.ID, video.TaskID),
		ProviderStatus: providerStatus,
		Progress:       fmt.Sprintf("%d%%", video.Progress),
		Url:            restVideoURL(video),
		RemoteUrl:      restVideoURL(video),
	}
	switch strings.ToLower(providerStatus) {
	case "queued", "pending", "not_started", "not_start":
		result.Status = model.TaskStatusQueued
	case "processing", "in_progress", "running":
		result.Status = model.TaskStatusInProgress
	case "completed", "succeeded", "success", "done":
		result.Status = model.TaskStatusSuccess
	case "failed", "cancelled", "canceled", "expired":
		result.Status = model.TaskStatusFailure
	default:
		result.Status = model.TaskStatusInProgress
	}
	if video.Seconds != "" {
		if duration, parseErr := strconv.Atoi(video.Seconds); parseErr == nil {
			result.Duration = int32(duration)
		}
	}
	if video.Error != nil {
		result.Reason = strings.TrimSpace(video.Error.Message)
		result.ErrorCode = strings.TrimSpace(video.Error.Code)
	}
	if strings.EqualFold(providerStatus, "expired") && result.Reason == "" {
		result.Reason = "xAI video request expired"
	}
	applyRESTMetadata(result, video.Metadata)
	applyRESTUsage(result, video.Usage)
	return result, nil
}

func applyRESTMetadata(result *relaycommon.TaskInfo, metadata map[string]any) {
	if result == nil || metadata == nil {
		return
	}
	result.StorageError = stringValue(metadata["storage_error"])
	result.UpstreamFileID = stringValue(metadata["upstream_file_id"])
	result.UpstreamPublicURL = stringValue(metadata["upstream_public_url"])
	if value, ok := boolValue(metadata["respect_moderation"]); ok {
		result.RespectModeration = &value
	}
	if result.Duration == 0 {
		result.Duration = int32(int64Value(metadata["duration"]))
	}
}

func applyRESTUsage(result *relaycommon.TaskInfo, usage map[string]any) {
	if result == nil || usage == nil {
		return
	}
	result.PromptTokens = int(int64Value(usage["prompt_tokens"]))
	result.CompletionTokens = int(int64Value(usage["completion_tokens"]))
	result.TotalTokens = int(int64Value(usage["total_tokens"]))
	result.CostUSDTicks = int64Value(usage["cost_in_usd_ticks"])
}

func mergeRESTVideoResult(result, upstream *dto.OpenAIVideo) {
	if result == nil || upstream == nil {
		return
	}
	result.Object = firstNonEmpty(upstream.Object, result.Object)
	result.Model = firstNonEmpty(upstream.Model, result.Model)
	result.Seconds = firstNonEmpty(upstream.Seconds, result.Seconds)
	result.Size = firstNonEmpty(upstream.Size, result.Size)
	result.OriginalURL = firstNonEmpty(upstream.OriginalURL, result.OriginalURL)
	result.ExpiresAt = upstream.ExpiresAt
	result.Error = upstream.Error
	result.Usage = upstream.Usage
	for key, value := range upstream.Metadata {
		result.SetMetadata(key, value)
	}
}

func restVideoURL(video *dto.OpenAIVideo) string {
	if video == nil {
		return ""
	}
	if video.Metadata != nil {
		if value := stringValue(video.Metadata["url"]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(video.OriginalURL)
}

func restFileURL(file *restVideoFile) string {
	if file == nil {
		return ""
	}
	return firstNonEmpty(file.URL, file.PublicURL)
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint:
		return int64(typed)
	case uint64:
		if typed > uint64(^uint64(0)>>1) {
			return 0
		}
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		result, _ := typed.Int64()
		return result
	case string:
		result, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return result
	default:
		return 0
	}
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		result, err := strconv.ParseBool(strings.TrimSpace(typed))
		return result, err == nil
	default:
		return false, false
	}
}
