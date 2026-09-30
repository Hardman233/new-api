package xai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/internal/xaiapi"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/xaifile"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

type fakeVideoClient struct {
	generateRequest *xaiapi.GenerateVideoRequest
	extendRequest   *xaiapi.ExtendVideoRequest
	deferredRequest *xaiapi.GetDeferredVideoRequest
	generateResult  *xaiapi.StartDeferredResponse
	extendResult    *xaiapi.StartDeferredResponse
	deferredResult  *xaiapi.GetDeferredVideoResponse
	err             error
}

func (f *fakeVideoClient) GenerateVideo(_ context.Context, request *xaiapi.GenerateVideoRequest, _ ...grpc.CallOption) (*xaiapi.StartDeferredResponse, error) {
	f.generateRequest = request
	return f.generateResult, f.err
}

func (f *fakeVideoClient) ExtendVideo(_ context.Context, request *xaiapi.ExtendVideoRequest, _ ...grpc.CallOption) (*xaiapi.StartDeferredResponse, error) {
	f.extendRequest = request
	return f.extendResult, f.err
}

func (f *fakeVideoClient) GetDeferredVideo(_ context.Context, request *xaiapi.GetDeferredVideoRequest, _ ...grpc.CallOption) (*xaiapi.GetDeferredVideoResponse, error) {
	f.deferredRequest = request
	return f.deferredResult, f.err
}

func TestBuildGenerateRequestMapsOfficialCapabilities(t *testing.T) {
	expiresAfter := int64(3600)
	publicExpiresAfter := int64(600)
	request, err := buildGenerateRequest(&relaycommon.TaskSubmitReq{
		Prompt:      "make it move",
		Model:       ModelList[0],
		Duration:    15,
		ImageFileID: "file_image",
		ReferenceImages: []relaycommon.TaskMediaInput{
			{ImageURL: "https://example.com/reference.png", Detail: "high"},
			{FileID: "file_reference", Detail: "low"},
		},
		AspectRatio: "9:16",
		Resolution:  "720p",
		StorageOptions: &relaycommon.TaskStorageOptions{
			Filename:     "result.mp4",
			ExpiresAfter: &expiresAfter,
			PublicURL:    &relaycommon.TaskPublicURLOptions{ExpiresAfter: &publicExpiresAfter},
		},
	}, xaiRelayInfo())
	if err != nil {
		t.Fatalf("buildGenerateRequest() error = %v", err)
	}
	if request.GetPrompt() != "make it move" || request.GetModel() != ModelList[0] || request.GetDuration() != 15 {
		t.Fatalf("unexpected basic request fields: %+v", request)
	}
	if request.GetImage().GetFileId() != "file_image" {
		t.Fatalf("image file id = %q", request.GetImage().GetFileId())
	}
	if len(request.GetReferenceImages()) != 2 {
		t.Fatalf("reference image count = %d", len(request.GetReferenceImages()))
	}
	if request.GetReferenceImages()[0].GetImageUrl() != "https://example.com/reference.png" || request.GetReferenceImages()[0].GetDetail() != xaiapi.ImageDetail_DETAIL_HIGH {
		t.Fatalf("unexpected URL reference: %+v", request.GetReferenceImages()[0])
	}
	if request.GetReferenceImages()[1].GetFileId() != "file_reference" || request.GetReferenceImages()[1].GetDetail() != xaiapi.ImageDetail_DETAIL_LOW {
		t.Fatalf("unexpected file reference: %+v", request.GetReferenceImages()[1])
	}
	if request.GetAspectRatio() != xaiapi.VideoAspectRatio_VIDEO_ASPECT_RATIO_9_16 || request.GetResolution() != xaiapi.VideoResolution_VIDEO_RESOLUTION_720P {
		t.Fatalf("unexpected output shape: ratio=%v resolution=%v", request.GetAspectRatio(), request.GetResolution())
	}
	if request.GetStorageOptions().GetFilename() != "result.mp4" || request.GetStorageOptions().GetExpiresAfter() != expiresAfter {
		t.Fatalf("unexpected storage options: %+v", request.GetStorageOptions())
	}
	if request.GetStorageOptions().GetPublicUrl().GetExpiresAfter() != publicExpiresAfter {
		t.Fatalf("unexpected public URL options: %+v", request.GetStorageOptions().GetPublicUrl())
	}
}

func TestBuildGenerateRequestSupportsImageURLAndVideoEditing(t *testing.T) {
	imageRequest, err := buildGenerateRequest(&relaycommon.TaskSubmitReq{
		Prompt:   "animate",
		ImageURL: "data:image/png;base64,AAAA",
	}, xaiRelayInfo())
	if err != nil {
		t.Fatalf("image request error = %v", err)
	}
	if imageRequest.GetImage().GetImageUrl() != "data:image/png;base64,AAAA" {
		t.Fatalf("image URL = %q", imageRequest.GetImage().GetImageUrl())
	}

	editRequest, err := buildGenerateRequest(&relaycommon.TaskSubmitReq{
		Prompt:      "change the sky",
		VideoFileID: "file_video",
	}, xaiRelayInfo())
	if err != nil {
		t.Fatalf("edit request error = %v", err)
	}
	if editRequest.GetVideo().GetFileId() != "file_video" {
		t.Fatalf("video file id = %q", editRequest.GetVideo().GetFileId())
	}
}

func TestBuildExtendRequestMapsOfficialCapabilities(t *testing.T) {
	request, err := buildExtendRequest(&relaycommon.TaskSubmitReq{
		Prompt:         "continue the camera movement",
		VideoURL:       "https://example.com/source.mp4",
		Duration:       10,
		StorageOptions: &relaycommon.TaskStorageOptions{Filename: "extended.mp4"},
	}, xaiRelayInfo())
	if err != nil {
		t.Fatalf("buildExtendRequest() error = %v", err)
	}
	if request.GetVideo().GetUrl() != "https://example.com/source.mp4" || request.GetDuration() != 10 {
		t.Fatalf("unexpected extension request: %+v", request)
	}
	if request.GetStorageOptions().GetFilename() != "extended.mp4" {
		t.Fatalf("storage filename = %q", request.GetStorageOptions().GetFilename())
	}
}

func TestDoRequestUsesOfficialVideoRPCs(t *testing.T) {
	originalFactory := newVideoClient
	t.Cleanup(func() { newVideoClient = originalFactory })
	fake := &fakeVideoClient{
		generateResult: &xaiapi.StartDeferredResponse{RequestId: "up_generate"},
		extendResult:   &xaiapi.StartDeferredResponse{RequestId: "up_extend"},
	}
	newVideoClient = func(string) (xaiapi.VideoClient, error) { return fake, nil }

	adaptor := &TaskAdaptor{apiKey: "test-key"}
	generatePayload, err := jsonMarshal.Marshal(&xaiapi.GenerateVideoRequest{Prompt: "hello", Model: ModelList[0]})
	if err != nil {
		t.Fatal(err)
	}
	generateBody, _ := common.Marshal(requestEnvelope{Operation: operationGenerate, Payload: generatePayload})
	response, err := adaptor.DoRequest(testGinContext(http.MethodPost, "/v1/videos", nil), nil, bytesReader(generateBody))
	if err != nil {
		t.Fatalf("Generate DoRequest() error = %v", err)
	}
	if response.StatusCode != http.StatusOK || fake.generateRequest.GetPrompt() != "hello" {
		t.Fatalf("unexpected generate RPC result: status=%d request=%+v", response.StatusCode, fake.generateRequest)
	}

	extendPayload, err := jsonMarshal.Marshal(&xaiapi.ExtendVideoRequest{Prompt: "continue", Video: videoInput("https://example.com/video.mp4", "")})
	if err != nil {
		t.Fatal(err)
	}
	extendBody, _ := common.Marshal(requestEnvelope{Operation: operationExtend, Payload: extendPayload})
	response, err = adaptor.DoRequest(testGinContext(http.MethodPost, "/v1/videos/extensions", nil), nil, bytesReader(extendBody))
	if err != nil {
		t.Fatalf("Extend DoRequest() error = %v", err)
	}
	if response.StatusCode != http.StatusOK || fake.extendRequest.GetPrompt() != "continue" {
		t.Fatalf("unexpected extend RPC result: status=%d request=%+v", response.StatusCode, fake.extendRequest)
	}
}

func TestParseTaskResultPreservesOfficialResult(t *testing.T) {
	costTicks := int64(1_250_000_000)
	storageError := "public URL creation failed"
	response := &xaiapi.GetDeferredVideoResponse{
		Status: xaiapi.DeferredStatus_DONE,
		Response: &xaiapi.VideoResponse{
			Model:    ModelList[0],
			Progress: 100,
			Video: &xaiapi.GeneratedVideo{
				Url:               "https://example.com/result.mp4",
				Duration:          8,
				RespectModeration: true,
				FileOutput: &xaiapi.FileOutput{
					FileId:    "file_result",
					PublicUrl: stringPointer("https://example.com/public.mp4"),
				},
				StorageError: &storageError,
			},
			Usage: &xaiapi.SamplingUsage{
				PromptTokens:     11,
				CompletionTokens: 22,
				TotalTokens:      33,
				CostInUsdTicks:   &costTicks,
			},
		},
	}
	body, err := jsonMarshal.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	result, err := (&TaskAdaptor{}).ParseTaskResult(body)
	if err != nil {
		t.Fatalf("ParseTaskResult() error = %v", err)
	}
	if result.Status != model.TaskStatusSuccess || result.Progress != "100%" || result.Url != "https://example.com/result.mp4" {
		t.Fatalf("unexpected task state: %+v", result)
	}
	if result.ProviderStatus != xaiapi.DeferredStatus_DONE.String() {
		t.Fatalf("provider status = %q", result.ProviderStatus)
	}
	if result.Duration != 8 || result.RespectModeration == nil || !*result.RespectModeration || result.StorageError != storageError {
		t.Fatalf("unexpected video metadata: %+v", result)
	}
	if result.UpstreamFileID != "file_result" || result.UpstreamPublicURL != "https://example.com/public.mp4" {
		t.Fatalf("unexpected file output: %+v", result)
	}
	if result.PromptTokens != 11 || result.CompletionTokens != 22 || result.TotalTokens != 33 || result.CostUSDTicks != costTicks {
		t.Fatalf("unexpected usage: %+v", result)
	}
}

func TestParseTaskResultMapsFailureAndExpiry(t *testing.T) {
	tests := []struct {
		name     string
		response *xaiapi.GetDeferredVideoResponse
		reason   string
		code     string
	}{
		{
			name: "failed",
			response: &xaiapi.GetDeferredVideoResponse{Status: xaiapi.DeferredStatus_FAILED, Response: &xaiapi.VideoResponse{
				Error: &xaiapi.VideoError{Code: "moderation_blocked", Message: "blocked"},
			}},
			reason: "blocked",
			code:   "moderation_blocked",
		},
		{name: "expired", response: &xaiapi.GetDeferredVideoResponse{Status: xaiapi.DeferredStatus_EXPIRED}, reason: "xAI video request expired"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, _ := jsonMarshal.Marshal(test.response)
			result, err := (&TaskAdaptor{}).ParseTaskResult(body)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != model.TaskStatusFailure || result.Reason != test.reason || result.ErrorCode != test.code {
				t.Fatalf("unexpected failure result: %+v", result)
			}
			if result.ProviderStatus != test.response.Status.String() {
				t.Fatalf("provider status = %q, want %q", result.ProviderStatus, test.response.Status.String())
			}
		})
	}
}

func TestGRPCFailureResponseMapsStatus(t *testing.T) {
	response := grpcFailureResponse(status.Error(codes.ResourceExhausted, "slow down"))
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d", response.StatusCode)
	}
	body, _ := io.ReadAll(response.Body)
	if string(body) == "" {
		t.Fatal("empty error body")
	}
}

func TestSelectVideoTransport(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    videoTransport
	}{
		{name: "official default", baseURL: "", want: videoTransportGRPC},
		{name: "official URL", baseURL: "https://api.x.ai", want: videoTransportGRPC},
		{name: "official host", baseURL: "api.x.ai:443", want: videoTransportGRPC},
		{name: "explicit gRPC", baseURL: "grpc://127.0.0.1:3001", want: videoTransportGRPC},
		{name: "HTTP gateway", baseURL: "http://hk.tianwenyue.com", want: videoTransportREST},
		{name: "HTTPS gateway", baseURL: "https://gateway.example/v1", want: videoTransportREST},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := selectVideoTransport(test.baseURL); got != test.want {
				t.Fatalf("selectVideoTransport(%q) = %v, want %v", test.baseURL, got, test.want)
			}
		})
	}
}

func TestNormalizeGRPCTargetSupportsExplicitSchemes(t *testing.T) {
	target, useTLS, _, err := normalizeGRPCTarget("grpc://127.0.0.1:3001")
	if err != nil {
		t.Fatal(err)
	}
	if target != "127.0.0.1:3001" || useTLS {
		t.Fatalf("insecure target = %q tls=%t", target, useTLS)
	}
	target, useTLS, serverName, err := normalizeGRPCTarget("grpcs://grpc.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if target != "grpc.example.com:443" || !useTLS || serverName != "grpc.example.com" {
		t.Fatalf("TLS target = %q tls=%t server=%q", target, useTLS, serverName)
	}
}

func TestRESTSubmitAndFetchUseGatewayPaths(t *testing.T) {
	service.InitHttpClient()
	var submitBody relaycommon.TaskSubmitReq
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer rest-key" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/v1/videos":
			if err := json.NewDecoder(request.Body).Decode(&submitBody); err != nil {
				t.Errorf("decode submit body: %v", err)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"id":"upstream_task","object":"video","model":"grok-imagine-video","status":"queued","progress":0}`)
		case request.Method == http.MethodGet && request.URL.Path == "/v1/videos/upstream_task":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"id":"upstream_task","object":"video","status":"completed","progress":100,"seconds":"1","metadata":{"url":"https://example.com/result.mp4"},"usage":{"cost_in_usd_ticks":500000000}}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	info := &relaycommon.RelayInfo{
		OriginModelName: ModelList[0],
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    server.URL,
			ApiKey:            "rest-key",
			UpstreamModelName: ModelList[0],
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}
	context := testGinContext(http.MethodPost, "/v1/videos", nil)
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:      "a small white dot",
		Duration:    1,
		Resolution:  "480p",
		AspectRatio: "1:1",
	})
	context.Set("xai_video_operation", operationGenerate)

	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	requestBody, err := adaptor.BuildRequestBody(context, info)
	if err != nil {
		t.Fatalf("BuildRequestBody() error = %v", err)
	}
	response, err := adaptor.DoRequest(context, info, requestBody)
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	upstreamID, _, taskErr := adaptor.DoResponse(context, response, info)
	if taskErr != nil {
		t.Fatalf("DoResponse() error = %v", taskErr)
	}
	if upstreamID != "upstream_task" {
		t.Fatalf("upstream task ID = %q", upstreamID)
	}
	if submitBody.Model != ModelList[0] || submitBody.Prompt != "a small white dot" || submitBody.Operation != operationGenerate {
		t.Fatalf("unexpected REST submit body: %+v", submitBody)
	}
	if submitBody.Duration != 1 || submitBody.Resolution != "480p" || submitBody.AspectRatio != "1:1" {
		t.Fatalf("missing REST video controls: %+v", submitBody)
	}

	pollResponse, err := adaptor.FetchTask(server.URL, "rest-key", map[string]any{"task_id": upstreamID}, "")
	if err != nil {
		t.Fatalf("FetchTask() error = %v", err)
	}
	defer pollResponse.Body.Close()
	pollBody, _ := io.ReadAll(pollResponse.Body)
	result, err := adaptor.ParseTaskResult(pollBody)
	if err != nil {
		t.Fatalf("ParseTaskResult() error = %v", err)
	}
	if result.Status != model.TaskStatusSuccess || result.Url != "https://example.com/result.mp4" || result.Progress != "100%" {
		t.Fatalf("unexpected REST task result: %+v", result)
	}
	if result.Duration != 1 || result.CostUSDTicks != 500000000 {
		t.Fatalf("unexpected REST usage: %+v", result)
	}
}

func TestUniAPIRESTSubmitAndFetchUseDocumentedProtocol(t *testing.T) {
	service.InitHttpClient()
	var submitBody uniAPIVideoGenerateRequest
	var submitFields map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer uniapi-key" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/xai/v1/videos/generations":
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read submit body: %v", err)
			}
			if err = json.Unmarshal(body, &submitBody); err != nil {
				t.Errorf("decode submit body: %v", err)
			}
			if err = json.Unmarshal(body, &submitFields); err != nil {
				t.Errorf("decode submit fields: %v", err)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{"request_id":"upstream_request"}`)
		case request.Method == http.MethodGet && request.URL.Path == "/xai/v1/videos/upstream_request":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(writer, `{
				"model":"grok-imagine-video",
				"video":{
					"url":"https://example.com/uniapi-result.mp4",
					"duration":"6",
					"respect_moderation":true
				}
			}`)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	info := &relaycommon.RelayInfo{
		OriginModelName: ModelList[0],
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    server.URL + "/xai/v1",
			ApiKey:            "uniapi-key",
			UpstreamModelName: ModelList[0],
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}
	context := testGinContext(http.MethodPost, "/v1/videos", nil)
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:      "a paper boat on a quiet lake",
		Duration:    6,
		Resolution:  "480p",
		AspectRatio: "16:9",
		ImageURL:    "https://example.com/first-frame.png",
		Output:      &relaycommon.TaskVideoOutput{UploadURL: "https://uploads.example.com/result"},
	})
	context.Set("xai_video_operation", operationGenerate)

	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	requestBody, err := adaptor.BuildRequestBody(context, info)
	if err != nil {
		t.Fatalf("BuildRequestBody() error = %v", err)
	}
	response, err := adaptor.DoRequest(context, info, requestBody)
	if err != nil {
		t.Fatalf("DoRequest() error = %v", err)
	}
	upstreamID, taskData, taskErr := adaptor.DoResponse(context, response, info)
	if taskErr != nil {
		t.Fatalf("DoResponse() error = %v", taskErr)
	}
	if upstreamID != "upstream_request" {
		t.Fatalf("upstream task ID = %q", upstreamID)
	}
	var submitted submitRecord
	if err = common.Unmarshal(taskData, &submitted); err != nil {
		t.Fatalf("decode submit record: %v", err)
	}
	if submitted.RequestID != upstreamID || submitted.Operation != operationGenerate || submitted.Duration != 6 || submitted.Resolution != "480p" || submitted.AspectRatio != "16:9" {
		t.Fatalf("unexpected submit record: %+v", submitted)
	}
	if submitBody.Prompt != "a paper boat on a quiet lake" || submitBody.Model != ModelList[0] {
		t.Fatalf("unexpected UniAPI basic fields: %+v", submitBody)
	}
	if submitBody.Duration != 6 || submitBody.Resolution != "480p" || submitBody.AspectRatio != "16:9" {
		t.Fatalf("unexpected UniAPI video controls: %+v", submitBody)
	}
	if submitBody.Image == nil || submitBody.Image.URL != "https://example.com/first-frame.png" {
		t.Fatalf("unexpected UniAPI image: %+v", submitBody.Image)
	}
	if submitBody.Output == nil || submitBody.Output.UploadURL != "https://uploads.example.com/result" {
		t.Fatalf("unexpected UniAPI output: %+v", submitBody.Output)
	}
	for _, field := range []string{"operation", "image_url", "image_file_id", "storage_options"} {
		if _, exists := submitFields[field]; exists {
			t.Fatalf("UniAPI submit body leaked internal field %q: %+v", field, submitFields)
		}
	}

	pollResponse, err := adaptor.FetchTask(info.ChannelBaseUrl, "uniapi-key", map[string]any{"task_id": upstreamID}, "")
	if err != nil {
		t.Fatalf("FetchTask() error = %v", err)
	}
	defer pollResponse.Body.Close()
	pollBody, _ := io.ReadAll(pollResponse.Body)
	result, err := adaptor.ParseTaskResult(pollBody)
	if err != nil {
		t.Fatalf("ParseTaskResult() error = %v", err)
	}
	if result.Status != model.TaskStatusSuccess || result.Url != "https://example.com/uniapi-result.mp4" || result.RemoteUrl != result.Url {
		t.Fatalf("unexpected UniAPI task result: %+v", result)
	}
	if result.Duration != 6 || result.Progress != "100%" || result.RespectModeration == nil || !*result.RespectModeration {
		t.Fatalf("unexpected UniAPI metadata: %+v", result)
	}
}

func TestUniAPIDoResponseIncludesPublicSubmitMetadata(t *testing.T) {
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Model:       ModelList[0],
		Prompt:      "a paper boat",
		Duration:    6,
		Resolution:  "480p",
		AspectRatio: "1:1",
	})
	context.Set("xai_video_operation", operationGenerate)
	info := &relaycommon.RelayInfo{
		OriginModelName: ModelList[0],
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.uniapi.io",
			UpstreamModelName: ModelList[0],
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_public"},
	}
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	response := &http.Response{Body: io.NopCloser(strings.NewReader(`{"request_id":"upstream_private"}`))}

	upstreamID, _, taskErr := adaptor.DoResponse(context, response, info)
	if taskErr != nil {
		t.Fatalf("DoResponse() error = %v", taskErr)
	}
	if upstreamID != "upstream_private" {
		t.Fatalf("upstream id = %q", upstreamID)
	}
	var public dto.OpenAIVideo
	if err := common.Unmarshal(recorder.Body.Bytes(), &public); err != nil {
		t.Fatalf("decode public response: %v", err)
	}
	if public.ID != "task_public" || public.CreatedAt <= 0 || public.Seconds != "6" || public.Size != "480p" {
		t.Fatalf("unexpected public response: %+v", public)
	}
	if public.Metadata["aspect_ratio"] != "1:1" || public.Metadata["operation"] != operationGenerate {
		t.Fatalf("unexpected public metadata: %+v", public.Metadata)
	}
}

func TestConvertToOpenAIVideoPreservesSubmittedMetadataAfterPolling(t *testing.T) {
	task := &model.Task{
		TaskID:     "task_public",
		CreatedAt:  123,
		FinishTime: 456,
		Status:     model.TaskStatusSuccess,
		Progress:   "100%",
		Properties: model.Properties{
			OriginModelName: ModelList[0],
		},
		PrivateData: model.TaskPrivateData{XAI: &model.XAITaskPrivateData{
			Operation:   operationGenerate,
			Duration:    6,
			AspectRatio: "1:1",
			Resolution:  "480p",
		}},
		Data: []byte(`{"model":"grok-imagine-video","status":"done","video":{"url":"https://example.com/result.mp4","duration":"6","respect_moderation":true}}`),
	}

	body, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("ConvertToOpenAIVideo() error = %v", err)
	}
	var public dto.OpenAIVideo
	if err = common.Unmarshal(body, &public); err != nil {
		t.Fatalf("decode public video: %v", err)
	}
	if public.CreatedAt != 123 || public.CompletedAt != 456 || public.Seconds != "6" || public.Size != "480p" {
		t.Fatalf("unexpected public video: %+v", public)
	}
	if public.Metadata["aspect_ratio"] != "1:1" || public.Metadata["operation"] != operationGenerate {
		t.Fatalf("submitted metadata was lost: %+v", public.Metadata)
	}
}

func TestUniAPIRESTAddsRequiredDefaults(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: ModelList[0],
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.uniapi.io",
			UpstreamModelName: ModelList[0],
		},
	}
	context := testGinContext(http.MethodPost, "/v1/videos", nil)
	context.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "a short video"})
	context.Set("xai_video_operation", operationGenerate)

	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	body, err := adaptor.BuildRequestBody(context, info)
	if err != nil {
		t.Fatalf("BuildRequestBody() error = %v", err)
	}
	var request uniAPIVideoGenerateRequest
	if err = json.NewDecoder(body).Decode(&request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.Duration != defaultUniAPIDuration || request.Resolution != defaultUniAPIResolution {
		t.Fatalf("required defaults = duration:%d resolution:%q", request.Duration, request.Resolution)
	}
	stored, err := relaycommon.GetTaskRequest(context)
	if err != nil {
		t.Fatalf("get stored task request: %v", err)
	}
	if stored.Duration != defaultUniAPIDuration || stored.Resolution != defaultUniAPIResolution {
		t.Fatalf("stored defaults = duration:%d resolution:%q", stored.Duration, stored.Resolution)
	}
	requestURL, err := adaptor.BuildRequestURL(info)
	if err != nil {
		t.Fatal(err)
	}
	if requestURL != "https://api.uniapi.io/xai/v1/videos/generations" {
		t.Fatalf("request URL = %q", requestURL)
	}
}

func TestBuildUniAPIVideoGenerateRequestDurationBounds(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: ModelList[0],
		ChannelMeta:     &relaycommon.ChannelMeta{UpstreamModelName: ModelList[0]},
	}
	tests := []struct {
		name      string
		duration  int
		want      int
		wantError bool
	}{
		{name: "documented default", duration: 0, want: defaultUniAPIDuration},
		{name: "minimum", duration: 1, want: 1},
		{name: "maximum", duration: 15, want: 15},
		{name: "above maximum", duration: 16, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, err := buildUniAPIVideoGenerateRequest(&relaycommon.TaskSubmitReq{
				Prompt:   "duration boundary",
				Model:    ModelList[0],
				Duration: test.duration,
			}, info, operationGenerate)
			if test.wantError {
				if err == nil {
					t.Fatalf("duration %d unexpectedly succeeded", test.duration)
				}
				return
			}
			if err != nil {
				t.Fatalf("duration %d error = %v", test.duration, err)
			}
			if request.Duration != test.want {
				t.Fatalf("duration = %d, want %d", request.Duration, test.want)
			}
		})
	}
}

func TestUniAPIDocumentedOutputEnums(t *testing.T) {
	for _, ratio := range []string{"1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3"} {
		if err := validateAspectRatio(ratio); err != nil {
			t.Errorf("documented aspect ratio %q rejected: %v", ratio, err)
		}
	}
	for _, resolution := range []string{"480p", "720p"} {
		if err := validateResolution(resolution); err != nil {
			t.Errorf("documented resolution %q rejected: %v", resolution, err)
		}
	}
	if err := validateAspectRatio("21:9"); err == nil {
		t.Fatal("undocumented aspect ratio unexpectedly accepted")
	}
	if err := validateResolution("1080p"); err == nil {
		t.Fatal("undocumented resolution unexpectedly accepted")
	}
}

func TestUniAPIRESTAcceptsPublicVideoJSON(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: ModelList[0],
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.uniapi.io",
			UpstreamModelName: ModelList[0],
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	context := testGinContext(http.MethodPost, "/v1/videos", strings.NewReader(`{
		"model":"grok-imagine-video",
		"prompt":"animate this frame",
		"duration":6,
		"resolution":"480p",
		"aspect_ratio":"9:16",
		"image":{"url":"https://example.com/input.png"},
		"output":{"upload_url":"https://uploads.example.com/output"}
	}`))
	context.Request.Header.Set("Content-Type", "application/json")

	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	if taskErr := adaptor.ValidateRequestAndSetAction(context, info); taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() error = %v", taskErr)
	}
	body, err := adaptor.BuildRequestBody(context, info)
	if err != nil {
		t.Fatalf("BuildRequestBody() error = %v", err)
	}
	var request uniAPIVideoGenerateRequest
	if err = json.NewDecoder(body).Decode(&request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.Image == nil || request.Image.URL != "https://example.com/input.png" {
		t.Fatalf("image mapping = %+v", request.Image)
	}
	if request.Output == nil || request.Output.UploadURL != "https://uploads.example.com/output" {
		t.Fatalf("output mapping = %+v", request.Output)
	}
}

func TestParseUniAPIRESTTaskStates(t *testing.T) {
	pending, err := parseUniAPITaskResult([]byte(`{"status":"pending"}`))
	if err != nil {
		t.Fatalf("pending parse error = %v", err)
	}
	if pending.Status != model.TaskStatusInProgress || pending.ProviderStatus != "pending" {
		t.Fatalf("unexpected pending result: %+v", pending)
	}

	blocked, err := parseUniAPITaskResult([]byte(`{
		"model":"grok-imagine-video",
		"video":{"url":"","duration":"6","respect_moderation":"false"}
	}`))
	if err != nil {
		t.Fatalf("moderation parse error = %v", err)
	}
	if blocked.Status != model.TaskStatusFailure || blocked.ErrorCode != "moderation_rejected" {
		t.Fatalf("unexpected moderation result: %+v", blocked)
	}
}

func TestUniAPIRESTRejectsUnsupportedOperations(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: ModelList[0],
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.uniapi.io",
			UpstreamModelName: ModelList[0],
		},
	}
	context := testGinContext(http.MethodPost, "/v1/videos/extensions", nil)
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "continue",
		VideoURL: "https://example.com/source.mp4",
	})
	context.Set("xai_video_operation", operationExtend)

	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	if _, err := adaptor.BuildRequestBody(context, info); err == nil || !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("unsupported operation error = %v", err)
	}
}

func TestUniAPIRESTValidationMarksUnsupportedCapability(t *testing.T) {
	info := &relaycommon.RelayInfo{
		OriginModelName: ModelList[0],
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.uniapi.io",
			UpstreamModelName: ModelList[0],
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	context := testGinContext(http.MethodPost, "/v1/videos/extensions", strings.NewReader(`{
		"model":"grok-imagine-video",
		"prompt":"continue the video",
		"video_url":"https://example.com/source.mp4"
	}`))
	context.Request.Header.Set("Content-Type", "application/json")

	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	taskErr := adaptor.ValidateRequestAndSetAction(context, info)
	if taskErr == nil || taskErr.Code != unsupportedCapability || !taskErr.LocalError {
		t.Fatalf("unsupported capability error = %+v", taskErr)
	}
}

func TestParseRESTTaskResultSupportsNewAPIEnvelope(t *testing.T) {
	body := []byte(`{
  "success": true,
  "data": {
    "task_id": "upstream_wrapped",
    "status": "failed",
    "progress": 100,
    "error": {"code": "moderation_blocked", "message": "blocked"}
  }
}`)
	result, err := parseRESTTaskResult(body)
	if err != nil {
		t.Fatalf("parseRESTTaskResult() error = %v", err)
	}
	if result.TaskID != "upstream_wrapped" || result.Status != model.TaskStatusFailure {
		t.Fatalf("unexpected wrapped task: %+v", result)
	}
	if result.ErrorCode != "moderation_blocked" || result.Reason != "blocked" {
		t.Fatalf("unexpected wrapped error: %+v", result)
	}
}

func TestAdjustBillingOnCompleteUsesActualXAIUSD(t *testing.T) {
	adaptor := &TaskAdaptor{}
	task := &model.Task{PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{GroupRatio: 0.8}}}
	result := &relaycommon.TaskInfo{CostUSDTicks: 2_500_000_000}
	want := int(0.25 * common.QuotaPerUnit * 0.8)
	if got := adaptor.AdjustBillingOnComplete(task, result); got != want {
		t.Fatalf("quota = %d, want %d", got, want)
	}
}

func TestResolveLocalFileInputsUsesAuthenticatedFile(t *testing.T) {
	oldDB := model.DB
	database, err := gorm.Open(sqlite.Open("file:xai-adaptor-files?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	model.DB = database
	t.Cleanup(func() { model.DB = oldDB })
	if err = database.AutoMigrate(&model.XAIFile{}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAI_FILES_DIR", t.TempDir())
	t.Setenv("XAI_FILES_OSS_ENABLED", "false")
	file, err := xaifile.StoreBytes(context.Background(), 7, 11, "frame.png", nil, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	if err != nil {
		t.Fatal(err)
	}
	request := &relaycommon.TaskSubmitReq{ImageFileID: file.FileID}
	if err = resolveLocalFileInputs(context.Background(), 7, request); err != nil {
		t.Fatalf("resolveLocalFileInputs() error = %v", err)
	}
	if request.ImageFileID != "" || !strings.HasPrefix(request.ImageURL, "data:image/png;base64,") {
		t.Fatalf("unexpected resolved input: %+v", request)
	}
	unauthorized := &relaycommon.TaskSubmitReq{ImageFileID: file.FileID}
	if err = resolveLocalFileInputs(context.Background(), 8, unauthorized); err == nil {
		t.Fatal("expected cross-user file resolution to fail")
	}
}

func testGinContext(method, path string, body io.Reader) *gin.Context {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(method, path, body)
	return context
}

func bytesReader(body []byte) io.Reader {
	return &byteReader{body: body}
}

type byteReader struct {
	body []byte
	off  int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.off >= len(r.body) {
		return 0, io.EOF
	}
	n := copy(p, r.body[r.off:])
	r.off += n
	return n, nil
}

func stringPointer(value string) *string { return &value }

func xaiRelayInfo() *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: ModelList[0]}}
}
