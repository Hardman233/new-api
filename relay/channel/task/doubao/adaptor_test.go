package doubao

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestBuildRequestURLSupportsOfficialAndXiongdaEndpoints(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    string
	}{
		{
			name:    "official ark",
			baseURL: "https://ark.cn-beijing.volces.com/",
			want:    "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks",
		},
		{
			name:    "xiongda host",
			baseURL: "http://118.196.64.1",
			want:    "http://118.196.64.1/api/v1/doubao/create",
		},
		{
			name:    "xiongda path prefix",
			baseURL: "https://example.com/api/v1/doubao/create",
			want:    "https://example.com/api/v1/doubao/create",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			adaptor := &TaskAdaptor{baseURL: tt.baseURL}
			got, err := adaptor.BuildRequestURL(nil)
			if err != nil {
				t.Fatalf("BuildRequestURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("BuildRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFetchTaskUsesPostBodyForXiongdaEndpoint(t *testing.T) {
	var gotMethod string
	var gotPath string
	var gotAuth string
	var gotBody map[string]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		if err := json.Unmarshal(body, &gotBody); err != nil {
			t.Fatalf("Unmarshal request body error = %v, body = %s", err, body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"running"}`))
	}))
	defer server.Close()

	resp, err := (&TaskAdaptor{}).FetchTask(server.URL+"/api/v1/doubao", "secret-key", map[string]any{
		"task_id": "task-upstream-1",
	}, "")
	if err != nil {
		t.Fatalf("FetchTask() error = %v", err)
	}
	_ = resp.Body.Close()

	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/api/v1/doubao/get_result" {
		t.Fatalf("path = %s, want /api/v1/doubao/get_result", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotBody["id"] != "task-upstream-1" {
		t.Fatalf("body id = %q, want task-upstream-1", gotBody["id"])
	}
}

func TestParseTaskResultUsesFileURLFallbackAndUsage(t *testing.T) {
	body := []byte(`{
		"status": "succeeded",
		"content": {
			"file_url": "https://cdn.example.com/video.mp4",
			"last_frame_url": "https://cdn.example.com/last.png"
		},
		"usage": {
			"completion_tokens": 9,
			"total_tokens": 10,
			"tool_usage": {
				"web_search": 1
			}
		}
	}`)

	info, err := (&TaskAdaptor{}).ParseTaskResult(body)
	if err != nil {
		t.Fatalf("ParseTaskResult() error = %v", err)
	}
	if info.Status != model.TaskStatusSuccess {
		t.Fatalf("status = %s, want SUCCESS", info.Status)
	}
	if info.Url != "https://cdn.example.com/video.mp4" {
		t.Fatalf("url = %q", info.Url)
	}
	if info.CompletionTokens != 9 || info.TotalTokens != 10 {
		t.Fatalf("tokens = %d/%d, want 9/10", info.CompletionTokens, info.TotalTokens)
	}
}

func TestParseTaskResultFallsBackTotalTokensToCompletionTokens(t *testing.T) {
	body := []byte(`{
		"status": "succeeded",
		"content": {
			"video_url": "https://cdn.example.com/video.mp4"
		},
		"usage": {
			"completion_tokens": 108900
		}
	}`)

	info, err := (&TaskAdaptor{}).ParseTaskResult(body)
	if err != nil {
		t.Fatalf("ParseTaskResult() error = %v", err)
	}
	if info.CompletionTokens != 108900 || info.TotalTokens != 108900 {
		t.Fatalf("tokens = %d/%d, want 108900/108900", info.CompletionTokens, info.TotalTokens)
	}
}

func TestEstimateBillingSeedancePriceTiers(t *testing.T) {
	gin.SetMode(gin.TestMode)

	videoContent := []interface{}{
		map[string]interface{}{
			"type": "text",
			"text": "reference video style",
		},
		map[string]interface{}{
			"type": "video_url",
			"video_url": map[string]interface{}{
				"url": "https://example.com/input.mp4",
			},
		},
	}

	tests := []struct {
		name       string
		model      string
		metadata   map[string]interface{}
		wantKey    string
		wantRatio  float64
		wantExists bool
	}{
		{
			name:       "seedance 720p without video uses base price",
			model:      seedance20Model,
			metadata:   map[string]interface{}{"resolution": "720p"},
			wantExists: false,
		},
		{
			name:       "seedance 720p with video input",
			model:      seedance20Model,
			metadata:   map[string]interface{}{"resolution": "720p", "content": videoContent},
			wantKey:    "video_input",
			wantRatio:  28.0 / 46.0,
			wantExists: true,
		},
		{
			name:       "seedance 1080p without video input",
			model:      seedance20Model,
			metadata:   map[string]interface{}{"resolution": "1080p"},
			wantKey:    "seedance_1080p",
			wantRatio:  51.0 / 46.0,
			wantExists: true,
		},
		{
			name:       "seedance 1080p with video input uses exact combined tier",
			model:      seedance20Model,
			metadata:   map[string]interface{}{"resolution": "1080P", "content": videoContent},
			wantKey:    "seedance_1080p_video_input",
			wantRatio:  31.0 / 46.0,
			wantExists: true,
		},
		{
			name:       "seedance fast video input",
			model:      seedance20FastModel,
			metadata:   map[string]interface{}{"resolution": "720p", "content": videoContent},
			wantKey:    "video_input",
			wantRatio:  22.0 / 37.0,
			wantExists: true,
		},
		{
			name:       "seedance fast 1080p without video has no unsupported upcharge",
			model:      seedance20FastModel,
			metadata:   map[string]interface{}{"resolution": "1080p"},
			wantExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("task_request", relaycommon.TaskSubmitReq{
				Model:    tt.model,
				Metadata: tt.metadata,
			})

			got := (&TaskAdaptor{}).EstimateBilling(c, &relaycommon.RelayInfo{OriginModelName: tt.model})
			if !tt.wantExists {
				if len(got) != 0 {
					t.Fatalf("EstimateBilling() = %v, want no extra ratio", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("EstimateBilling() = %v, want exactly one ratio", got)
			}
			ratio, ok := got[tt.wantKey]
			if !ok {
				t.Fatalf("ratio key missing: got %v, want key %q", got, tt.wantKey)
			}
			if math.Abs(ratio-tt.wantRatio) > 1e-12 {
				t.Fatalf("ratio = %.12f, want %.12f", ratio, tt.wantRatio)
			}
		})
	}
}

func TestEstimateBillingSeedance25UsesVideoInputTierForRealRequestContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:  seedance25Model,
		Prompt: "make a video from this reference",
		Content: []map[string]interface{}{
			{"type": "text", "text": "make a video from this reference"},
			{"type": "video_url", "video_url": map[string]interface{}{
				"url": "https://example.com/reference.mp4",
			}},
		},
	})

	got := (&TaskAdaptor{}).EstimateBilling(c, &relaycommon.RelayInfo{OriginModelName: seedance25Model})
	ratio, ok := got["seedance_2_5_video_input"]
	if !ok || math.Abs(ratio-42.0/70.0) > 1e-12 {
		t.Fatalf("SD2.5 video ratio = %v, want %.12f", got, 42.0/70.0)
	}
}

func TestEstimateBillingSeedance25WithoutVideoUsesBasePrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:  seedance25Model,
		Prompt: "text to video",
	})

	got := (&TaskAdaptor{}).EstimateBilling(c, &relaycommon.RelayInfo{OriginModelName: seedance25Model})
	if ratio, ok := got["seedance_2_5_base"]; !ok || ratio != 1 {
		t.Fatalf("SD2.5 no-video ratios = %v, want base tier", got)
	}
}

func TestValidateSeedance25DefersLimitsToUpstream(t *testing.T) {
	duration := dto.IntValue(30)
	payload := &requestPayload{
		Model:      seedance25Model,
		Resolution: "2160p",
		Ratio:      "2:1",
		Duration:   &duration,
		Content: []ContentItem{
			{Type: "text", Text: "use this audio"},
			{Type: "audio_url", AudioURL: &MediaURL{URL: "https://example.com/reference.mp3"}, Role: "reference_audio"},
		},
	}
	if err := validateSeedanceRequestPayloadForModel(seedance25Model, payload); err != nil {
		t.Fatalf("SD2.5 validation error = %v, want upstream validation", err)
	}
}

func TestValidateSeedance20StillRejectsTheSameInvalidDuration(t *testing.T) {
	duration := dto.IntValue(30)
	payload := &requestPayload{
		Model:    seedance20Model,
		Duration: &duration,
		Content:  []ContentItem{{Type: "text", Text: "legacy validation"}},
	}
	if err := validateSeedanceRequestPayloadForModel(seedance20Model, payload); err == nil {
		t.Fatal("SD2.0 invalid duration was accepted")
	}
}

func TestSeedanceAllows4KResolution(t *testing.T) {
	tests := []struct {
		name string
		req  relaycommon.TaskSubmitReq
	}{
		{
			name: "top-level resolution",
			req: relaycommon.TaskSubmitReq{
				Model:      "doubao-seedance-2-0-260128",
				Prompt:     "cinematic test",
				Resolution: "4K",
				Ratio:      "16:9",
				Duration:   5,
			},
		},
		{
			name: "metadata resolution",
			req: relaycommon.TaskSubmitReq{
				Model:    "doubao-seedance-2-0-260128",
				Prompt:   "cinematic test",
				Duration: 5,
				Metadata: map[string]interface{}{
					"ratio":      "16:9",
					"resolution": "4K",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := (&TaskAdaptor{}).convertToRequestPayload(&tt.req)
			if err != nil {
				t.Fatalf("convertToRequestPayload() error = %v", err)
			}
			if err := validateSeedanceRequestPayload(body); err != nil {
				t.Fatalf("validateSeedanceRequestPayload() error = %v", err)
			}
			if body.Resolution != "4k" {
				t.Fatalf("resolution = %q, want 4k", body.Resolution)
			}
		})
	}
}

func TestConvertToRequestPayloadPassesCallbackURLAndAllowlistedMetadata(t *testing.T) {
	callbackURL := "http://127.0.0.1:18080/callback"
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:  "doubao-seedance-2-0-260128",
		Prompt: "callback smoke test",
		Metadata: map[string]interface{}{
			"callback_url":      callbackURL,
			"return_last_frame": true,
			"generate_audio":    true,
			"ratio":             "16:9",
			"resolution":        "720p",
			"duration":          5,
			"not_allowlisted":   "must be dropped",
		},
	})
	if err != nil {
		t.Fatalf("convertToRequestPayload() error = %v", err)
	}
	if payload.CallbackURL != callbackURL {
		t.Fatalf("callback_url = %q, want %q", payload.CallbackURL, callbackURL)
	}
	if payload.ReturnLastFrame == nil || !bool(*payload.ReturnLastFrame) {
		t.Fatalf("return_last_frame not passed")
	}
	if payload.GenerateAudio == nil || !bool(*payload.GenerateAudio) {
		t.Fatalf("generate_audio not passed")
	}
	if payload.Ratio != "16:9" || payload.Resolution != "720p" {
		t.Fatalf("ratio/resolution = %q/%q, want 16:9/720p", payload.Ratio, payload.Resolution)
	}
	if payload.Duration == nil || int(*payload.Duration) != 5 {
		t.Fatalf("duration = %v, want 5", payload.Duration)
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal payload error = %v", err)
	}
	if string(raw) == "" || json.Valid(raw) == false {
		t.Fatalf("invalid payload json: %s", raw)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("Unmarshal payload json error = %v", err)
	}
	if _, ok := body["not_allowlisted"]; ok {
		t.Fatalf("non-allowlisted metadata key leaked into upstream payload: %s", raw)
	}
}

func TestConvertToRequestPayloadSupportsSeedanceOfficialTopLevelFields(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model: "doubao-seedance-2-0-260128",
		Content: []map[string]interface{}{
			{
				"type": "text",
				"text": "make a cinematic product video",
			},
			{
				"type": "video_url",
				"video_url": map[string]interface{}{
					"url": "https://example.com/ref.mp4",
				},
				"role": "reference_video",
			},
			{
				"type": "audio_url",
				"audio_url": map[string]interface{}{
					"url": "https://example.com/ref.mp3",
				},
				"role": "reference_audio",
			},
		},
		Ratio:           "16:9",
		Resolution:      "1080P",
		Duration:        5,
		GenerateAudio:   false,
		ReturnLastFrame: true,
		Watermark:       false,
		Tools: []map[string]interface{}{
			{"type": "web_search"},
		},
	})
	if err != nil {
		t.Fatalf("convertToRequestPayload() error = %v", err)
	}
	if err := validateSeedanceRequestPayload(payload); err != nil {
		t.Fatalf("validateSeedanceRequestPayload() error = %v", err)
	}
	if payload.Resolution != "1080p" || payload.Ratio != "16:9" {
		t.Fatalf("resolution/ratio = %q/%q, want 1080p/16:9", payload.Resolution, payload.Ratio)
	}
	if payload.Duration == nil || int(*payload.Duration) != 5 {
		t.Fatalf("duration = %v, want 5", payload.Duration)
	}
	if payload.GenerateAudio == nil || bool(*payload.GenerateAudio) {
		t.Fatalf("generate_audio = %v, want false pointer", payload.GenerateAudio)
	}
	if payload.ReturnLastFrame == nil || !bool(*payload.ReturnLastFrame) {
		t.Fatalf("return_last_frame not passed")
	}
	if payload.Watermark == nil || bool(*payload.Watermark) {
		t.Fatalf("watermark = %v, want false pointer", payload.Watermark)
	}
	if len(payload.Tools) != 1 || payload.Tools[0].Type != "web_search" {
		t.Fatalf("tools = %+v", payload.Tools)
	}
	if len(payload.Content) != 3 {
		t.Fatalf("content len = %d, want 3", len(payload.Content))
	}
	if payload.Content[1].VideoURL == nil || payload.Content[1].Role != "reference_video" {
		t.Fatalf("video content = %+v", payload.Content[1])
	}
	if payload.Content[2].AudioURL == nil || payload.Content[2].Role != "reference_audio" {
		t.Fatalf("audio content = %+v", payload.Content[2])
	}
}

func TestConvertToRequestPayloadPassesSeedance25OutputFormat(t *testing.T) {
	tests := []struct {
		name string
		req  relaycommon.TaskSubmitReq
	}{
		{
			name: "top-level extra_body",
			req: relaycommon.TaskSubmitReq{
				Model:     seedance25Model,
				Prompt:    "cinematic product video",
				ExtraBody: map[string]interface{}{"output_format": " MOV ", "ignored": "value"},
			},
		},
		{
			name: "metadata extra_body",
			req: relaycommon.TaskSubmitReq{
				Model:  seedance25Model,
				Prompt: "cinematic product video",
				Metadata: map[string]interface{}{
					"extra_body": map[string]interface{}{"output_format": "mov", "ignored": "value"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := (&TaskAdaptor{}).convertToRequestPayload(&tt.req)
			if err != nil {
				t.Fatalf("convertToRequestPayload() error = %v", err)
			}
			if payload.ExtraBody == nil || payload.ExtraBody.OutputFormat != "mov" {
				t.Fatalf("extra_body = %+v, want output_format=mov", payload.ExtraBody)
			}

			raw, err := json.Marshal(payload)
			if err != nil {
				t.Fatalf("Marshal payload error = %v", err)
			}
			var body map[string]interface{}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("Unmarshal payload error = %v", err)
			}
			extraBody, ok := body["extra_body"].(map[string]interface{})
			if !ok || extraBody["output_format"] != "mov" {
				t.Fatalf("upstream extra_body = %#v", body["extra_body"])
			}
			if _, leaked := extraBody["ignored"]; leaked {
				t.Fatalf("unknown extra_body field leaked upstream: %s", raw)
			}
		})
	}
}

func TestConvertToRequestPayloadDoesNotPassOutputFormatToSeedance20(t *testing.T) {
	tests := []relaycommon.TaskSubmitReq{
		{
			Model:     seedance20Model,
			Prompt:    "legacy video",
			ExtraBody: map[string]interface{}{"output_format": "mov"},
		},
		{
			Model:  seedance20Model,
			Prompt: "legacy video",
			Metadata: map[string]interface{}{
				"extra_body": map[string]interface{}{"output_format": "mov"},
			},
		},
	}
	for i := range tests {
		payload, err := (&TaskAdaptor{}).convertToRequestPayload(&tests[i])
		if err != nil {
			t.Fatalf("case %d convertToRequestPayload() error = %v", i, err)
		}
		if payload.ExtraBody != nil {
			t.Fatalf("case %d SD2.0 extra_body = %+v, want nil", i, payload.ExtraBody)
		}
	}
}

func TestConvertToRequestPayloadUsesReferenceRoleForMultipleImages(t *testing.T) {
	payload, err := (&TaskAdaptor{}).convertToRequestPayload(&relaycommon.TaskSubmitReq{
		Model:  "doubao-seedance-2-0-260128",
		Prompt: "make a video from references",
		Images: []string{
			"https://example.com/a.png",
			"https://example.com/b.png",
		},
	})
	if err != nil {
		t.Fatalf("convertToRequestPayload() error = %v", err)
	}
	if err := validateSeedanceRequestPayload(payload); err != nil {
		t.Fatalf("validateSeedanceRequestPayload() error = %v", err)
	}
	if len(payload.Content) != 3 {
		t.Fatalf("content len = %d, want 3", len(payload.Content))
	}
	for i := 0; i < 2; i++ {
		if payload.Content[i].Role != "reference_image" {
			t.Fatalf("content[%d].role = %q, want reference_image", i, payload.Content[i].Role)
		}
	}
}

func TestApplyMaterialAssetsConvertsAudioURLToSD2MaterialAsset(t *testing.T) {
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	model.DB = db
	t.Cleanup(func() {
		model.DB = oldDB
	})
	if err := db.AutoMigrate(&model.SD2MaterialAsset{}); err != nil {
		t.Fatalf("migrate test db: %v", err)
	}

	var createBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("Action") {
		case "CreateAsset":
			body, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(body, &createBody); err != nil {
				t.Fatalf("unmarshal CreateAsset body: %v", err)
			}
			_, _ = w.Write([]byte(`{"Result":{"AssetId":"asset_audio_1"}}`))
		case "GetAsset":
			_, _ = w.Write([]byte(`{"Result":{"Asset":{"AssetId":"asset_audio_1","Status":"Active"}}}`))
		default:
			t.Fatalf("unexpected action %q", r.URL.Query().Get("Action"))
		}
	}))
	defer server.Close()

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)

	payload := &requestPayload{
		Model: "doubao-seedance-2-0-260128",
		Content: []ContentItem{
			{Type: "text", Text: "make a video with reference audio"},
			{Type: "audio_url", AudioURL: &MediaURL{URL: "https://cdn.example.com/ref.mp3"}, Role: "reference_audio"},
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelId: 77,
			ChannelOtherSettings: dto.ChannelOtherSettings{
				SD2MaterialEnabled:         true,
				SD2MaterialAccessKeyID:     "ak",
				SD2MaterialSecretAccessKey: "sk",
				SD2MaterialAssetGroupID:    "group-audio",
				SD2MaterialProjectName:     "0807Oc",
				SD2MaterialEndpoint:        server.URL,
				SD2MaterialPollTimeoutSec:  3,
				SD2MaterialPollIntervalSec: 1,
			},
		},
	}

	if err := (&TaskAdaptor{}).applyMaterialAssets(c, info, payload); err != nil {
		t.Fatalf("applyMaterialAssets() error = %v", err)
	}
	if payload.Content[1].AudioURL == nil || payload.Content[1].AudioURL.URL != "asset://asset_audio_1" {
		t.Fatalf("audio url = %+v, want asset://asset_audio_1", payload.Content[1].AudioURL)
	}
	if createBody["AssetType"] != "Audio" {
		t.Fatalf("CreateAsset AssetType = %#v, want Audio; body=%v", createBody["AssetType"], createBody)
	}
	if createBody["URL"] != "https://cdn.example.com/ref.mp3" {
		t.Fatalf("CreateAsset URL = %#v", createBody["URL"])
	}
	if createBody["ProjectName"] != "0807Oc" {
		t.Fatalf("CreateAsset ProjectName = %#v, want 0807Oc", createBody["ProjectName"])
	}
}

func TestValidateSeedanceRequestPayloadRejectsOfficialRuleViolations(t *testing.T) {
	tests := []struct {
		name    string
		req     relaycommon.TaskSubmitReq
		wantErr string
	}{
		{
			name: "audio alone",
			req: relaycommon.TaskSubmitReq{
				Model: "doubao-seedance-2-0-260128",
				Content: []map[string]interface{}{
					{"type": "text", "text": "use this audio"},
					{"type": "audio_url", "audio_url": map[string]interface{}{"url": "https://example.com/a.mp3"}, "role": "reference_audio"},
				},
			},
			wantErr: "audio reference cannot be used alone",
		},
		{
			name: "invalid duration",
			req: relaycommon.TaskSubmitReq{
				Model:    "doubao-seedance-2-0-260128",
				Prompt:   "too short",
				Duration: 3,
			},
			wantErr: "duration must be -1 or between 4 and 15 seconds",
		},
		{
			name: "mixed first frame and reference",
			req: relaycommon.TaskSubmitReq{
				Model: "doubao-seedance-2-0-260128",
				Content: []map[string]interface{}{
					{"type": "text", "text": "mixing modes"},
					{"type": "image_url", "image_url": map[string]interface{}{"url": "https://example.com/first.png"}, "role": "first_frame"},
					{"type": "image_url", "image_url": map[string]interface{}{"url": "https://example.com/ref.png"}, "role": "reference_image"},
				},
			},
			wantErr: "first-frame/last-frame mode cannot be mixed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := (&TaskAdaptor{}).convertToRequestPayload(&tt.req)
			if err != nil {
				t.Fatalf("convertToRequestPayload() error = %v", err)
			}
			err = validateSeedanceRequestPayload(payload)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("validateSeedanceRequestPayload() error = %v, want contains %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidateRequestAcceptsOfficialTopLevelContentText(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{
		"model": "doubao-seedance-2-0-260128",
		"content": [
			{"type": "text", "text": "make a short product video"}
		],
		"ratio": "16:9",
		"resolution": "720p",
		"duration": 5
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info); taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() error = %v", taskErr)
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		t.Fatalf("GetTaskRequest() error = %v", err)
	}
	if req.Prompt != "make a short product video" {
		t.Fatalf("prompt = %q", req.Prompt)
	}
}

func TestValidateRequestParsesSeedance25OutputFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{
		"model": "doubao-seedance-2-5-260628",
		"prompt": "make a professional video",
		"extra_body": {"output_format": "mov"}
	}`))
	c.Request.Header.Set("Content-Type", "application/json")

	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	if taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info); taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() error = %v", taskErr)
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		t.Fatalf("GetTaskRequest() error = %v", err)
	}
	if req.ExtraBody["output_format"] != "mov" {
		t.Fatalf("parsed extra_body = %#v", req.ExtraBody)
	}
}

func TestConvertToOpenAIVideoIncludesXiongdaMetadata(t *testing.T) {
	task := &model.Task{
		TaskID:    "task_public",
		Status:    model.TaskStatusSuccess,
		Progress:  "100%",
		CreatedAt: 100,
		UpdatedAt: 200,
		Properties: model.Properties{
			OriginModelName: "doubao-seedance-2-0-260128",
		},
		Data: []byte(`{
			"data": {
				"id": "upstream-task",
				"status": "succeeded",
				"content": {
					"file_url": "https://cdn.example.com/video.mp4",
					"last_frame_url": "https://cdn.example.com/last.png"
				},
				"resolution": "720p",
				"ratio": "16:9",
				"duration": 5,
				"frames": 120,
				"fileformat": "mov",
				"generate_audio": false,
				"usage": {
					"completion_tokens": 9,
					"tool_usage": {
						"web_search": 1
					}
				}
			}
		}`),
	}

	raw, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("ConvertToOpenAIVideo() error = %v", err)
	}

	var video dto.OpenAIVideo
	if err := json.Unmarshal(raw, &video); err != nil {
		t.Fatalf("Unmarshal OpenAIVideo error = %v, body = %s", err, raw)
	}
	if video.Metadata["url"] != "https://cdn.example.com/video.mp4" {
		t.Fatalf("metadata.url = %v", video.Metadata["url"])
	}
	if video.Metadata["file_url"] != "https://cdn.example.com/video.mp4" {
		t.Fatalf("metadata.file_url = %v", video.Metadata["file_url"])
	}
	if video.Metadata["last_frame_url"] != "https://cdn.example.com/last.png" {
		t.Fatalf("metadata.last_frame_url = %v", video.Metadata["last_frame_url"])
	}
	if video.Metadata["web_search"].(float64) != 1 {
		t.Fatalf("metadata.web_search = %v", video.Metadata["web_search"])
	}
	if video.Usage["completion_tokens"].(float64) != 9 {
		t.Fatalf("usage.completion_tokens = %v", video.Usage["completion_tokens"])
	}
	if video.Usage["tool_usage"].(map[string]interface{})["web_search"].(float64) != 1 {
		t.Fatalf("usage.tool_usage.web_search = %v", video.Usage["tool_usage"])
	}
	if video.Metadata["usage"].(map[string]interface{})["completion_tokens"].(float64) != 9 {
		t.Fatalf("metadata.usage = %v", video.Metadata["usage"])
	}
	if _, exists := video.Metadata["fileformat"]; exists {
		t.Fatalf("SD2.0 metadata unexpectedly contains fileformat: %v", video.Metadata)
	}
	if _, exists := video.Metadata["generate_audio"]; exists {
		t.Fatalf("SD2.0 metadata unexpectedly contains generate_audio: %v", video.Metadata)
	}
}

func TestConvertToOpenAIVideoIncludesSeedance25ResponseMetadata(t *testing.T) {
	task := &model.Task{
		TaskID:   "task_public_sd25",
		Status:   model.TaskStatusSuccess,
		Progress: "100%",
		Properties: model.Properties{
			OriginModelName: seedance25Model,
		},
		Data: []byte(`{
			"data": {
				"id": "upstream-sd25-task",
				"status": "succeeded",
				"content": {"video_url": "https://cdn.example.com/video.mov"},
				"fileformat": "mov",
				"generate_audio": false,
				"revised_prompt": "revised cinematic prompt",
				"draft": false,
				"draft_task_id": "draft-task-1",
				"subdivisionlevel": 0,
				"execution_expires_after": 172800,
				"_request_id": "request-1",
				"usage": {"completion_tokens": 100}
			}
		}`),
	}

	raw, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(task)
	if err != nil {
		t.Fatalf("ConvertToOpenAIVideo() error = %v", err)
	}

	var video dto.OpenAIVideo
	if err := json.Unmarshal(raw, &video); err != nil {
		t.Fatalf("Unmarshal OpenAIVideo error = %v, body = %s", err, raw)
	}
	want := map[string]interface{}{
		"fileformat":              "mov",
		"generate_audio":          false,
		"revised_prompt":          "revised cinematic prompt",
		"draft":                   false,
		"draft_task_id":           "draft-task-1",
		"subdivisionlevel":        float64(0),
		"execution_expires_after": float64(172800),
		"_request_id":             "request-1",
	}
	for key, value := range want {
		if got, ok := video.Metadata[key]; !ok || got != value {
			t.Fatalf("metadata[%q] = %#v, want %#v", key, got, value)
		}
	}
}
