package hailuo

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
)

// https://platform.minimaxi.com/docs/api-reference/video-generation-intro
type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
	huimengi    bool
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
	a.huimengi = isHuimengiBaseURL(info.ChannelBaseUrl)
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) (taskErr *dto.TaskError) {
	if taskErr = relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}
	if !isH3Model(h3ModelFromInfo(info)) {
		return nil
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if a.isHuimengiH3(info) {
		_, err = buildHuimengiH3Request(&req)
	} else {
		_, err = buildH3VideoRequest(&req, info)
	}
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	return nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if isH3Model(h3ModelFromInfo(info)) {
		if a.isHuimengiH3(info) {
			return huimengiH3CreateURL(a.baseURL), nil
		}
		return h3BaseURL(a.baseURL) + H3CreateEndpoint, nil
	}
	return strings.TrimRight(a.baseURL, "/") + TextToVideoEndpoint, nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	v, exists := c.Get("task_request")
	if !exists {
		return nil, fmt.Errorf("request not found in context")
	}
	req, ok := v.(relaycommon.TaskSubmitReq)
	if !ok {
		return nil, fmt.Errorf("invalid request type in context")
	}

	var body any
	var err error
	if isH3Model(h3ModelFromInfo(info)) {
		if a.isHuimengiH3(info) {
			body, err = buildHuimengiH3Request(&req)
		} else {
			body, err = buildH3VideoRequest(&req, info)
		}
	} else {
		body, err = a.convertToRequestPayload(&req, info)
	}
	if err != nil {
		return nil, errors.Wrap(err, "convert request payload failed")
	}

	data, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(data), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	if isH3Model(h3ModelFromInfo(info)) {
		if a.isHuimengiH3(info) {
			return a.doHuimengiResponse(c, responseBody, resp.StatusCode, info)
		}
		return a.doH3Response(c, responseBody, resp.StatusCode, info)
	}

	var hResp VideoResponse
	if err := common.Unmarshal(responseBody, &hResp); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}

	if hResp.BaseResp.StatusCode != StatusSuccess {
		taskErr = service.TaskErrorWrapper(
			fmt.Errorf("hailuo api error: %s", hResp.BaseResp.StatusMsg),
			strconv.Itoa(hResp.BaseResp.StatusCode),
			http.StatusBadRequest,
		)
		return
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName

	c.JSON(http.StatusOK, ov)
	return hResp.TaskID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid task_id")
	}

	var uri string
	if isH3Model(fmt.Sprint(body["model"])) {
		if isHuimengiBaseURL(baseUrl) {
			uri = huimengiH3QueryURL(baseUrl, taskID)
		} else {
			uri = fmt.Sprintf("%s%s/%s", h3BaseURL(baseUrl), H3QueryEndpoint, taskID)
		}
	} else {
		uri = fmt.Sprintf("%s%s?task_id=%s", strings.TrimRight(baseUrl, "/"), QueryTaskEndpoint, taskID)
	}

	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
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

func (a *TaskAdaptor) convertToRequestPayload(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*VideoRequest, error) {
	modelConfig := GetModelConfig(info.UpstreamModelName)
	duration := DefaultDuration
	if req.Duration > 0 {
		duration = req.Duration
	}
	resolution := modelConfig.DefaultResolution
	if req.Size != "" {
		resolution = a.parseResolutionFromSize(req.Size, modelConfig)
	}

	videoRequest := &VideoRequest{
		Model:      info.UpstreamModelName,
		Prompt:     req.Prompt,
		Duration:   &duration,
		Resolution: resolution,
	}
	if err := req.UnmarshalMetadata(&videoRequest); err != nil {
		return nil, errors.Wrap(err, "unmarshal metadata to video request failed")
	}

	return videoRequest, nil
}

func (a *TaskAdaptor) parseResolutionFromSize(size string, modelConfig ModelConfig) string {
	switch {
	case strings.Contains(size, "1080"):
		return Resolution1080P
	case strings.Contains(size, "768"):
		return Resolution768P
	case strings.Contains(size, "720"):
		return Resolution720P
	case strings.Contains(size, "512"):
		return Resolution512P
	default:
		return modelConfig.DefaultResolution
	}
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	if isHuimengiTaskResponse(respBody) {
		return parseHuimengiH3TaskResult(respBody)
	}
	if isH3TaskResponse(respBody) {
		return parseH3TaskResult(respBody)
	}
	if result, ok := parseH3APIError(respBody); ok {
		return result, nil
	}

	resTask := QueryTaskResponse{}
	if err := common.Unmarshal(respBody, &resTask); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}

	taskResult := relaycommon.TaskInfo{}

	if resTask.BaseResp.StatusCode == StatusSuccess {
		taskResult.Code = 0
	} else {
		taskResult.Code = resTask.BaseResp.StatusCode
		taskResult.Reason = resTask.BaseResp.StatusMsg
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
	}

	switch resTask.Status {
	case TaskStatusPreparing, TaskStatusQueueing, TaskStatusProcessing:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
		if resTask.Status == TaskStatusProcessing {
			taskResult.Progress = "50%"
		}
	case TaskStatusSuccess:
		taskResult.Status = model.TaskStatusSuccess
		taskResult.Progress = "100%"
		taskResult.Url = a.buildVideoURL(resTask.TaskID, resTask.FileID)
	case TaskStatusFailed:
		taskResult.Status = model.TaskStatusFailure
		taskResult.Progress = "100%"
		if taskResult.Reason == "" {
			taskResult.Reason = "task failed"
		}
	default:
		taskResult.Status = model.TaskStatusInProgress
		taskResult.Progress = "30%"
	}

	return &taskResult, nil
}

func (a *TaskAdaptor) doH3Response(c *gin.Context, responseBody []byte, responseStatus int, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	var hResp H3VideoResponse
	if err := common.Unmarshal(responseBody, &hResp); err != nil {
		return "", nil, service.TaskErrorWrapper(
			errors.Wrapf(err, "body: %s", responseBody),
			"unmarshal_response_body_failed",
			http.StatusInternalServerError,
		)
	}
	if strings.TrimSpace(hResp.TaskID) == "" {
		if errorResult, ok := parseH3APIError(responseBody); ok {
			statusCode := responseStatus
			if parsedStatus, err := strconv.Atoi(errorResult.ErrorCode); err == nil && parsedStatus >= 400 && parsedStatus <= 599 {
				statusCode = parsedStatus
			}
			if statusCode < 400 || statusCode > 599 {
				statusCode = http.StatusBadGateway
			}
			return "", nil, service.TaskErrorWrapper(
				fmt.Errorf("MiniMax H3 API error: %s", errorResult.Reason),
				errorResult.ErrorCode,
				statusCode,
			)
		}
		return "", nil, service.TaskErrorWrapper(
			fmt.Errorf("MiniMax H3 response did not contain task_id"),
			"invalid_upstream_response",
			http.StatusBadGateway,
		)
	}

	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName
	c.JSON(http.StatusOK, ov)
	return strings.TrimSpace(hResp.TaskID), responseBody, nil
}

func isH3TaskResponse(responseBody []byte) bool {
	var envelope map[string]any
	if common.Unmarshal(responseBody, &envelope) != nil {
		return false
	}
	task, ok := envelope["task"].(map[string]any)
	return ok && (task["id"] != nil || task["model"] != nil || task["status"] != nil)
}

func parseH3TaskResult(responseBody []byte) (*relaycommon.TaskInfo, error) {
	var response H3QueryTaskResponse
	if err := common.Unmarshal(responseBody, &response); err != nil {
		return nil, errors.Wrap(err, "unmarshal MiniMax H3 task result failed")
	}
	task := response.Task
	result := &relaycommon.TaskInfo{
		TaskID:           task.ID,
		ProviderStatus:   strings.ToLower(strings.TrimSpace(task.Status)),
		Duration:         int32(task.Duration),
		PromptTokens:     task.Usage.PromptTokens,
		CompletionTokens: task.Usage.CompletionTokens,
		TotalTokens:      task.Usage.TotalTokens,
		BillingUnits:     h3ActualBillingUnits(task),
	}
	if result.TotalTokens == 0 {
		result.TotalTokens = result.PromptTokens + result.CompletionTokens
	}

	switch result.ProviderStatus {
	case "queued":
		result.Status = model.TaskStatusQueued
		result.Progress = "20%"
	case "running":
		result.Status = model.TaskStatusInProgress
		result.Progress = "50%"
	case "succeeded":
		result.Status = model.TaskStatusSuccess
		result.Progress = "100%"
		result.Url = strings.TrimSpace(task.Content.URL)
		if result.Url == "" {
			return nil, fmt.Errorf("MiniMax H3 succeeded task did not contain content.url")
		}
	case "failed", "cancelled":
		result.Status = model.TaskStatusFailure
		result.Progress = "100%"
		if task.Error != nil {
			result.ErrorCode = strings.TrimSpace(task.Error.Code)
			result.Reason = strings.TrimSpace(task.Error.Message)
		}
		if result.Reason == "" {
			result.Reason = "MiniMax H3 task " + result.ProviderStatus
		}
	default:
		result.Status = model.TaskStatusInProgress
		result.Progress = "30%"
	}
	return result, nil
}

func parseH3APIError(responseBody []byte) (*relaycommon.TaskInfo, bool) {
	var response H3APIErrorResponse
	if common.Unmarshal(responseBody, &response) != nil || strings.TrimSpace(response.Error.Message) == "" {
		return nil, false
	}
	result := &relaycommon.TaskInfo{
		ErrorCode:      strings.TrimSpace(response.Error.HTTPCode),
		Reason:         strings.TrimSpace(response.Error.Message),
		ProviderStatus: strings.TrimSpace(response.Error.Type),
	}
	if strings.EqualFold(response.Error.Type, "rate_limit_error") || response.Error.HTTPCode == "429" {
		result.Status = model.TaskStatusInProgress
		result.Progress = "30%"
	} else {
		result.Status = model.TaskStatusFailure
		result.Progress = "100%"
	}
	return result, true
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	if originTask == nil {
		return nil, fmt.Errorf("task is required")
	}
	if isHuimengiTaskData(originTask.Data) {
		return a.convertHuimengiToOpenAIVideo(originTask)
	}
	if isH3Model(originTask.Properties.UpstreamModelName) || isH3Model(originTask.Properties.OriginModelName) {
		return a.convertH3ToOpenAIVideo(originTask)
	}

	var hailuoResp QueryTaskResponse
	if err := common.Unmarshal(originTask.Data, &hailuoResp); err != nil {
		return nil, errors.Wrap(err, "unmarshal hailuo task data failed")
	}

	openAIVideo := originTask.ToOpenAIVideo()
	if hailuoResp.BaseResp.StatusCode != StatusSuccess {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: hailuoResp.BaseResp.StatusMsg,
			Code:    strconv.Itoa(hailuoResp.BaseResp.StatusCode),
		}
	}

	jsonData, err := common.Marshal(openAIVideo)
	if err != nil {
		return nil, errors.Wrap(err, "marshal openai video failed")
	}

	return jsonData, nil
}

func (a *TaskAdaptor) doHuimengiResponse(c *gin.Context, responseBody []byte, responseStatus int, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	var response HuimengiH3SubmitResponse
	if err := common.Unmarshal(responseBody, &response); err != nil {
		return "", nil, service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusBadGateway)
	}
	taskID := strings.TrimSpace(response.TaskID)
	if taskID == "" {
		taskID = strings.TrimSpace(response.ID)
	}
	if taskID == "" {
		return "", nil, service.TaskErrorWrapper(fmt.Errorf("Huimengi response did not contain task_id"), "invalid_upstream_response", responseStatus)
	}
	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.CreatedAt = time.Now().Unix()
	ov.Model = info.OriginModelName
	c.JSON(http.StatusOK, ov)
	return taskID, responseBody, nil
}

func (a *TaskAdaptor) convertHuimengiToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	openAIVideo := originTask.ToOpenAIVideo()
	var response HuimengiH3TaskResponse
	if len(originTask.Data) > 0 {
		if err := common.Unmarshal(originTask.Data, &response); err != nil {
			return nil, errors.Wrap(err, "unmarshal Huimengi task data failed")
		}
	}
	if response.Result.Duration > 0 {
		openAIVideo.Seconds = strconv.Itoa(response.Result.Duration)
	}
	if response.Status != "" {
		openAIVideo.SetMetadata("provider_status", response.Status)
	}
	if response.Result.Resolution != "" {
		openAIVideo.SetMetadata("resolution", response.Result.Resolution)
	}
	if response.Result.Ratio != "" {
		openAIVideo.SetMetadata("ratio", response.Result.Ratio)
	}
	if response.ErrorMessage != "" {
		openAIVideo.Error = &dto.OpenAIVideoError{Message: response.ErrorMessage}
	}
	if response.Error != nil && openAIVideo.Error == nil {
		openAIVideo.Error = &dto.OpenAIVideoError{Message: response.Error.Message, Code: response.Error.Code}
	}
	if cost := parseHuimengiCost(response.Cost); cost > 0 {
		openAIVideo.Usage = map[string]any{
			"cost":          cost,
			"billing_units": huimengiCostToBillingUnits(cost),
		}
	}
	return common.Marshal(openAIVideo)
}

func (a *TaskAdaptor) convertH3ToOpenAIVideo(originTask *model.Task) ([]byte, error) {
	openAIVideo := originTask.ToOpenAIVideo()
	var response H3QueryTaskResponse
	if len(originTask.Data) > 0 {
		if err := common.Unmarshal(originTask.Data, &response); err != nil {
			return nil, errors.Wrap(err, "unmarshal MiniMax H3 task data failed")
		}
	}
	task := response.Task
	if task.Duration > 0 {
		openAIVideo.Seconds = strconv.Itoa(task.Duration)
	}
	if task.Error != nil {
		openAIVideo.Error = &dto.OpenAIVideoError{
			Message: strings.TrimSpace(task.Error.Message),
			Code:    strings.TrimSpace(task.Error.Code),
		}
	}
	if task.Status != "" {
		openAIVideo.SetMetadata("provider_status", task.Status)
	}
	if task.Resolution != "" {
		openAIVideo.SetMetadata("resolution", task.Resolution)
	}
	if task.Ratio != "" {
		openAIVideo.SetMetadata("ratio", task.Ratio)
	}
	if task.TaskType != "" {
		openAIVideo.SetMetadata("task_type", task.TaskType)
	}
	if task.Modality != "" {
		openAIVideo.SetMetadata("modality", task.Modality)
	}
	if task.Status == "succeeded" || task.Usage.TotalSeconds > 0 || task.Usage.TotalTokens > 0 {
		openAIVideo.Usage = map[string]any{
			"total_seconds":       task.Usage.TotalSeconds,
			"input_seconds":       task.Usage.InputSeconds,
			"output_seconds":      task.Usage.OutputSeconds,
			"input_image_count":   task.Usage.InputImageCount,
			"input_audio_seconds": task.Usage.InputAudioSeconds,
			"total_tokens":        task.Usage.TotalTokens,
			"prompt_tokens":       task.Usage.PromptTokens,
			"completion_tokens":   task.Usage.CompletionTokens,
		}
	}
	return common.Marshal(openAIVideo)
}

func (a *TaskAdaptor) buildVideoURL(_, fileID string) string {
	if a.apiKey == "" || a.baseURL == "" {
		return ""
	}

	url := fmt.Sprintf("%s/v1/files/retrieve?file_id=%s", a.baseURL, fileID)

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return ""
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := service.GetHttpClient().Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}

	var retrieveResp RetrieveFileResponse
	if err := common.Unmarshal(responseBody, &retrieveResp); err != nil {
		return ""
	}

	if retrieveResp.BaseResp.StatusCode != StatusSuccess {
		return ""
	}

	return retrieveResp.File.DownloadURL
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

func containsInt(slice []int, item int) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
