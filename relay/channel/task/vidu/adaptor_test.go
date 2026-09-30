package vidu

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestBuildRequestURLUsesOfficialViduPaths(t *testing.T) {
	tests := []struct {
		action string
		want   string
	}{
		{constant.TaskActionTextGenerate, "https://api.vidu.com/ent/v2/text2video"},
		{constant.TaskActionGenerate, "https://api.vidu.com/ent/v2/img2video"},
		{constant.TaskActionFirstTailGenerate, "https://api.vidu.com/ent/v2/start-end2video"},
		{constant.TaskActionReferenceGenerate, "https://api.vidu.com/ent/v2/reference2video"},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			adaptor := &TaskAdaptor{baseURL: "https://api.vidu.com/"}
			info := newViduRelayInfo("https://api.vidu.com", "key", "viduq3-pro")
			info.Action = tt.action
			got, err := adaptor.BuildRequestURL(info)
			if err != nil {
				t.Fatalf("BuildRequestURL() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("BuildRequestURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestViduQ3BuildsOfficialRequestBodies(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantAction string
		assert     func(t *testing.T, payload map[string]interface{})
	}{
		{
			name: "q3 pro text2video full fields",
			body: `{
				"model":"viduq3-pro",
				"mode":"text2video",
				"prompt":"wide establishing shot",
				"duration":5,
				"resolution":"720p",
				"aspect_ratio":"16:9",
				"audio":true,
				"off_peak":true,
				"bgm":true,
				"watermark":false,
				"wm_position":3,
				"wm_url":"https://cdn.example.com/wm.png",
				"meta_data":"{\"order\":\"A100\"}",
				"callback_url":"https://client.example.com/cb"
			}`,
			wantAction: constant.TaskActionTextGenerate,
			assert: func(t *testing.T, payload map[string]interface{}) {
				requireEqual(t, payload["model"], "viduq3-pro")
				requireEqual(t, payload["prompt"], "wide establishing shot")
				requireEqual(t, payload["duration"], float64(5))
				requireEqual(t, payload["resolution"], "720p")
				requireEqual(t, payload["aspect_ratio"], "16:9")
				requireEqual(t, payload["audio"], true)
				requireEqual(t, payload["off_peak"], true)
				requireEqual(t, payload["watermark"], false)
				requireEqual(t, payload["wm_position"], float64(3))
				requireAbsent(t, payload, "bgm")
				requireAbsent(t, payload, "images")
				requireAbsent(t, payload, "subjects")
				requireAbsent(t, payload, "is_rec")
			},
		},
		{
			name: "q3 turbo img2video allows optional prompt and is_rec",
			body: `{
				"model":"viduq3-turbo",
				"mode":"img2video",
				"images":["https://cdn.example.com/first.png"],
				"duration":8,
				"resolution":"540P",
				"audio":false,
				"is_rec":true
			}`,
			wantAction: constant.TaskActionGenerate,
			assert: func(t *testing.T, payload map[string]interface{}) {
				requireEqual(t, payload["model"], "viduq3-turbo")
				requireEqual(t, payload["duration"], float64(8))
				requireEqual(t, payload["resolution"], "540p")
				requireEqual(t, payload["audio"], false)
				requireEqual(t, payload["is_rec"], true)
				requireStringSlice(t, payload["images"], []string{"https://cdn.example.com/first.png"})
				requireAbsent(t, payload, "aspect_ratio")
				requireAbsent(t, payload, "subjects")
			},
		},
		{
			name: "q3 pro fast img2video defaults audio",
			body: `{
				"model":"viduq3-pro-fast",
				"mode":"img2video",
				"images":["https://cdn.example.com/first.png"],
				"duration":8,
				"resolution":"1080P"
			}`,
			wantAction: constant.TaskActionGenerate,
			assert: func(t *testing.T, payload map[string]interface{}) {
				requireEqual(t, payload["model"], "viduq3-pro-fast")
				requireEqual(t, payload["duration"], float64(8))
				requireEqual(t, payload["resolution"], "1080p")
				requireEqual(t, payload["audio"], true)
				requireStringSlice(t, payload["images"], []string{"https://cdn.example.com/first.png"})
				requireAbsent(t, payload, "is_rec")
			},
		},
		{
			name: "q3 mix reference2video without subjects",
			body: `{
				"model":"viduq3-mix",
				"mode":"reference2video",
				"prompt":"keep this scene steady",
				"images":["https://cdn.example.com/a.png"],
				"duration":3,
				"resolution":"720p"
			}`,
			wantAction: constant.TaskActionReferenceGenerate,
			assert: func(t *testing.T, payload map[string]interface{}) {
				requireEqual(t, payload["model"], "viduq3-mix")
				requireEqual(t, payload["duration"], float64(3))
				requireEqual(t, payload["resolution"], "720p")
				requireStringSlice(t, payload["images"], []string{"https://cdn.example.com/a.png"})
				requireAbsent(t, payload, "audio")
				requireAbsent(t, payload, "subjects")
			},
		},
		{
			name: "q3 turbo start-end2video preserves seed zero",
			body: `{
				"model":"viduq3-turbo",
				"mode":"start-end2video",
				"prompt":"match the first and last frame",
				"images":["https://cdn.example.com/first.png","https://cdn.example.com/last.png"],
				"duration":16,
				"resolution":"1080P",
				"seed":0,
				"is_rec":true
			}`,
			wantAction: constant.TaskActionFirstTailGenerate,
			assert: func(t *testing.T, payload map[string]interface{}) {
				requireEqual(t, payload["resolution"], "1080p")
				requireEqual(t, payload["duration"], float64(16))
				requireEqual(t, payload["seed"], float64(0))
				requireEqual(t, payload["is_rec"], true)
				requireStringSlice(t, payload["images"], []string{"https://cdn.example.com/first.png", "https://cdn.example.com/last.png"})
				requireAbsent(t, payload, "aspect_ratio")
			},
		},
		{
			name: "q3 turbo ordinary reference2video",
			body: `{
				"model":"viduq3-turbo",
				"mode":"reference2video",
				"prompt":"keep these subjects consistent",
				"images":["https://cdn.example.com/a.png","https://cdn.example.com/b.png","https://cdn.example.com/c.png"],
				"duration":3,
				"resolution":"720p",
				"aspect_ratio":"1:1"
			}`,
			wantAction: constant.TaskActionReferenceGenerate,
			assert: func(t *testing.T, payload map[string]interface{}) {
				requireEqual(t, payload["model"], "viduq3-turbo")
				requireEqual(t, payload["duration"], float64(3))
				requireEqual(t, payload["aspect_ratio"], "1:1")
				requireStringSlice(t, payload["images"], []string{"https://cdn.example.com/a.png", "https://cdn.example.com/b.png", "https://cdn.example.com/c.png"})
				requireAbsent(t, payload, "is_rec")
				requireAbsent(t, payload, "audio_type")
			},
		},
		{
			name: "q3 turbo subject reference2video",
			body: `{
				"model":"viduq3-turbo",
				"mode":"reference2video",
				"prompt":"two recurring characters speak",
				"subjects":[
					{"name":"host","images":["https://cdn.example.com/host1.png","https://cdn.example.com/host2.png"],"voice_id":"voice-host"},
					{"name":"guest","server_id":"subj_123"}
				],
				"auto_subjects":true,
				"audio":true,
				"audio_type":"speech_only",
				"off_peak":true,
				"duration":5,
				"resolution":"720p",
				"aspect_ratio":"16:9"
			}`,
			wantAction: constant.TaskActionReferenceGenerate,
			assert: func(t *testing.T, payload map[string]interface{}) {
				requireEqual(t, payload["auto_subjects"], true)
				requireEqual(t, payload["audio_type"], "speech_only")
				requireEqual(t, payload["off_peak"], true)
				requireAbsent(t, payload, "images")
				subjects, ok := payload["subjects"].([]interface{})
				if !ok || len(subjects) != 2 {
					t.Fatalf("subjects = %#v, want 2 items", payload["subjects"])
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newViduTestContext(tt.body)
			info := newViduRelayInfo("https://api.vidu.com", "key", "")
			adaptor := &TaskAdaptor{}
			adaptor.Init(info)
			if taskErr := adaptor.ValidateRequestAndSetAction(c, info); taskErr != nil {
				t.Fatalf("ValidateRequestAndSetAction() error = %s", taskErr.Message)
			}
			if info.Action != tt.wantAction {
				t.Fatalf("action = %q, want %q", info.Action, tt.wantAction)
			}
			reader, err := adaptor.BuildRequestBody(c, info)
			if err != nil {
				t.Fatalf("BuildRequestBody() error = %v", err)
			}
			payload := readJSONMap(t, reader)
			tt.assert(t, payload)
		})
	}
}

func TestViduQ3ValidationRejectsUnsupportedCombinations(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "pro reference rejected",
			body: `{"model":"viduq3-pro","mode":"reference2video","prompt":"ref","images":["https://cdn.example.com/a.png"],"duration":3}`,
			want: "reference2video is not supported by viduq3-pro",
		},
		{
			name: "pro-fast text rejected",
			body: `{"model":"viduq3-pro-fast","mode":"text2video","prompt":"ref","duration":3}`,
			want: "text2video is not supported by viduq3-pro-fast",
		},
		{
			name: "pro-fast start end rejected",
			body: `{"model":"viduq3-pro-fast","mode":"start-end2video","prompt":"ref","images":["https://cdn.example.com/a.png","https://cdn.example.com/b.png"],"duration":3}`,
			want: "start-end2video is not supported by viduq3-pro-fast",
		},
		{
			name: "mix subjects rejected",
			body: `{"model":"viduq3-mix","mode":"reference2video","prompt":"ref","subjects":[{"name":"host","images":["https://cdn.example.com/a.png"]}],"duration":3}`,
			want: "subjects are not supported by viduq3-mix",
		},
		{
			name: "top level videos rejected",
			body: `{"model":"viduq3-turbo","mode":"reference2video","prompt":"ref","videos":["https://cdn.example.com/a.mp4"],"duration":3}`,
			want: "top-level videos is not supported",
		},
		{
			name: "off peak requires audio",
			body: `{"model":"viduq3-turbo","mode":"text2video","prompt":"quiet","audio":false,"off_peak":true}`,
			want: "off_peak=true requires audio=true",
		},
		{
			name: "bad meta data",
			body: `{"model":"viduq3-turbo","mode":"text2video","prompt":"bad meta","meta_data":"not-json"}`,
			want: "meta_data must be a valid JSON string",
		},
		{
			name: "is rec not text",
			body: `{"model":"viduq3-turbo","mode":"text2video","prompt":"bad rec","is_rec":true}`,
			want: "is_rec is only supported",
		},
		{
			name: "subject videos rejected",
			body: `{"model":"viduq3-turbo","mode":"reference2video","prompt":"ref","subjects":[{"name":"host","videos":["https://cdn.example.com/a.mp4"]}],"duration":3}`,
			want: "videos is not supported",
		},
		{
			name: "invalid reference aspect ratio",
			body: `{"model":"viduq3-turbo","mode":"reference2video","prompt":"ref","images":["https://cdn.example.com/a.png"],"duration":3,"aspect_ratio":"4:3"}`,
			want: "aspect_ratio for reference2video",
		},
		{
			name: "auto subjects without subjects rejected",
			body: `{"model":"viduq3-turbo","mode":"reference2video","prompt":"ref","images":["https://cdn.example.com/a.png"],"duration":3,"auto_subjects":true}`,
			want: "auto_subjects is only supported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newViduTestContext(tt.body)
			info := newViduRelayInfo("", "", "")
			err := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
			if err == nil {
				t.Fatalf("ValidateRequestAndSetAction() error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Message, tt.want) {
				t.Fatalf("error = %q, want contains %q", err.Message, tt.want)
			}
		})
	}
}

func TestViduQ3ValidationRejectsPromptLengthLimits(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "text2video prompt over 5000 rejected",
			body: `{"model":"viduq3-pro","mode":"text2video","prompt":"` + strings.Repeat("x", 5001) + `"}`,
			want: "prompt for text2video must be at most 5000 characters",
		},
		{
			name: "ordinary reference prompt over 2000 rejected",
			body: `{"model":"viduq3-turbo","mode":"reference2video","images":["https://cdn.example.com/a.png"],"duration":3,"prompt":"` + strings.Repeat("x", 2001) + `"}`,
			want: "prompt for reference2video must be at most 2000 characters",
		},
		{
			name: "subject reference prompt over 5000 rejected",
			body: `{"model":"viduq3-turbo","mode":"reference2video","subjects":[{"name":"host"}],"duration":3,"prompt":"` + strings.Repeat("x", 5001) + `"}`,
			want: "prompt for reference2video must be at most 5000 characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newViduTestContext(tt.body)
			info := newViduRelayInfo("", "", "")
			err := (&TaskAdaptor{}).ValidateRequestAndSetAction(c, info)
			if err == nil {
				t.Fatalf("ValidateRequestAndSetAction() error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Message, tt.want) {
				t.Fatalf("error = %q, want contains %q", err.Message, tt.want)
			}
		})
	}
}

func TestViduBuildRequestSendsOfficialPathAndTokenAuth(t *testing.T) {
	var gotPath string
	var gotAuth string
	var gotBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		_, _ = w.Write([]byte(`{"task_id":"vidu_task_1","state":"created","model":"viduq3-pro"}`))
	}))
	defer server.Close()

	c, _ := newViduTestContext(`{"model":"viduq3-pro","mode":"text2video","prompt":"send smoke"}`)
	info := newViduRelayInfo(server.URL, "secret", "viduq3-pro")
	info.Action = constant.TaskActionTextGenerate
	adaptor := &TaskAdaptor{}
	adaptor.Init(info)

	upstreamURL, err := adaptor.BuildRequestURL(info)
	if err != nil {
		t.Fatalf("BuildRequestURL() error = %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, upstreamURL, strings.NewReader(`{"model":"viduq3-pro","prompt":"send smoke"}`))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if err := adaptor.BuildRequestHeader(c, req, info); err != nil {
		t.Fatalf("BuildRequestHeader() error = %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http Do() error = %v", err)
	}
	_ = resp.Body.Close()

	if gotPath != "/ent/v2/text2video" {
		t.Fatalf("path = %q, want /ent/v2/text2video", gotPath)
	}
	if gotAuth != "Token secret" {
		t.Fatalf("auth = %q, want Token secret", gotAuth)
	}
	requireEqual(t, gotBody["model"], "viduq3-pro")
}

func TestViduFetchTaskUsesOfficialCreationsEndpoint(t *testing.T) {
	var gotPath string
	var gotAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"state":"processing"}`))
	}))
	defer server.Close()

	resp, err := (&TaskAdaptor{}).FetchTask(server.URL+"/", "secret-key", map[string]any{"task_id": "task_123"}, "")
	if err != nil {
		t.Fatalf("FetchTask() error = %v", err)
	}
	_ = resp.Body.Close()

	if gotPath != "/ent/v2/tasks/task_123/creations" {
		t.Fatalf("path = %q, want /ent/v2/tasks/task_123/creations", gotPath)
	}
	if gotAuth != "Token secret-key" {
		t.Fatalf("auth = %q, want Token secret-key", gotAuth)
	}
}

func TestViduResponseAndTaskResultParsing(t *testing.T) {
	c, recorder := newViduTestContext(`{"model":"viduq3-pro","mode":"text2video","prompt":"ok"}`)
	info := newViduRelayInfo("", "", "viduq3-pro")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"task_id":"upstream_1","state":"created","model":"viduq3-pro"}`)),
	}

	taskID, raw, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	if taskErr != nil {
		t.Fatalf("DoResponse() taskErr = %v", taskErr)
	}
	if taskID != "upstream_1" {
		t.Fatalf("taskID = %q, want upstream_1", taskID)
	}
	if !strings.Contains(string(raw), "upstream_1") {
		t.Fatalf("raw response = %s", raw)
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("response code = %d, want 200", recorder.Code)
	}

	result, err := (&TaskAdaptor{}).ParseTaskResult([]byte(`{
		"state":"success",
		"credits":135,
		"payload":"client payload",
		"creations":[{"id":"creation_1","url":"https://cdn.example.com/video.mp4","cover_url":"https://cdn.example.com/cover.jpg","watermarked_url":"https://cdn.example.com/wm.mp4"}]
	}`))
	if err != nil {
		t.Fatalf("ParseTaskResult() error = %v", err)
	}
	if result.Status != model.TaskStatusSuccess {
		t.Fatalf("status = %q, want SUCCESS", result.Status)
	}
	if result.Url != "https://cdn.example.com/video.mp4" {
		t.Fatalf("url = %q", result.Url)
	}
	if result.TotalTokens != 135 || result.CompletionTokens != 135 {
		t.Fatalf("tokens = %d/%d, want 135/135", result.CompletionTokens, result.TotalTokens)
	}
}

func TestViduOfficialSubmitResponseUsesPublicTaskID(t *testing.T) {
	c, recorder := newViduTestContext(`{"model":"viduq3-pro","mode":"text2video","prompt":"ok"}`)
	c.Set("vidu_official_api", true)
	info := newViduRelayInfo("", "", "viduq3-pro")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"task_id":"upstream_1","state":"created","model":"viduq3-pro"}`)),
	}

	taskID, raw, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	if taskErr != nil {
		t.Fatalf("DoResponse() taskErr = %v", taskErr)
	}
	if taskID != "upstream_1" {
		t.Fatalf("taskID = %q, want upstream_1", taskID)
	}
	if !strings.Contains(string(raw), "upstream_1") {
		t.Fatalf("raw response = %s", raw)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal response error = %v, body = %s", err, recorder.Body.String())
	}
	if payload["task_id"] != "task_public" {
		t.Fatalf("client task_id = %v, want task_public", payload["task_id"])
	}
	if payload["state"] != "created" {
		t.Fatalf("state = %v", payload["state"])
	}
}

func TestViduOfficialDoResponseUsesPublicTaskID(t *testing.T) {
	c, recorder := newViduTestContext(`{"model":"viduq3-pro","mode":"text2video","prompt":"ok"}`)
	c.Set("vidu_official_api", true)
	info := newViduRelayInfo("", "", "viduq3-pro")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"task_id":"upstream_1","state":"created","model":"viduq3-pro","payload":"client"}`)),
	}

	taskID, raw, taskErr := (&TaskAdaptor{}).DoResponse(c, resp, info)
	if taskErr != nil {
		t.Fatalf("DoResponse() taskErr = %v", taskErr)
	}
	if taskID != "upstream_1" {
		t.Fatalf("taskID = %q, want upstream_1", taskID)
	}
	if !strings.Contains(string(raw), "upstream_1") {
		t.Fatalf("raw response = %s", raw)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("Unmarshal response error = %v, body = %s", err, recorder.Body.String())
	}
	if got["task_id"] != "task_public" {
		t.Fatalf("public task_id = %#v", got["task_id"])
	}
	if strings.Contains(recorder.Body.String(), "upstream_1") {
		t.Fatalf("official response leaked upstream task id: %s", recorder.Body.String())
	}
}

func TestViduConvertToViduOfficialHidesUpstreamTaskID(t *testing.T) {
	raw, err := (&TaskAdaptor{}).ConvertToViduOfficial(&model.Task{
		TaskID:    "task_public",
		Status:    model.TaskStatusSuccess,
		CreatedAt: 100,
		UpdatedAt: 200,
		Data:      []byte(`{"task_id":"upstream_1","state":"processing","credits":55,"creations":[{"url":"https://cdn.example.com/video.mp4"}]}`),
	})
	if err != nil {
		t.Fatalf("ConvertToViduOfficial() error = %v", err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("Unmarshal response error = %v, body = %s", err, raw)
	}
	if got["task_id"] != "task_public" {
		t.Fatalf("task_id = %#v", got["task_id"])
	}
	if got["state"] != "success" {
		t.Fatalf("state = %#v", got["state"])
	}
	if strings.Contains(string(raw), "upstream_1") {
		t.Fatalf("official response leaked upstream task id: %s", raw)
	}
}

func TestViduUpstreamErrorResponseKeepsReasonAndMessage(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader(`{"reason":"rate_limit","message":"Too many requests","metadata":{"trace_id":"trace-1"}}`)),
	}
	_, raw, taskErr := (&TaskAdaptor{}).DoResponse(nil, resp, newViduRelayInfo("", "", ""))
	if taskErr == nil {
		t.Fatal("DoResponse() taskErr = nil")
	}
	if taskErr.Code != "rate_limit" || taskErr.Message != "Too many requests" || taskErr.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("taskErr = %+v", taskErr)
	}
	if !strings.Contains(string(raw), "trace-1") {
		t.Fatalf("raw response = %s", raw)
	}
}

func TestViduConvertToOpenAIVideoIncludesCreationsAndCredits(t *testing.T) {
	raw, err := (&TaskAdaptor{}).ConvertToOpenAIVideo(&model.Task{
		TaskID:    "task_public",
		Status:    model.TaskStatusSuccess,
		Progress:  "100%",
		CreatedAt: 100,
		UpdatedAt: 200,
		Data: []byte(`{
			"state":"success",
			"credits":88,
			"payload":"user payload",
			"creations":[{"id":"creation_1","url":"https://cdn.example.com/video.mp4","cover_url":"https://cdn.example.com/cover.jpg"}]
		}`),
	})
	if err != nil {
		t.Fatalf("ConvertToOpenAIVideo() error = %v", err)
	}
	var video dto.OpenAIVideo
	if err := json.Unmarshal(raw, &video); err != nil {
		t.Fatalf("Unmarshal video error = %v, body = %s", err, raw)
	}
	if video.Metadata["url"] != "https://cdn.example.com/video.mp4" {
		t.Fatalf("metadata.url = %v", video.Metadata["url"])
	}
	if video.Metadata["cover_url"] != "https://cdn.example.com/cover.jpg" {
		t.Fatalf("metadata.cover_url = %v", video.Metadata["cover_url"])
	}
	if video.Usage["total_tokens"].(float64) != 88 {
		t.Fatalf("usage.total_tokens = %v", video.Usage["total_tokens"])
	}
}

func TestViduQ3CreditEstimationMatrix(t *testing.T) {
	tests := []struct {
		name   string
		body   requestPayload
		action string
		want   int
	}{}
	for _, tt := range []struct {
		name     string
		model    string
		action   string
		offPeak  bool
		bySecond map[string]int
	}{
		{
			name:     "pro standard immediate",
			model:    viduQ3ProModel,
			action:   constant.TaskActionTextGenerate,
			bySecond: map[string]int{"540p": 9, "720p": 20, "1080p": 24},
		},
		{
			name:     "pro standard off peak",
			model:    viduQ3ProModel,
			action:   constant.TaskActionGenerate,
			offPeak:  true,
			bySecond: map[string]int{"540p": 5, "720p": 10, "1080p": 12},
		},
		{
			name:     "turbo standard immediate",
			model:    viduQ3TurboModel,
			action:   constant.TaskActionFirstTailGenerate,
			bySecond: map[string]int{"540p": 7, "720p": 11, "1080p": 13},
		},
		{
			name:     "turbo standard off peak",
			model:    viduQ3TurboModel,
			action:   constant.TaskActionGenerate,
			offPeak:  true,
			bySecond: map[string]int{"540p": 4, "720p": 6, "1080p": 7},
		},
		{
			name:     "turbo reference immediate",
			model:    viduQ3TurboModel,
			action:   constant.TaskActionReferenceGenerate,
			bySecond: map[string]int{"540p": 4, "720p": 10, "1080p": 13},
		},
		{
			name:     "turbo reference off peak",
			model:    viduQ3TurboModel,
			action:   constant.TaskActionReferenceGenerate,
			offPeak:  true,
			bySecond: map[string]int{"540p": 2, "720p": 5, "1080p": 7},
		},
		{
			name:     "pro fast image immediate",
			model:    viduQ3ProFastModel,
			action:   constant.TaskActionGenerate,
			bySecond: map[string]int{"720p": 20, "1080p": 25},
		},
		{
			name:     "pro fast image off peak",
			model:    viduQ3ProFastModel,
			action:   constant.TaskActionGenerate,
			offPeak:  true,
			bySecond: map[string]int{"720p": 10, "1080p": 13},
		},
		{
			name:     "q3 reference immediate",
			model:    viduQ3Model,
			action:   constant.TaskActionReferenceGenerate,
			bySecond: map[string]int{"540p": 7, "720p": 12, "1080p": 15},
		},
		{
			name:     "q3 reference off peak",
			model:    viduQ3Model,
			action:   constant.TaskActionReferenceGenerate,
			offPeak:  true,
			bySecond: map[string]int{"540p": 4, "720p": 6, "1080p": 7},
		},
		{
			name:     "mix reference immediate",
			model:    viduQ3MixModel,
			action:   constant.TaskActionReferenceGenerate,
			bySecond: map[string]int{"720p": 24, "1080p": 29},
		},
	} {
		for resolution, rate := range tt.bySecond {
			body := requestPayload{Model: tt.model, Duration: intPtr(3), Resolution: resolution}
			if tt.offPeak {
				body.OffPeak = boolPtr(true)
			}
			tests = append(tests, struct {
				name   string
				body   requestPayload
				action string
				want   int
			}{
				name:   tt.name + " " + resolution,
				body:   body,
				action: tt.action,
				want:   rate * 3,
			})
		}
	}
	tests = append(tests, struct {
		name   string
		body   requestPayload
		action string
		want   int
	}{
		name:   "img2video is_rec adds ten credits",
		body:   requestPayload{Model: viduQ3TurboModel, Duration: intPtr(5), Resolution: "540p", IsRec: boolPtr(true)},
		action: constant.TaskActionGenerate,
		want:   45,
	})
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := EstimateViduQ3Credits(&tt.body, tt.action)
			if !ok {
				t.Fatal("EstimateViduQ3Credits() ok = false")
			}
			if got != tt.want {
				t.Fatalf("credits = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestViduEstimateTieredBillingTokensUsesOfficialCredits(t *testing.T) {
	c, _ := newViduTestContext(`{"model":"viduq3-turbo","mode":"text2video","prompt":"ok","duration":5,"resolution":"720p"}`)
	info := newViduRelayInfo("", "", "viduq3-turbo")
	adaptor := &TaskAdaptor{}
	if taskErr := adaptor.ValidateRequestAndSetAction(c, info); taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() error = %s", taskErr.Message)
	}

	params, ok := adaptor.EstimateTieredBillingTokens(c, info)

	if !ok {
		t.Fatal("EstimateTieredBillingTokens() ok = false")
	}
	if params.C != 55 {
		t.Fatalf("estimated credits = %v, want 55", params.C)
	}
}

func TestViduAdjustBillingOnCompleteUsesActualCredits(t *testing.T) {
	task := &model.Task{
		Quota: 1100,
		PrivateData: model.TaskPrivateData{
			BillingContext: &model.TaskBillingContext{
				OtherRatios: map[string]float64{
					"vidu_q3_estimated_credits": 55,
				},
			},
		},
	}
	got := (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{TotalTokens: 60})
	if got != 1200 {
		t.Fatalf("AdjustBillingOnComplete() = %d, want 1200", got)
	}
}

func TestViduAdjustBillingOnCompleteSkipsTieredExpr(t *testing.T) {
	task := &model.Task{
		Quota: 1100,
		PrivateData: model.TaskPrivateData{
			BillingContext: &model.TaskBillingContext{
				TieredBillingSnapshot: &billingexpr.BillingSnapshot{BillingMode: "tiered_expr"},
				OtherRatios: map[string]float64{
					"vidu_q3_estimated_credits": 55,
				},
			},
		},
	}
	got := (&TaskAdaptor{}).AdjustBillingOnComplete(task, &relaycommon.TaskInfo{TotalTokens: 60})
	if got != 0 {
		t.Fatalf("AdjustBillingOnComplete() = %d, want 0", got)
	}
}

func newViduTestContext(body string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, w
}

func newViduRelayInfo(baseURL, apiKey, modelName string) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		OriginModelName: modelName,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeVidu,
			ChannelBaseUrl:    baseURL,
			ApiKey:            apiKey,
			UpstreamModelName: modelName,
		},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			PublicTaskID: "task_public",
		},
	}
	return info
}

func readJSONMap(t *testing.T, reader io.Reader) map[string]interface{} {
	t.Helper()
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal payload error = %v, body = %s", err, raw)
	}
	return payload
}

func requireEqual(t *testing.T, got interface{}, want interface{}) {
	t.Helper()
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func requireAbsent(t *testing.T, payload map[string]interface{}, key string) {
	t.Helper()
	if _, ok := payload[key]; ok {
		t.Fatalf("payload unexpectedly contains %q: %#v", key, payload)
	}
}

func requireStringSlice(t *testing.T, got interface{}, want []string) {
	t.Helper()
	items, ok := got.([]interface{})
	if !ok {
		t.Fatalf("slice = %#v, want %#v", got, want)
	}
	if len(items) != len(want) {
		t.Fatalf("slice len = %d, want %d: %#v", len(items), len(want), got)
	}
	for i, item := range items {
		if item != want[i] {
			t.Fatalf("slice[%d] = %#v, want %#v", i, item, want[i])
		}
	}
}
