package xaigrpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/internal/xaiapi"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/xaifile"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type videoServer struct {
	xaiapi.UnimplementedVideoServer
	bridge *httpBridge
}

func (s *videoServer) GenerateVideo(ctx context.Context, request *xaiapi.GenerateVideoRequest) (*xaiapi.StartDeferredResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	payload := generateVideoPayload(request)
	var response dto.OpenAIVideo
	if err := s.bridge.invokeJSON(ctx, http.MethodPost, "/v1/videos", payload, &response); err != nil {
		return nil, err
	}
	requestID := firstNonEmpty(response.ID, response.TaskID)
	if requestID == "" {
		return nil, status.Error(codes.Internal, "video submission returned no request ID")
	}
	return &xaiapi.StartDeferredResponse{RequestId: requestID}, nil
}

func (s *videoServer) ExtendVideo(ctx context.Context, request *xaiapi.ExtendVideoRequest) (*xaiapi.StartDeferredResponse, error) {
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	payload := extendVideoPayload(request)
	var response dto.OpenAIVideo
	if err := s.bridge.invokeJSON(ctx, http.MethodPost, "/v1/videos/extensions", payload, &response); err != nil {
		return nil, err
	}
	requestID := firstNonEmpty(response.ID, response.TaskID)
	if requestID == "" {
		return nil, status.Error(codes.Internal, "video extension returned no request ID")
	}
	return &xaiapi.StartDeferredResponse{RequestId: requestID}, nil
}

func (s *videoServer) GetDeferredVideo(ctx context.Context, request *xaiapi.GetDeferredVideoRequest) (*xaiapi.GetDeferredVideoResponse, error) {
	if request == nil || strings.TrimSpace(request.RequestId) == "" {
		return nil, status.Error(codes.InvalidArgument, "request_id is required")
	}
	path := "/v1/videos/" + url.PathEscape(strings.TrimSpace(request.RequestId))
	recorder, err := s.bridge.invoke(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	body, err := readRecorderBody(recorder)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read video status response: %v", err)
	}
	if recorder.Code < 200 || recorder.Code >= 300 {
		return nil, grpcErrorFromHTTP(recorder.Code, body)
	}
	var public dto.OpenAIVideo
	if err = json.Unmarshal(body, &public); err != nil {
		return nil, status.Errorf(codes.Internal, "decode video status response: %v", err)
	}
	response := &xaiapi.GetDeferredVideoResponse{Status: deferredStatus(public.Status)}
	if response.Status == xaiapi.DeferredStatus_PENDING {
		return response, nil
	}

	task, err := getTaskAfterAuthorizedFetch(request.RequestId)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "load video task: %v", err)
	}
	if task.PrivateData.XAI != nil && task.PrivateData.XAI.DeferredStatus == xaiapi.DeferredStatus_EXPIRED.String() {
		response.Status = xaiapi.DeferredStatus_EXPIRED
	}
	videoResponse := taskVideoResponse(task, &public)
	if response.Status == xaiapi.DeferredStatus_DONE && videoResponse.Video != nil {
		if file, fileErr := xaifile.EnsureTaskOutput(ctx, task, publicBaseURL(ctx)); fileErr != nil {
			message := fileErr.Error()
			videoResponse.Video.StorageError = &message
		} else if file != nil {
			videoResponse.Video.FileOutput = xaiFileOutput(file)
		}
	}
	response.Response = videoResponse
	return response, nil
}

func generateVideoPayload(request *xaiapi.GenerateVideoRequest) map[string]any {
	payload := map[string]any{
		"prompt": request.GetPrompt(),
		"model":  request.GetModel(),
	}
	if request.Duration != nil {
		payload["duration"] = request.GetDuration()
	}
	if request.Image != nil {
		setImageInput(payload, "image_url", "image_file_id", request.Image)
	}
	if request.Video != nil {
		setVideoInput(payload, request.Video)
	}
	if request.AspectRatio != nil {
		payload["aspect_ratio"] = aspectRatioString(request.GetAspectRatio())
	}
	if request.Resolution != nil {
		payload["resolution"] = resolutionString(request.GetResolution())
	}
	if len(request.ReferenceImages) > 0 {
		references := make([]map[string]any, 0, len(request.ReferenceImages))
		for _, input := range request.ReferenceImages {
			item := map[string]any{"detail": imageDetailString(input.GetDetail())}
			if input.GetFileId() != "" {
				item["file_id"] = input.GetFileId()
			} else {
				item["image_url"] = input.GetImageUrl()
			}
			references = append(references, item)
		}
		payload["reference_images"] = references
	}
	if request.StorageOptions != nil {
		payload["storage_options"] = storageOptionsPayload(request.StorageOptions)
	}
	return payload
}

func extendVideoPayload(request *xaiapi.ExtendVideoRequest) map[string]any {
	payload := map[string]any{
		"operation": "extend",
		"prompt":    request.GetPrompt(),
		"model":     request.GetModel(),
	}
	if request.Duration != nil {
		payload["duration"] = request.GetDuration()
	}
	if request.Video != nil {
		setVideoInput(payload, request.Video)
	}
	if request.StorageOptions != nil {
		payload["storage_options"] = storageOptionsPayload(request.StorageOptions)
	}
	return payload
}

func setImageInput(payload map[string]any, urlKey, fileKey string, input *xaiapi.ImageUrlContent) {
	if input.GetFileId() != "" {
		payload[fileKey] = input.GetFileId()
	} else {
		payload[urlKey] = input.GetImageUrl()
	}
}

func setVideoInput(payload map[string]any, input *xaiapi.VideoUrlContent) {
	if input.GetFileId() != "" {
		payload["video_file_id"] = input.GetFileId()
	} else {
		payload["video_url"] = input.GetUrl()
	}
}

func storageOptionsPayload(options *xaiapi.StorageOptions) map[string]any {
	payload := map[string]any{"filename": options.GetFilename()}
	if options.ExpiresAfter != nil {
		payload["expires_after"] = options.GetExpiresAfter()
	}
	if options.PublicUrl != nil {
		publicURL := map[string]any{}
		if options.PublicUrl.ExpiresAfter != nil {
			publicURL["expires_after"] = options.PublicUrl.GetExpiresAfter()
		}
		payload["public_url"] = publicURL
	}
	return payload
}

func deferredStatus(value string) xaiapi.DeferredStatus {
	switch value {
	case dto.VideoStatusCompleted:
		return xaiapi.DeferredStatus_DONE
	case dto.VideoStatusFailed:
		return xaiapi.DeferredStatus_FAILED
	default:
		return xaiapi.DeferredStatus_PENDING
	}
}

func getTaskAfterAuthorizedFetch(taskID string) (*model.Task, error) {
	var task model.Task
	if err := model.DB.Where("task_id = ?", strings.TrimSpace(taskID)).First(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

func taskVideoResponse(task *model.Task, public *dto.OpenAIVideo) *xaiapi.VideoResponse {
	result := &xaiapi.VideoResponse{}
	var upstream xaiapi.GetDeferredVideoResponse
	if task != nil && len(task.Data) > 0 {
		unmarshal := protojson.UnmarshalOptions{DiscardUnknown: true}
		if unmarshal.Unmarshal(task.Data, &upstream) == nil && upstream.Response != nil {
			result = upstream.Response
		}
	}
	if public != nil {
		result.Model = firstNonEmpty(public.Model, result.Model)
	}
	if task != nil {
		result.Model = firstNonEmpty(task.Properties.OriginModelName, result.Model)
	}
	if result.Video == nil {
		result.Video = &xaiapi.GeneratedVideo{}
	}
	if task != nil && task.Status == model.TaskStatusSuccess {
		result.Video.Url = task.GetResultURL()
	}
	if public != nil {
		if duration, err := strconv.Atoi(public.Seconds); err == nil && duration > 0 {
			result.Video.Duration = int32(duration)
		}
		result.Progress = int32(public.Progress)
		if result.Usage == nil {
			result.Usage = samplingUsage(public.Usage)
		}
		if public.Error != nil {
			result.Error = &xaiapi.VideoError{Code: public.Error.Code, Message: public.Error.Message}
		}
		if moderation, ok := boolMapValue(public.Metadata, "respect_moderation"); ok {
			result.Video.RespectModeration = moderation
		}
	}
	if task != nil && task.Status == model.TaskStatusFailure && result.Error == nil {
		result.Error = &xaiapi.VideoError{Code: "video_generation_failed", Message: task.FailReason}
	}
	if task != nil && task.PrivateData.XAI != nil && strings.TrimSpace(task.PrivateData.XAI.StorageError) != "" {
		message := task.PrivateData.XAI.StorageError
		result.Video.StorageError = &message
	}
	return result
}

func samplingUsage(values map[string]any) *xaiapi.SamplingUsage {
	if len(values) == 0 {
		return nil
	}
	usage := &xaiapi.SamplingUsage{
		PromptTokens:           int32(numberMapValue(values, "prompt_tokens")),
		CompletionTokens:       int32(numberMapValue(values, "completion_tokens")),
		TotalTokens:            int32(numberMapValue(values, "total_tokens")),
		ReasoningTokens:        int32(numberMapValue(values, "reasoning_tokens")),
		PromptTextTokens:       int32(numberMapValue(values, "prompt_text_tokens")),
		CachedPromptTextTokens: int32(numberMapValue(values, "cached_prompt_text_tokens")),
		PromptImageTokens:      int32(numberMapValue(values, "prompt_image_tokens")),
		NumSourcesUsed:         int32(numberMapValue(values, "num_sources_used")),
	}
	if ticks := numberMapValue(values, "cost_in_usd_ticks"); ticks > 0 {
		usage.CostInUsdTicks = &ticks
	}
	return usage
}

func xaiFileOutput(file *model.XAIFile) *xaiapi.FileOutput {
	if file == nil {
		return nil
	}
	output := &xaiapi.FileOutput{FileId: file.FileID, Filename: file.Filename}
	if file.ExpiresAt > 0 {
		output.ExpiresAt = timestamppb.New(time.Unix(file.ExpiresAt, 0))
	}
	if file.PublicURL != "" {
		publicURL := file.PublicURL
		output.PublicUrl = &publicURL
		if file.PublicURLExpiresAt > 0 {
			output.PublicUrlExpiresAt = timestamppb.New(time.Unix(file.PublicURLExpiresAt, 0))
		}
	}
	return output
}

func aspectRatioString(value xaiapi.VideoAspectRatio) string {
	return map[xaiapi.VideoAspectRatio]string{
		xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_1_1:  "1:1",
		xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_16_9: "16:9",
		xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_9_16: "9:16",
		xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_4_3:  "4:3",
		xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_3_4:  "3:4",
		xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_3_2:  "3:2",
		xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_2_3:  "2:3",
	}[value]
}

func resolutionString(value xaiapi.VideoResolution) string {
	if value == xaiapi.VideoResolution_VIDEO_RESOLUTION_720P {
		return "720p"
	}
	return "480p"
}

func imageDetailString(value xaiapi.ImageDetail) string {
	switch value {
	case xaiapi.ImageDetail_DETAIL_LOW:
		return "low"
	case xaiapi.ImageDetail_DETAIL_HIGH:
		return "high"
	default:
		return "auto"
	}
}

func numberMapValue(values map[string]any, key string) int64 {
	if values == nil {
		return 0
	}
	switch value := values[key].(type) {
	case float64:
		return int64(value)
	case int64:
		return value
	case json.Number:
		result, _ := value.Int64()
		return result
	}
	return 0
}

func boolMapValue(values map[string]any, key string) (bool, bool) {
	if values == nil {
		return false, false
	}
	value, ok := values[key].(bool)
	return value, ok
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
