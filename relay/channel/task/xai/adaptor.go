package xai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/internal/xaiapi"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/xaifile"
	"github.com/gin-gonic/gin"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	operationGenerate       = "generate"
	operationExtend         = "extend"
	defaultGenerateDuration = 8
	defaultExtendDuration   = 6
	unsupportedCapability   = "unsupported_upstream_capability"
)

var jsonMarshal = protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: false}
var jsonUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}

type requestEnvelope struct {
	Operation string          `json:"operation"`
	Payload   json.RawMessage `json:"payload"`
}

type submitRecord struct {
	RequestID   string `json:"request_id"`
	Model       string `json:"model"`
	Operation   string `json:"operation"`
	Duration    int    `json:"duration,omitempty"`
	AspectRatio string `json:"aspect_ratio,omitempty"`
	Resolution  string `json:"resolution,omitempty"`
}

func newSubmitRecord(c *gin.Context, info *relaycommon.RelayInfo, requestID string) submitRecord {
	request, _ := relaycommon.GetTaskRequest(c)
	operation := c.GetString("xai_video_operation")
	if operation == "" {
		operation = operationGenerate
	}
	modelName := request.Model
	if info != nil {
		modelName = firstNonEmpty(info.UpstreamModelName, modelName, ModelList[0])
	}
	return submitRecord{
		RequestID:   requestID,
		Model:       modelName,
		Operation:   operation,
		Duration:    request.Duration,
		AspectRatio: request.AspectRatio,
		Resolution:  request.Resolution,
	}
}

func applySubmitRecord(video *dto.OpenAIVideo, record submitRecord) {
	if video == nil {
		return
	}
	if record.Duration > 0 {
		video.Seconds = strconv.Itoa(record.Duration)
		video.SetMetadata("duration", record.Duration)
	}
	if record.Resolution != "" {
		video.Size = record.Resolution
	}
	if record.AspectRatio != "" {
		video.SetMetadata("aspect_ratio", record.AspectRatio)
	}
	if record.Operation != "" {
		video.SetMetadata("operation", record.Operation)
	}
}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	baseURL   string
	apiKey    string
	transport videoTransport
	operation string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.baseURL = info.ChannelBaseUrl
	a.apiKey = info.ApiKey
	a.transport = selectVideoTransport(a.baseURL)
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	var req relaycommon.TaskSubmitReq
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	normalizeRequest(&req)
	operation := requestOperation(c, info, &req)
	a.operation = operation

	if strings.TrimSpace(req.Prompt) == "" {
		return service.TaskErrorWrapperLocal(errors.New("field prompt is required"), "invalid_request", http.StatusBadRequest)
	}
	if req.Model == "" {
		req.Model = info.OriginModelName
	}
	if req.Model == "" {
		req.Model = ModelList[0]
	}
	if a.usesUniAPI() {
		switch {
		case operation == operationExtend:
			return uniAPIUnsupportedCapability("UniAPI REST does not support video extension")
		case hasVideoInput(&req):
			return uniAPIUnsupportedCapability("UniAPI REST does not support video editing")
		case len(req.ReferenceImages) > 0 || len(req.ReferenceImageFileIDs) > 0:
			return uniAPIUnsupportedCapability("UniAPI REST does not support reference images")
		}
	}
	if operation == operationExtend {
		info.Action = constant.TaskActionExtend
		if !hasVideoInput(&req) && info.OriginTaskID == "" {
			return service.TaskErrorWrapperLocal(errors.New("video_url or video_file_id is required for extension"), "invalid_request", http.StatusBadRequest)
		}
		if req.Duration != 0 && (req.Duration < 1 || req.Duration > 10) {
			return service.TaskErrorWrapperLocal(errors.New("extension duration must be between 1 and 10 seconds"), "invalid_request", http.StatusBadRequest)
		}
	} else {
		if a.usesUniAPI() && req.Duration != 0 && (req.Duration < 1 || req.Duration > 15) {
			return service.TaskErrorWrapperLocal(errors.New("duration must be between 1 and 15 seconds for UniAPI"), "invalid_request", http.StatusBadRequest)
		}
		if !a.usesUniAPI() && req.Duration != 0 && (req.Duration < 1 || req.Duration > 15) {
			return service.TaskErrorWrapperLocal(errors.New("duration must be between 1 and 15 seconds"), "invalid_request", http.StatusBadRequest)
		}
		switch {
		case hasVideoInput(&req):
			info.Action = constant.TaskActionEdit
		case len(req.ReferenceImages) > 0 || len(req.ReferenceImageFileIDs) > 0:
			info.Action = constant.TaskActionReferenceGenerate
		case hasImageInput(&req):
			info.Action = constant.TaskActionFirstTailGenerate
		default:
			info.Action = constant.TaskActionTextGenerate
		}
	}
	if req.ImageURL != "" && req.ImageFileID != "" {
		return service.TaskErrorWrapperLocal(errors.New("image_url and image_file_id cannot be set together"), "invalid_request", http.StatusBadRequest)
	}
	if req.VideoURL != "" && req.VideoFileID != "" {
		return service.TaskErrorWrapperLocal(errors.New("video_url and video_file_id cannot be set together"), "invalid_request", http.StatusBadRequest)
	}
	if err := validateAspectRatio(req.AspectRatio); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if err := validateResolution(req.Resolution); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if req.StorageOptions != nil {
		if strings.TrimSpace(req.StorageOptions.Filename) == "" {
			return service.TaskErrorWrapperLocal(errors.New("storage_options.filename is required"), "invalid_request", http.StatusBadRequest)
		}
		if req.StorageOptions.ExpiresAfter != nil && *req.StorageOptions.ExpiresAfter <= 0 {
			return service.TaskErrorWrapperLocal(errors.New("storage_options.expires_after must be positive"), "invalid_request", http.StatusBadRequest)
		}
		if req.StorageOptions.PublicURL != nil && req.StorageOptions.PublicURL.ExpiresAfter != nil && *req.StorageOptions.PublicURL.ExpiresAfter <= 0 {
			return service.TaskErrorWrapperLocal(errors.New("storage_options.public_url.expires_after must be positive"), "invalid_request", http.StatusBadRequest)
		}
	}
	c.Set("task_request", req)
	c.Set("xai_video_operation", operation)
	return nil
}

func (a *TaskAdaptor) EstimateBilling(c *gin.Context, _ *relaycommon.RelayInfo) map[string]float64 {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	duration := req.Duration
	if duration <= 0 {
		if c.GetString("xai_video_operation") == operationExtend {
			duration = defaultExtendDuration
		} else if a.usesUniAPI() {
			duration = defaultUniAPIDuration
		} else {
			duration = defaultGenerateDuration
		}
	}
	return map[string]float64{"seconds": float64(duration)}
}

func (a *TaskAdaptor) AdjustBillingOnSubmit(_ *relaycommon.RelayInfo, _ []byte) map[string]float64 {
	return nil
}

func (a *TaskAdaptor) AdjustBillingOnComplete(task *model.Task, result *relaycommon.TaskInfo) int {
	if task == nil || result == nil || result.CostUSDTicks <= 0 {
		return 0
	}
	groupRatio := 1.0
	if task.PrivateData.BillingContext != nil && task.PrivateData.BillingContext.GroupRatio > 0 {
		groupRatio = task.PrivateData.BillingContext.GroupRatio
	}
	costUSD := float64(result.CostUSDTicks) * 1e-10
	return int(math.Round(costUSD * common.QuotaPerUnit * groupRatio))
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	if a.usesREST() {
		return buildRESTSubmitURL(a.baseURL)
	}
	if strings.TrimSpace(info.ChannelBaseUrl) != "" {
		return info.ChannelBaseUrl, nil
	}
	return "https://api.x.ai", nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, request *http.Request, _ *relaycommon.RelayInfo) error {
	if a.usesREST() {
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(a.apiKey))
		request.Header.Set("Content-Type", "application/json")
		if c != nil && c.Request != nil {
			if requestID := strings.TrimSpace(c.GetString(common.RequestIdKey)); requestID != "" {
				request.Header.Set("X-Request-Id", requestID)
			}
		}
	}
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, err
	}
	operation := c.GetString("xai_video_operation")
	if operation == "" {
		operation = operationGenerate
	}
	a.operation = operation
	if a.usesREST() {
		if a.usesUniAPI() {
			if err = resolveUniAPIFileInputs(info.UserId, &req); err != nil {
				return nil, err
			}
			upstream, buildErr := buildUniAPIVideoGenerateRequest(&req, info, operation)
			if buildErr != nil {
				return nil, buildErr
			}
			req.Duration = upstream.Duration
			req.Resolution = upstream.Resolution
			req.AspectRatio = upstream.AspectRatio
			c.Set("task_request", req)
			body, marshalErr := common.Marshal(upstream)
			if marshalErr != nil {
				return nil, marshalErr
			}
			return bytes.NewReader(body), nil
		}
		if err = resolveLocalFileInputs(c.Request.Context(), info.UserId, &req); err != nil {
			return nil, err
		}
		req.Model = firstNonEmpty(info.UpstreamModelName, req.Model, ModelList[0])
		req.Operation = operation
		body, marshalErr := common.Marshal(req)
		if marshalErr != nil {
			return nil, marshalErr
		}
		return bytes.NewReader(body), nil
	}
	if err = resolveLocalFileInputs(c.Request.Context(), info.UserId, &req); err != nil {
		return nil, err
	}
	var payload []byte
	if operation == operationExtend {
		upstream, err := buildExtendRequest(&req, info)
		if err != nil {
			return nil, err
		}
		payload, err = jsonMarshal.Marshal(upstream)
		if err != nil {
			return nil, err
		}
	} else {
		upstream, err := buildGenerateRequest(&req, info)
		if err != nil {
			return nil, err
		}
		payload, err = jsonMarshal.Marshal(upstream)
		if err != nil {
			return nil, err
		}
	}
	body, err := common.Marshal(requestEnvelope{Operation: operation, Payload: payload})
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(body), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	if a.usesREST() {
		return channel.DoTaskApiRequest(a, c, info, requestBody)
	}
	body, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, err
	}
	var envelope requestEnvelope
	if err = common.Unmarshal(body, &envelope); err != nil {
		return nil, err
	}
	client, err := newVideoClient(a.baseURL)
	if err != nil {
		return nil, err
	}
	parent := context.Background()
	if c != nil && c.Request != nil {
		parent = c.Request.Context()
	}
	ctx, cancel := outgoingContext(parent, a.apiKey)
	defer cancel()

	var response proto.Message
	if envelope.Operation == operationExtend {
		var request xaiapi.ExtendVideoRequest
		if err = jsonUnmarshal.Unmarshal(envelope.Payload, &request); err != nil {
			return nil, err
		}
		response, err = client.ExtendVideo(ctx, &request)
	} else {
		var request xaiapi.GenerateVideoRequest
		if err = jsonUnmarshal.Unmarshal(envelope.Payload, &request); err != nil {
			return nil, err
		}
		response, err = client.GenerateVideo(ctx, &request)
	}
	if err != nil {
		return grpcFailureResponse(err), nil
	}
	responseBody, err := jsonMarshal.Marshal(response)
	if err != nil {
		return nil, err
	}
	return jsonHTTPResponse(http.StatusOK, responseBody), nil
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	if a.usesREST() {
		return a.doRESTResponse(c, resp, info)
	}
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()
	var upstream xaiapi.StartDeferredResponse
	if err = jsonUnmarshal.Unmarshal(responseBody, &upstream); err != nil {
		return "", nil, service.TaskErrorWrapper(err, "unmarshal_response_body_failed", http.StatusBadGateway)
	}
	if strings.TrimSpace(upstream.RequestId) == "" {
		return "", nil, service.TaskErrorWrapper(errors.New("xAI request_id is empty"), "invalid_response", http.StatusBadGateway)
	}
	record := newSubmitRecord(c, info, upstream.RequestId)
	taskData, _ := common.Marshal(record)
	public := dto.NewOpenAIVideo()
	public.ID = info.PublicTaskID
	public.TaskID = info.PublicTaskID
	public.Model = info.OriginModelName
	public.Status = dto.VideoStatusQueued
	public.Progress = 0
	public.CreatedAt = common.GetTimestamp()
	applySubmitRecord(public, record)
	c.JSON(http.StatusOK, public)
	return upstream.RequestId, taskData, nil
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, _ := body["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("invalid task_id")
	}
	if selectVideoTransport(baseURL) == videoTransportREST {
		return fetchRESTTask(baseURL, key, taskID, proxy)
	}
	client, err := newVideoClient(baseURL)
	if err != nil {
		return nil, err
	}
	ctx, cancel := outgoingContext(context.Background(), key)
	defer cancel()
	response, err := client.GetDeferredVideo(ctx, &xaiapi.GetDeferredVideoRequest{RequestId: taskID})
	if err != nil {
		return grpcFailureResponse(err), nil
	}
	responseBody, err := jsonMarshal.Marshal(response)
	if err != nil {
		return nil, err
	}
	return jsonHTTPResponse(http.StatusOK, responseBody), nil
}

func (a *TaskAdaptor) ParseTaskResult(responseBody []byte) (*relaycommon.TaskInfo, error) {
	if a.usesREST() {
		if a.usesUniAPI() {
			return parseUniAPITaskResult(responseBody)
		}
		return parseRESTTaskResult(responseBody)
	}
	var response xaiapi.GetDeferredVideoResponse
	if err := jsonUnmarshal.Unmarshal(responseBody, &response); err != nil {
		return nil, err
	}
	result := &relaycommon.TaskInfo{
		Code:           0,
		ProviderStatus: response.Status.String(),
	}
	switch response.Status {
	case xaiapi.DeferredStatus_PENDING, xaiapi.DeferredStatus_INVALID_DEFERRED_STATUS:
		result.Status = model.TaskStatusInProgress
	case xaiapi.DeferredStatus_DONE:
		result.Status = model.TaskStatusSuccess
	case xaiapi.DeferredStatus_FAILED, xaiapi.DeferredStatus_EXPIRED:
		result.Status = model.TaskStatusFailure
	default:
		result.Status = model.TaskStatusInProgress
	}
	if response.Status == xaiapi.DeferredStatus_EXPIRED {
		result.Reason = "xAI video request expired"
	}
	if response.Response == nil {
		return result, nil
	}
	videoResponse := response.Response
	if videoResponse.Progress > 0 {
		result.Progress = fmt.Sprintf("%d%%", videoResponse.Progress)
	}
	if videoResponse.Video != nil {
		result.Url = videoResponse.Video.Url
		result.RemoteUrl = videoResponse.Video.Url
		result.Duration = videoResponse.Video.Duration
		moderation := videoResponse.Video.RespectModeration
		result.RespectModeration = &moderation
		result.StorageError = videoResponse.Video.GetStorageError()
		if videoResponse.Video.FileOutput != nil {
			result.UpstreamFileID = videoResponse.Video.FileOutput.FileId
			result.UpstreamPublicURL = videoResponse.Video.FileOutput.GetPublicUrl()
		}
	}
	if videoResponse.Usage != nil {
		result.PromptTokens = int(videoResponse.Usage.PromptTokens)
		result.CompletionTokens = int(videoResponse.Usage.CompletionTokens)
		result.TotalTokens = int(videoResponse.Usage.TotalTokens)
		if videoResponse.Usage.CostInUsdTicks != nil {
			result.CostUSDTicks = *videoResponse.Usage.CostInUsdTicks
		}
	}
	if videoResponse.Error != nil {
		result.Reason = strings.TrimSpace(videoResponse.Error.Message)
		result.ErrorCode = strings.TrimSpace(videoResponse.Error.Code)
		if result.Reason == "" {
			result.Reason = result.ErrorCode
		}
	}
	return result, nil
}

func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	if task == nil {
		return nil, errors.New("task is nil")
	}
	result := dto.NewOpenAIVideo()
	result.ID = task.TaskID
	result.TaskID = task.TaskID
	result.Model = task.Properties.OriginModelName
	if result.Model == "" {
		result.Model = task.Properties.UpstreamModelName
	}
	result.Status = task.Status.ToVideoStatus()
	result.SetProgressStr(task.Progress)
	result.CreatedAt = task.CreatedAt
	result.CompletedAt = task.FinishTime
	if restVideo, recognized, _ := decodeRESTVideo(task.Data); recognized {
		mergeRESTVideoResult(result, restVideo)
	}

	var deferred xaiapi.GetDeferredVideoResponse
	if err := jsonUnmarshal.Unmarshal(task.Data, &deferred); err == nil && deferred.Response != nil {
		response := deferred.Response
		result.Model = firstNonEmpty(response.Model, result.Model)
		if response.Video != nil {
			result.Seconds = strconv.Itoa(int(response.Video.Duration))
			result.SetMetadata("duration", response.Video.Duration)
			result.SetMetadata("respect_moderation", response.Video.RespectModeration)
			if response.Video.FileOutput != nil {
				result.SetMetadata("upstream_file_id", response.Video.FileOutput.FileId)
			}
			if response.Video.GetStorageError() != "" {
				result.SetMetadata("storage_error", response.Video.GetStorageError())
			}
		}
		if response.Usage != nil {
			result.Usage = usageMap(response.Usage)
		}
		if response.Error != nil {
			result.Error = &dto.OpenAIVideoError{Code: response.Error.Code, Message: response.Error.Message}
		}
	} else {
		var submitted submitRecord
		if common.Unmarshal(task.Data, &submitted) == nil && submitted.RequestID != "" {
			applySubmitRecord(result, submitted)
		}
	}
	if task.PrivateData.XAI != nil {
		applySubmitRecord(result, submitRecord{
			Operation:   task.PrivateData.XAI.Operation,
			Duration:    task.PrivateData.XAI.Duration,
			AspectRatio: task.PrivateData.XAI.AspectRatio,
			Resolution:  task.PrivateData.XAI.Resolution,
		})
	}
	if task.Status == model.TaskStatusFailure && result.Error == nil {
		result.Error = &dto.OpenAIVideoError{Code: "video_generation_failed", Message: task.FailReason}
	}
	if url := task.GetResultURL(); url != "" {
		result.SetMetadata("url", url)
	}
	return common.Marshal(result)
}

func (a *TaskAdaptor) GetModelList() []string     { return ModelList }
func (a *TaskAdaptor) GetChannelName() string     { return ChannelName }
func (a *TaskAdaptor) UseActualCostBilling() bool { return true }

func (a *TaskAdaptor) usesREST() bool {
	return a.transport == videoTransportREST
}

func (a *TaskAdaptor) usesUniAPI() bool {
	return a.usesREST() && selectRESTVideoDialect(a.baseURL) == restVideoDialectUniAPI
}

func normalizeRequest(req *relaycommon.TaskSubmitReq) {
	req.Prompt = strings.TrimSpace(req.Prompt)
	req.Model = strings.TrimSpace(req.Model)
	req.Operation = strings.ToLower(strings.TrimSpace(req.Operation))
	req.ImageURL = strings.TrimSpace(req.ImageURL)
	req.ImageFileID = strings.TrimSpace(req.ImageFileID)
	req.VideoURL = strings.TrimSpace(req.VideoURL)
	req.VideoFileID = strings.TrimSpace(req.VideoFileID)
	req.AspectRatio = strings.TrimSpace(req.AspectRatio)
	req.Resolution = strings.ToLower(strings.TrimSpace(req.Resolution))
	if req.Output != nil {
		req.Output.UploadURL = strings.TrimSpace(req.Output.UploadURL)
	}
	if req.AspectRatio == "" {
		req.AspectRatio = strings.TrimSpace(req.Ratio)
	}
	if req.ImageURL == "" {
		req.ImageURL = strings.TrimSpace(req.Image)
	}
	if req.ImageURL == "" && len(req.Images) == 1 {
		req.ImageURL = strings.TrimSpace(req.Images[0])
	}
	if len(req.ReferenceImages) == 0 && len(req.Images) > 1 {
		for _, item := range req.Images {
			req.ReferenceImages = append(req.ReferenceImages, relaycommon.TaskMediaInput{ImageURL: strings.TrimSpace(item)})
		}
	}
}

func buildUniAPIVideoGenerateRequest(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo, operation string) (*uniAPIVideoGenerateRequest, error) {
	if operation != operationGenerate {
		return nil, errors.New("UniAPI REST does not support video extension")
	}
	if hasVideoInput(req) {
		return nil, errors.New("UniAPI REST does not support video editing")
	}
	if len(req.ReferenceImages) > 0 || len(req.ReferenceImageFileIDs) > 0 {
		return nil, errors.New("UniAPI REST does not support reference images")
	}
	if strings.TrimSpace(req.ImageFileID) != "" {
		return nil, errors.New("UniAPI REST image_file_id must resolve to a public URL")
	}

	duration := req.Duration
	if duration <= 0 {
		duration = defaultUniAPIDuration
	}
	if duration < 1 || duration > 15 {
		return nil, errors.New("UniAPI REST duration must be between 1 and 15 seconds")
	}
	resolution := strings.ToLower(strings.TrimSpace(req.Resolution))
	if resolution == "" {
		resolution = defaultUniAPIResolution
	}
	request := &uniAPIVideoGenerateRequest{
		Prompt:      strings.TrimSpace(req.Prompt),
		AspectRatio: strings.TrimSpace(req.AspectRatio),
		Duration:    duration,
		Model:       firstNonEmpty(info.UpstreamModelName, req.Model, ModelList[0]),
		Resolution:  resolution,
	}
	if imageURL := strings.TrimSpace(req.ImageURL); imageURL != "" {
		request.Image = &uniAPIVideoImage{URL: imageURL}
	}
	if req.Output != nil && strings.TrimSpace(req.Output.UploadURL) != "" {
		request.Output = &uniAPIVideoOutput{UploadURL: strings.TrimSpace(req.Output.UploadURL)}
	}
	return request, nil
}

func uniAPIUnsupportedCapability(message string) *dto.TaskError {
	return service.TaskErrorWrapperLocal(errors.New(message), unsupportedCapability, http.StatusUnprocessableEntity)
}

func resolveUniAPIFileInputs(userID int, req *relaycommon.TaskSubmitReq) error {
	if req == nil || !xaifile.IsLocalFileID(req.ImageFileID) {
		return nil
	}
	file, err := xaifile.CreatePublicURL(userID, req.ImageFileID, "", nil)
	if err != nil {
		return fmt.Errorf("create public URL for image_file_id: %w", err)
	}
	req.ImageURL = strings.TrimSpace(file.PublicURL)
	req.ImageFileID = ""
	return nil
}

func requestOperation(c *gin.Context, info *relaycommon.RelayInfo, req *relaycommon.TaskSubmitReq) string {
	path := ""
	if c != nil && c.Request != nil && c.Request.URL != nil {
		path = c.Request.URL.Path
	}
	if info.Action == constant.TaskActionExtend || req.Operation == operationExtend || strings.HasSuffix(path, "/extend") || strings.HasSuffix(path, "/extensions") {
		return operationExtend
	}
	return operationGenerate
}

func hasImageInput(req *relaycommon.TaskSubmitReq) bool {
	return req.ImageURL != "" || req.ImageFileID != ""
}

func hasVideoInput(req *relaycommon.TaskSubmitReq) bool {
	return req.VideoURL != "" || req.VideoFileID != ""
}

func resolveLocalFileInputs(ctx context.Context, userID int, req *relaycommon.TaskSubmitReq) error {
	if req == nil {
		return nil
	}
	resolve := func(fileID string) (string, error) {
		if !xaifile.IsLocalFileID(fileID) {
			return "", nil
		}
		return xaifile.DataURLForUser(ctx, userID, fileID)
	}
	if dataURL, err := resolve(req.ImageFileID); err != nil {
		return fmt.Errorf("resolve image_file_id: %w", err)
	} else if dataURL != "" {
		req.ImageURL = dataURL
		req.ImageFileID = ""
	}
	if dataURL, err := resolve(req.VideoFileID); err != nil {
		return fmt.Errorf("resolve video_file_id: %w", err)
	} else if dataURL != "" {
		req.VideoURL = dataURL
		req.VideoFileID = ""
	}
	for index := range req.ReferenceImages {
		dataURL, err := resolve(req.ReferenceImages[index].FileID)
		if err != nil {
			return fmt.Errorf("resolve reference_images[%d].file_id: %w", index, err)
		}
		if dataURL != "" {
			req.ReferenceImages[index].ImageURL = dataURL
			req.ReferenceImages[index].FileID = ""
		}
	}
	for _, fileID := range req.ReferenceImageFileIDs {
		dataURL, err := resolve(fileID)
		if err != nil {
			return fmt.Errorf("resolve reference_image_file_ids: %w", err)
		}
		if dataURL == "" {
			req.ReferenceImages = append(req.ReferenceImages, relaycommon.TaskMediaInput{FileID: fileID})
		} else {
			req.ReferenceImages = append(req.ReferenceImages, relaycommon.TaskMediaInput{ImageURL: dataURL})
		}
	}
	req.ReferenceImageFileIDs = nil
	return nil
}

func buildGenerateRequest(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*xaiapi.GenerateVideoRequest, error) {
	request := &xaiapi.GenerateVideoRequest{Prompt: req.Prompt, Model: info.UpstreamModelName}
	if request.Model == "" {
		request.Model = req.Model
	}
	if req.Duration > 0 {
		duration := int32(req.Duration)
		request.Duration = &duration
	}
	if req.ImageURL != "" || req.ImageFileID != "" {
		request.Image = imageInput(req.ImageURL, req.ImageFileID, "auto")
	}
	if req.VideoURL != "" || req.VideoFileID != "" {
		request.Video = videoInput(req.VideoURL, req.VideoFileID)
	}
	for _, reference := range req.ReferenceImages {
		request.ReferenceImages = append(request.ReferenceImages, imageInput(reference.SourceURL(), reference.FileID, reference.Detail))
	}
	for _, fileID := range req.ReferenceImageFileIDs {
		request.ReferenceImages = append(request.ReferenceImages, imageInput("", fileID, "auto"))
	}
	if req.AspectRatio != "" {
		value, _ := aspectRatioValue(req.AspectRatio)
		request.AspectRatio = &value
	}
	if req.Resolution != "" {
		value, _ := resolutionValue(req.Resolution)
		request.Resolution = &value
	}
	request.StorageOptions = storageOptions(req.StorageOptions)
	return request, nil
}

func buildExtendRequest(req *relaycommon.TaskSubmitReq, info *relaycommon.RelayInfo) (*xaiapi.ExtendVideoRequest, error) {
	videoURL, videoFileID := req.VideoURL, req.VideoFileID
	if videoURL == "" && videoFileID == "" && info.OriginTaskID != "" {
		originTask, exists, err := model.GetByTaskId(info.UserId, info.OriginTaskID)
		if err != nil {
			return nil, err
		}
		if !exists {
			return nil, errors.New("origin task does not exist")
		}
		videoURL = originTask.GetResultURL()
	}
	if videoURL == "" && videoFileID == "" {
		return nil, errors.New("video input is required for extension")
	}
	request := &xaiapi.ExtendVideoRequest{
		Prompt:         req.Prompt,
		Model:          info.UpstreamModelName,
		Video:          videoInput(videoURL, videoFileID),
		StorageOptions: storageOptions(req.StorageOptions),
	}
	if request.Model == "" {
		request.Model = req.Model
	}
	if req.Duration > 0 {
		duration := int32(req.Duration)
		request.Duration = &duration
	}
	return request, nil
}

func imageInput(rawURL, fileID, detail string) *xaiapi.ImageUrlContent {
	input := &xaiapi.ImageUrlContent{Detail: imageDetailValue(detail)}
	if strings.TrimSpace(fileID) != "" {
		input.Source = &xaiapi.ImageUrlContent_FileId{FileId: strings.TrimSpace(fileID)}
	} else {
		input.Source = &xaiapi.ImageUrlContent_ImageUrl{ImageUrl: strings.TrimSpace(rawURL)}
	}
	return input
}

func videoInput(rawURL, fileID string) *xaiapi.VideoUrlContent {
	input := &xaiapi.VideoUrlContent{}
	if strings.TrimSpace(fileID) != "" {
		input.Source = &xaiapi.VideoUrlContent_FileId{FileId: strings.TrimSpace(fileID)}
	} else {
		input.Source = &xaiapi.VideoUrlContent_Url{Url: strings.TrimSpace(rawURL)}
	}
	return input
}

func storageOptions(options *relaycommon.TaskStorageOptions) *xaiapi.StorageOptions {
	if options == nil {
		return nil
	}
	result := &xaiapi.StorageOptions{Filename: strings.TrimSpace(options.Filename), ExpiresAfter: options.ExpiresAfter}
	if options.PublicURL != nil {
		result.PublicUrl = &xaiapi.PublicUrlOptions{ExpiresAfter: options.PublicURL.ExpiresAfter}
	}
	return result
}

func imageDetailValue(detail string) xaiapi.ImageDetail {
	switch strings.ToLower(strings.TrimSpace(detail)) {
	case "low":
		return xaiapi.ImageDetail_DETAIL_LOW
	case "high":
		return xaiapi.ImageDetail_DETAIL_HIGH
	default:
		return xaiapi.ImageDetail_DETAIL_AUTO
	}
}

func validateAspectRatio(value string) error {
	if value == "" {
		return nil
	}
	_, ok := aspectRatioValue(value)
	if !ok {
		return fmt.Errorf("aspect_ratio must be one of 1:1, 16:9, 9:16, 4:3, 3:4, 3:2, 2:3")
	}
	return nil
}

func aspectRatioValue(value string) (xaiapi.VideoAspectRatio, bool) {
	values := map[string]xaiapi.VideoAspectRatio{
		"1:1":  xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_1_1,
		"16:9": xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_16_9,
		"9:16": xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_9_16,
		"4:3":  xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_4_3,
		"3:4":  xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_3_4,
		"3:2":  xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_3_2,
		"2:3":  xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_2_3,
	}
	result, ok := values[strings.TrimSpace(value)]
	return result, ok
}

func validateResolution(value string) error {
	if value == "" {
		return nil
	}
	_, ok := resolutionValue(value)
	if !ok {
		return errors.New("resolution must be 480p or 720p")
	}
	return nil
}

func resolutionValue(value string) (xaiapi.VideoResolution, bool) {
	values := map[string]xaiapi.VideoResolution{
		"480p": xaiapi.VideoResolution_VIDEO_RESOLUTION_480P,
		"720p": xaiapi.VideoResolution_VIDEO_RESOLUTION_720P,
	}
	result, ok := values[strings.ToLower(strings.TrimSpace(value))]
	return result, ok
}

func grpcFailureResponse(err error) *http.Response {
	grpcStatus := status.Convert(err)
	statusCode := http.StatusBadGateway
	switch grpcStatus.Code() {
	case codes.InvalidArgument, codes.FailedPrecondition, codes.OutOfRange:
		statusCode = http.StatusBadRequest
	case codes.Unauthenticated:
		statusCode = http.StatusUnauthorized
	case codes.PermissionDenied:
		statusCode = http.StatusForbidden
	case codes.NotFound:
		statusCode = http.StatusNotFound
	case codes.ResourceExhausted:
		statusCode = http.StatusTooManyRequests
	case codes.DeadlineExceeded:
		statusCode = http.StatusGatewayTimeout
	case codes.Unavailable:
		statusCode = http.StatusServiceUnavailable
	}
	body, _ := common.Marshal(map[string]any{"error": map[string]any{
		"code": grpcStatus.Code().String(), "message": grpcStatus.Message(),
	}})
	return jsonHTTPResponse(statusCode, body)
}

func jsonHTTPResponse(statusCode int, body []byte) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}

func usageMap(usage *xaiapi.SamplingUsage) map[string]any {
	if usage == nil {
		return nil
	}
	result := map[string]any{
		"prompt_tokens":             usage.PromptTokens,
		"completion_tokens":         usage.CompletionTokens,
		"total_tokens":              usage.TotalTokens,
		"reasoning_tokens":          usage.ReasoningTokens,
		"prompt_text_tokens":        usage.PromptTextTokens,
		"cached_prompt_text_tokens": usage.CachedPromptTextTokens,
		"prompt_image_tokens":       usage.PromptImageTokens,
		"num_sources_used":          usage.NumSourcesUsed,
	}
	if len(usage.ServerSideToolsUsed) > 0 {
		result["server_side_tools_used"] = usage.ServerSideToolsUsed
	}
	if usage.CostInUsdTicks != nil {
		result["cost_in_usd_ticks"] = *usage.CostInUsdTicks
		result["cost_usd"] = float64(*usage.CostInUsdTicks) * 1e-10
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var _ channel.TaskAdaptor = (*TaskAdaptor)(nil)
var _ channel.OpenAIVideoConverter = (*TaskAdaptor)(nil)
