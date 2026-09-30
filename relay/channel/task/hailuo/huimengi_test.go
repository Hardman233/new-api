package hailuo

import (
	"encoding/json"
	"io"
	"math"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestHuimengiH3BuildsNativeTaskRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:      "a quiet ocean at sunrise",
		Duration:    4,
		Resolution:  Resolution768P,
		Ratio:       "16:9",
		CallbackURL: "https://example.com/webhook",
	})

	info := huimengiTestInfo(H3Model)
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)
	requestURL, err := adaptor.BuildRequestURL(info)
	if err != nil {
		t.Fatal(err)
	}
	if requestURL != HuimengiDefaultBaseURL+HuimengiCreateEndpoint {
		t.Fatalf("request URL = %q", requestURL)
	}

	reader, err := adaptor.BuildRequestBody(ctx, info)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	data, _ := io.ReadAll(reader)
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["model"] != HuimengiH3Model {
		t.Fatalf("model = %#v", payload["model"])
	}
	params, ok := payload["params"].(map[string]any)
	if !ok {
		t.Fatalf("params missing: %#v", payload)
	}
	if params["prompt"] != "a quiet ocean at sunrise" || params["duration"] != float64(4) || params["resolution"] != Resolution768P || params["ratio"] != "16:9" {
		t.Fatalf("unexpected params: %#v", params)
	}
	if payload["webhook_url"] != "https://example.com/webhook" {
		t.Fatalf("webhook_url = %#v", payload["webhook_url"])
	}
	if _, exists := payload["content"]; exists {
		t.Fatalf("platform content leaked to Huimengi: %#v", payload)
	}
}

func TestHuimengiH3MapsImageAndReferenceInputs(t *testing.T) {
	request := &relaycommon.TaskSubmitReq{
		Prompt:   "animate the references",
		Duration: 5,
		Content: []map[string]interface{}{
			{"type": "image_url", "image_url": map[string]interface{}{"url": "https://example.com/ref.png"}, "role": "reference_image"},
			{"type": "video_url", "video_url": map[string]interface{}{"url": "https://example.com/ref.mp4"}, "role": "reference_video"},
			{"type": "audio_url", "audio_url": map[string]interface{}{"url": "https://example.com/ref.mp3"}, "role": "reference_audio"},
		},
	}
	body, err := buildHuimengiH3Request(request)
	if err != nil {
		t.Fatal(err)
	}
	if body.Params.Ratio != "adaptive" || len(body.Params.ReferenceImages) != 1 || len(body.Params.ReferenceVideos) != 1 || len(body.Params.ReferenceAudios) != 1 {
		t.Fatalf("unexpected media params: %+v", body.Params)
	}
	if body.Params.ReferenceImages[0] != "https://example.com/ref.png" || body.Params.ReferenceVideos[0] != "https://example.com/ref.mp4" || body.Params.ReferenceAudios[0] != "https://example.com/ref.mp3" {
		t.Fatalf("media URLs not mapped: %+v", body.Params)
	}
}

func TestHuimengiH3QueryAndStatuses(t *testing.T) {
	if got := huimengiH3QueryURL(HuimengiDefaultBaseURL+"/", "task/id"); got != HuimengiDefaultBaseURL+HuimengiQueryEndpoint+"/task%2Fid" {
		t.Fatalf("query URL = %q", got)
	}

	tests := []struct {
		name   string
		body   string
		status model.TaskStatus
	}{
		{name: "pending", body: `{"id":"hm-1","model":"minimax-h3","status":"pending"}`, status: model.TaskStatusQueued},
		{name: "processing", body: `{"id":"hm-1","model":"minimax-h3","status":"processing"}`, status: model.TaskStatusInProgress},
		{name: "failed", body: `{"id":"hm-1","model":"minimax-h3","status":"failed","error_message":"blocked"}`, status: model.TaskStatusFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != string(test.status) {
				t.Fatalf("status = %q, want %q", result.Status, test.status)
			}
		})
	}

	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{
      "id":"hm-complete",
      "model":"minimax-h3",
      "status":"completed",
      "result":{"video_url":"https://example.com/video.mp4","duration":5,"resolution":"2K","ratio":"adaptive"},
      "cost":3.6
    }`))
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != string(model.TaskStatusSuccess) || result.Url != "https://example.com/video.mp4" || result.Duration != 5 {
		t.Fatalf("unexpected completed result: %+v", result)
	}
	if math.Abs(result.BillingUnits-8) > 1e-9 {
		t.Fatalf("billing units = %v, want 8", result.BillingUnits)
	}
}

func TestHuimengiH3ResponseAndPublicConversionHideUpstreamID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	info := huimengiTestInfo(H3Model)
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{PublicTaskID: "public-hm-1"}

	taskID, data, taskErr := (&TaskAdaptor{}).doHuimengiResponse(ctx, []byte(`{"task_id":"upstream-hm-1","status":"pending"}`), 200, info)
	if taskErr != nil || taskID != "upstream-hm-1" || !strings.Contains(string(data), "upstream-hm-1") {
		t.Fatalf("submit response = id=%q data=%s err=%+v", taskID, data, taskErr)
	}
	if strings.Contains(recorder.Body.String(), "upstream-hm-1") || !strings.Contains(recorder.Body.String(), "public-hm-1") {
		t.Fatalf("public response leaked upstream ID: %s", recorder.Body.String())
	}

	task := &model.Task{
		TaskID: "public-hm-1",
		Status: model.TaskStatusSuccess,
		Properties: model.Properties{
			OriginModelName:   H3Model,
			UpstreamModelName: H3Model,
		},
		PrivateData: model.TaskPrivateData{ResultURL: "https://oss.example.com/video.mp4"},
		Data:        []byte(`{"id":"upstream-hm-1","model":"minimax-h3","status":"completed","result":{"video_url":"https://example.com/video.mp4","duration":5,"resolution":"768P","ratio":"16:9"},"cost":2.25}`),
	}
	converted, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(converted), "upstream-hm-1") {
		t.Fatalf("converted response leaked upstream ID: %s", converted)
	}
	var response dto.OpenAIVideo
	if err := json.Unmarshal(converted, &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != "public-hm-1" || response.Seconds != "5" || response.Metadata["resolution"] != "768P" {
		t.Fatalf("unexpected converted response: %+v", response)
	}
}

func huimengiTestInfo(modelName string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		OriginModelName: modelName,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    HuimengiDefaultBaseURL,
			UpstreamModelName: modelName,
		},
	}
}
