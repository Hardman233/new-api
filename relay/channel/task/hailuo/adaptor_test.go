package hailuo

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestBuildRequestURLSeparatesH3AndLegacyProtocols(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		model   string
		want    string
	}{
		{
			name:    "H3 replaces legacy default host",
			baseURL: "https://api.minimax.chat",
			model:   H3Model,
			want:    H3DefaultBaseURL + H3CreateEndpoint,
		},
		{
			name:    "H3 preserves custom relay host",
			baseURL: "https://relay.example.com/",
			model:   H3Model,
			want:    "https://relay.example.com" + H3CreateEndpoint,
		},
		{
			name:    "legacy keeps v1 endpoint",
			baseURL: "https://relay.example.com/",
			model:   "MiniMax-Hailuo-2.3",
			want:    "https://relay.example.com" + TextToVideoEndpoint,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adaptor := &TaskAdaptor{baseURL: test.baseURL}
			got, err := adaptor.BuildRequestURL(hailuoTestInfo(test.model))
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("URL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBuildH3VideoRequestSupportsOfficialMultimodalContent(t *testing.T) {
	watermark := true
	req := &relaycommon.TaskSubmitReq{
		Prompt:      "use the reference voice",
		Duration:    4,
		Resolution:  "2k",
		Ratio:       "9:16",
		CallbackURL: "https://example.com/callback",
		Watermark:   watermark,
		Content: []map[string]interface{}{
			{
				"type": "video_url",
				"video_url": map[string]interface{}{
					"url": "https://example.com/reference.mp4",
				},
				"role": "reference_video",
			},
			{
				"type": "audio_url",
				"audio_url": map[string]interface{}{
					"url": "https://example.com/reference.mp3",
				},
				"role": "reference_audio",
			},
		},
	}

	body, err := buildH3VideoRequest(req, hailuoTestInfo(H3Model))
	if err != nil {
		t.Fatalf("buildH3VideoRequest() error = %v", err)
	}
	if body.Model != H3Model || body.Resolution != Resolution2K || body.Duration != 4 || body.Ratio != "9:16" {
		t.Fatalf("unexpected basic fields: %+v", body)
	}
	if body.CallbackURL != req.CallbackURL || body.AIGCWatermark == nil || !*body.AIGCWatermark {
		t.Fatalf("callback/watermark not preserved: %+v", body)
	}
	if len(body.Content) != 3 || body.Content[0].Type != "text" || body.Content[0].Text != req.Prompt {
		t.Fatalf("unexpected content: %+v", body.Content)
	}
	if body.Content[1].VideoURL == nil || body.Content[1].VideoURL.URL != "https://example.com/reference.mp4" {
		t.Fatalf("video content not preserved: %+v", body.Content[1])
	}
	if body.Content[2].AudioURL == nil || body.Content[2].Role != "reference_audio" {
		t.Fatalf("audio content not preserved: %+v", body.Content[2])
	}
}

func TestBuildH3VideoRequestMapsSimpleImageInputs(t *testing.T) {
	body, err := buildH3VideoRequest(&relaycommon.TaskSubmitReq{
		Prompt:   "smooth camera movement",
		Images:   []string{"https://example.com/first.png", "https://example.com/last.png"},
		Duration: 5,
		Ratio:    "16:9",
	}, hailuoTestInfo(H3Model))
	if err != nil {
		t.Fatal(err)
	}
	if body.Ratio != "adaptive" {
		t.Fatalf("frame input ratio = %q, want adaptive", body.Ratio)
	}
	if len(body.Content) != 3 || body.Content[1].Role != "first_frame" || body.Content[2].Role != "last_frame" {
		t.Fatalf("unexpected frame mapping: %+v", body.Content)
	}
}

func TestValidateH3VideoRequestRejectsInvalidOfficialCombinations(t *testing.T) {
	tests := []struct {
		name string
		body H3VideoRequest
		want string
	}{
		{
			name: "duration",
			body: H3VideoRequest{Model: H3Model, Content: []H3ContentItem{{Type: "text", Text: "ok"}}, Resolution: Resolution768P, Duration: 3, Ratio: "16:9"},
			want: "duration",
		},
		{
			name: "text adaptive",
			body: H3VideoRequest{Model: H3Model, Content: []H3ContentItem{{Type: "text", Text: "ok"}}, Resolution: Resolution768P, Duration: 4, Ratio: "adaptive"},
			want: "cannot be adaptive",
		},
		{
			name: "mixed frame and reference",
			body: H3VideoRequest{
				Model: H3Model, Resolution: Resolution768P, Duration: 4, Ratio: "adaptive",
				Content: []H3ContentItem{
					{Type: "text", Text: "ok"},
					{Type: "image_url", ImageURL: &H3MediaURL{URL: "https://example.com/first.png"}, Role: "first_frame"},
					{Type: "audio_url", AudioURL: &H3MediaURL{URL: "https://example.com/ref.mp3"}, Role: "reference_audio"},
				},
			},
			want: "cannot be mixed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateH3VideoRequest(&test.body)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestBuildRequestBodyUsesH3OfficialShapeAndLegacyShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	h3Context, _ := gin.CreateTestContext(httptest.NewRecorder())
	h3Context.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "ocean sunrise", Duration: 4, AIGCWatermark: true})
	h3Reader, err := (&TaskAdaptor{}).BuildRequestBody(h3Context, hailuoTestInfo(H3Model))
	if err != nil {
		t.Fatal(err)
	}
	h3Payload := readJSONMap(t, h3Reader)
	if h3Payload["model"] != H3Model || h3Payload["resolution"] != Resolution768P || h3Payload["ratio"] != "16:9" {
		t.Fatalf("unexpected H3 payload: %#v", h3Payload)
	}
	if _, exists := h3Payload["prompt"]; exists {
		t.Fatalf("H3 payload leaked legacy prompt field: %#v", h3Payload)
	}
	if _, exists := h3Payload["content"]; !exists {
		t.Fatalf("H3 payload missing content: %#v", h3Payload)
	}
	if h3Payload["aigc_watermark"] != true {
		t.Fatalf("H3 payload missing official aigc_watermark: %#v", h3Payload)
	}

	imageContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	imageContext.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "animate this image",
		Image:  "https://example.com/first.png",
	})
	imageReader, err := (&TaskAdaptor{}).BuildRequestBody(imageContext, hailuoTestInfo(H3Model))
	if err != nil {
		t.Fatal(err)
	}
	imagePayload := readJSONMap(t, imageReader)
	content, ok := imagePayload["content"].([]interface{})
	if !ok || len(content) != 2 {
		t.Fatalf("unexpected H3 image payload: %#v", imagePayload)
	}
	imageItem, ok := content[1].(map[string]interface{})
	if !ok || imageItem["role"] != "first_frame" {
		t.Fatalf("top-level image was not mapped to first_frame: %#v", imagePayload)
	}

	legacyContext, _ := gin.CreateTestContext(httptest.NewRecorder())
	legacyContext.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "legacy prompt", Duration: 6})
	legacyInfo := hailuoTestInfo("MiniMax-Hailuo-2.3")
	legacyReader, err := (&TaskAdaptor{}).BuildRequestBody(legacyContext, legacyInfo)
	if err != nil {
		t.Fatal(err)
	}
	legacyPayload := readJSONMap(t, legacyReader)
	if legacyPayload["prompt"] != "legacy prompt" || legacyPayload["model"] != "MiniMax-Hailuo-2.3" {
		t.Fatalf("legacy payload changed: %#v", legacyPayload)
	}
	if _, exists := legacyPayload["content"]; exists {
		t.Fatalf("legacy payload unexpectedly contains H3 content: %#v", legacyPayload)
	}
}

func TestDoResponseParsesH3TaskIDWithoutExposingIt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	response := &http.Response{Body: io.NopCloser(strings.NewReader(`{"task_id":"upstream-h3-1"}`))}
	info := hailuoTestInfo(H3Model)
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{PublicTaskID: "task_public_1"}

	taskID, data, taskErr := (&TaskAdaptor{}).DoResponse(context, response, info)
	if taskErr != nil {
		t.Fatalf("DoResponse() error = %v", taskErr)
	}
	if taskID != "upstream-h3-1" || !strings.Contains(string(data), "upstream-h3-1") {
		t.Fatalf("unexpected upstream result: id=%q data=%s", taskID, data)
	}
	if strings.Contains(recorder.Body.String(), "upstream-h3-1") || !strings.Contains(recorder.Body.String(), "task_public_1") {
		t.Fatalf("public response leaked upstream ID: %s", recorder.Body.String())
	}
}

func TestDoResponsePreservesH3HTTPErrorStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	response := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body: io.NopCloser(strings.NewReader(
			`{"type":"error","error":{"type":"rate_limit_error","message":"retry later","http_code":"429"}}`,
		)),
	}

	_, _, taskErr := (&TaskAdaptor{}).DoResponse(context, response, hailuoTestInfo(H3Model))
	if taskErr == nil || taskErr.StatusCode != http.StatusTooManyRequests || taskErr.Code != "429" {
		t.Fatalf("task error = %+v, want HTTP 429 with code 429", taskErr)
	}
}

func TestFetchTaskSeparatesH3AndLegacyQueryURLs(t *testing.T) {
	var paths []string
	var queries []string
	var authHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		queries = append(queries, r.URL.RawQuery)
		authHeaders = append(authHeaders, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"task":{"id":"task-1","status":"queued"}}`))
	}))
	defer server.Close()

	adaptor := &TaskAdaptor{}
	for _, body := range []map[string]any{
		{"task_id": "h3-task", "model": H3Model},
		{"task_id": "legacy-task", "model": "MiniMax-Hailuo-2.3"},
	} {
		response, err := adaptor.FetchTask(server.URL, "secret", body, "")
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
	}
	if paths[0] != H3QueryEndpoint+"/h3-task" || queries[0] != "" {
		t.Fatalf("H3 query = %s?%s", paths[0], queries[0])
	}
	if paths[1] != QueryTaskEndpoint || queries[1] != "task_id=legacy-task" {
		t.Fatalf("legacy query = %s?%s", paths[1], queries[1])
	}
	if authHeaders[0] != "Bearer secret" || authHeaders[1] != "Bearer secret" {
		t.Fatalf("unexpected auth headers: %#v", authHeaders)
	}
}

func TestParseH3TaskResultMapsStatusesUsageAndBilling(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantStatus model.TaskStatus
		wantReason string
	}{
		{name: "queued", body: `{"task":{"id":"1","model":"MiniMax-H3","status":"queued"}}`, wantStatus: model.TaskStatusQueued},
		{name: "running", body: `{"task":{"id":"1","model":"MiniMax-H3","status":"running"}}`, wantStatus: model.TaskStatusInProgress},
		{name: "failed", body: `{"task":{"id":"1","model":"MiniMax-H3","status":"failed","error":{"code":"1026","message":"sensitive"}}}`, wantStatus: model.TaskStatusFailure, wantReason: "sensitive"},
		{name: "cancelled", body: `{"task":{"id":"1","model":"MiniMax-H3","status":"cancelled"}}`, wantStatus: model.TaskStatusFailure, wantReason: "cancelled"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != string(test.wantStatus) || (test.wantReason != "" && !strings.Contains(result.Reason, test.wantReason)) {
				t.Fatalf("result = %+v", result)
			}
		})
	}

	success := []byte(`{
		"task": {
			"id": "424010985738629",
			"model": "MiniMax-H3",
			"status": "succeeded",
			"content": {"url": "https://example.com/h3.mp4"},
			"resolution": "2K",
			"duration": 5,
			"usage": {
				"total_seconds": 7,
				"input_seconds": 2,
				"output_seconds": 5,
				"input_image_count": 7,
				"total_tokens": 273890,
				"prompt_tokens": 13500,
				"completion_tokens": 260390
			}
		}
	}`)
	result, err := (&TaskAdaptor{}).ParseTaskResult(success)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != model.TaskStatusSuccess || result.Url != "https://example.com/h3.mp4" || result.Duration != 5 {
		t.Fatalf("unexpected success result: %+v", result)
	}
	if result.PromptTokens != 13500 || result.CompletionTokens != 260390 || result.TotalTokens != 273890 {
		t.Fatalf("usage not preserved: %+v", result)
	}
	if math.Abs(result.BillingUnits-12.0) > 1e-9 {
		t.Fatalf("billing units = %v, want 12", result.BillingUnits)
	}
}

func TestParseH3APIErrorRetriesRateLimitAndFailsPermanentError(t *testing.T) {
	rateLimit, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"retry later","http_code":"429"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if rateLimit.Status != model.TaskStatusInProgress {
		t.Fatalf("rate limit status = %s", rateLimit.Status)
	}

	badRequest, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{"type":"error","error":{"type":"bad_request_error","message":"invalid task id","http_code":"400"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if badRequest.Status != model.TaskStatusFailure || badRequest.ErrorCode != "400" {
		t.Fatalf("bad request result = %+v", badRequest)
	}
}

func TestLegacyTaskResultStillUsesV1StatusMapping(t *testing.T) {
	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{
		"task_id":"legacy-task",
		"status":"Queueing",
		"base_resp":{"status_code":0,"status_msg":"success"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != model.TaskStatusInProgress || result.Progress != "30%" {
		t.Fatalf("legacy result changed: %+v", result)
	}
}

func TestH3BillingUsesOfficialResolutionDurationAndActualUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	content := []map[string]interface{}{{"type": "text", "text": "references"}}
	for index := 0; index < 6; index++ {
		content = append(content, map[string]interface{}{
			"type":      "image_url",
			"image_url": map[string]interface{}{"url": "https://example.com/ref.png"},
			"role":      "reference_image",
		})
	}
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:     "references",
		Content:    content,
		Duration:   4,
		Resolution: "2K",
	})
	info := hailuoTestInfo(H3Model)

	ratios := (&TaskAdaptor{}).EstimateBilling(context, info)
	if math.Abs(ratios["minimax_h3_billing_units"]-6.8) > 1e-9 {
		t.Fatalf("estimated units = %v, want 6.8", ratios)
	}

	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })
	task := &model.Task{
		Properties: model.Properties{UpstreamModelName: H3Model},
		PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{
			ModelPrice: 0.5,
			GroupRatio: 0.8,
		}},
	}
	actual := (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{BillingUnits: 12})
	if actual != 2_400_000 {
		t.Fatalf("actual quota = %d, want 2400000", actual)
	}
}

func TestH3TieredBillingUsesBillingUnitsAsCompletionUnits(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{Prompt: "ok", Duration: 5, Resolution: Resolution768P})
	info := hailuoTestInfo(H3Model)
	params, ok := (&TaskAdaptor{}).EstimateTieredBillingTokens(context, info)
	if !ok || params.C != 5 {
		t.Fatalf("tiered params = %+v ok=%v, want c=5", params, ok)
	}

	expression := `tier("h3", c * 500000)`
	task := &model.Task{
		Properties: model.Properties{UpstreamModelName: H3Model},
		PrivateData: model.TaskPrivateData{BillingContext: &model.TaskBillingContext{
			TieredBillingSnapshot: &billingexpr.BillingSnapshot{
				BillingMode:   "tiered_expr",
				ExprString:    expression,
				ExprHash:      billingexpr.ExprHashString(expression),
				GroupRatio:    1,
				QuotaPerUnit:  500000,
				EstimatedTier: "h3",
			},
		}},
	}
	actual := (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{BillingUnits: 8})
	if actual != 2_000_000 {
		t.Fatalf("tiered actual quota = %d, want 2000000", actual)
	}
}

func TestConvertH3ToOpenAIVideoIncludesUsageWithoutUpstreamTaskID(t *testing.T) {
	task := &model.Task{
		TaskID:    "task_public_1",
		Status:    model.TaskStatusSuccess,
		Progress:  "100%",
		CreatedAt: 1785125529,
		UpdatedAt: 1785125946,
		Properties: model.Properties{
			OriginModelName:   H3Model,
			UpstreamModelName: H3Model,
		},
		PrivateData: model.TaskPrivateData{
			ResultURL: "https://oss.example.com/h3.mp4",
		},
		Data: []byte(`{
			"task": {
				"id": "upstream-h3-1",
				"model": "MiniMax-H3",
				"status": "succeeded",
				"resolution": "2K",
				"duration": 5,
				"ratio": "16:9",
				"task_type": "generation",
				"modality": "video",
				"usage": {
					"total_seconds": 7,
					"input_seconds": 2,
					"output_seconds": 5,
					"input_image_count": 1,
					"input_audio_seconds": 6,
					"total_tokens": 273890,
					"prompt_tokens": 13500,
					"completion_tokens": 260390
				}
			}
		}`),
	}

	data, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "upstream-h3-1") {
		t.Fatalf("public response leaked upstream task ID: %s", data)
	}
	var result dto.OpenAIVideo
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != "task_public_1" || result.Seconds != "5" || result.Metadata["url"] != "https://oss.example.com/h3.mp4" {
		t.Fatalf("unexpected public response: %+v", result)
	}
	if result.Metadata["resolution"] != "2K" || result.Metadata["ratio"] != "16:9" {
		t.Fatalf("missing H3 metadata: %+v", result.Metadata)
	}
	if result.Usage["total_seconds"] != float64(7) || result.Usage["input_image_count"] != float64(1) {
		t.Fatalf("missing H3 usage: %+v", result.Usage)
	}
}

func hailuoTestInfo(modelName string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: modelName,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.minimax.chat",
			UpstreamModelName: modelName,
		},
	}
}

func readJSONMap(t *testing.T, reader io.Reader) map[string]interface{} {
	t.Helper()
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	return result
}
