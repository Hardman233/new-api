package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

type upstreamCaptureReadCloser struct {
	*strings.Reader
	closed bool
}

func (r *upstreamCaptureReadCloser) Close() error {
	r.closed = true
	return nil
}

func TestApplySameChannelRetryLogMetadata(t *testing.T) {
	snapshot := upstreamResponseLogSnapshot{row: model.UpstreamResponseLog{
		StatusCode: http.StatusTooManyRequests,
		Other:      `{"existing":true}`,
	}}
	info := &relaycommon.RelayInfo{
		ChannelRetryActive: true,
		ChannelRetryIndex:  2,
		ChannelRetryMax:    3,
	}

	applySameChannelRetryLogMetadata(&snapshot, info)
	if snapshot.row.RetryCount != 2 {
		t.Fatalf("retry_count=%d", snapshot.row.RetryCount)
	}
	var other map[string]interface{}
	if err := json.Unmarshal([]byte(snapshot.row.Other), &other); err != nil {
		t.Fatal(err)
	}
	if other["retry_scope"] != "same_channel" || other["attempt"] != float64(3) || other["max_retries"] != float64(3) {
		t.Fatalf("retry metadata=%v", other)
	}
	if other["retry_reason"] != "http_status:429" || other["existing"] != true {
		t.Fatalf("retry reason metadata=%v", other)
	}
}

func TestUpstreamResponseBodyCapturePreservesBytesAndClose(t *testing.T) {
	originalEnabled := common.UpstreamResponseLogEnabled
	common.UpstreamResponseLogEnabled = true
	t.Cleanup(func() { common.UpstreamResponseLogEnabled = originalEnabled })

	want := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"OK\"}\n\n"
	originalBody := &upstreamCaptureReadCloser{Reader: strings.NewReader(want)}
	resp := &http.Response{Body: originalBody}

	capture := StartUpstreamResponseBodyCapture(resp)
	if capture == nil {
		t.Fatal("capture is nil")
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatal(err)
	}

	if string(got) != want {
		t.Fatalf("read body changed:\n got: %q\nwant: %q", got, want)
	}
	if capture.body.String() != want {
		t.Fatalf("captured body changed:\n got: %q\nwant: %q", capture.body.String(), want)
	}
	if !originalBody.closed {
		t.Fatal("original response body was not closed")
	}
}

func TestExtractUpstreamUsageSupportsResponsesSSE(t *testing.T) {
	raw := []byte("event: response.created\n" +
		"data: {\"type\":\"response.created\",\"response\":{\"usage\":null}}\n\n" +
		"event: response.completed\r\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":35,\"output_tokens\":12,\"total_tokens\":47}}}\r\n\r\n" +
		"data: [DONE]\n\n")

	usage := extractUpstreamUsage(raw)
	if usage.PromptTokens != 35 || usage.CompletionTokens != 12 || usage.TotalTokens != 47 {
		t.Fatalf("usage = %+v, want 35/12/47", usage)
	}
	if usage.UsageJSON == "" {
		t.Fatal("usage JSON was not retained")
	}
}

func TestSanitizeUpstreamResponseBodyStripsMediaAndKeepsUsage(t *testing.T) {
	oldMediaToOSS := common.UpstreamResponseLogMediaToOSS
	common.UpstreamResponseLogMediaToOSS = false
	defer func() {
		common.UpstreamResponseLogMediaToOSS = oldMediaToOSS
	}()

	largeBase64 := strings.Repeat("a", 600)
	raw := []byte(`{"data":[{"b64_json":"` + largeBase64 + `"}],"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}`)

	body, mediaReplaced, rewriteErr := sanitizeUpstreamResponseBody(nil, GeneratedMediaUploadParams{}, raw)
	if rewriteErr != "" {
		t.Fatalf("rewriteErr = %q", rewriteErr)
	}
	if !mediaReplaced {
		t.Fatalf("mediaReplaced = false, want true")
	}
	if strings.Contains(string(body), largeBase64) {
		t.Fatalf("sanitized body still contains base64 payload")
	}
	if !strings.Contains(string(body), "BINARY_OMITTED") {
		t.Fatalf("sanitized body does not contain media placeholder: %s", body)
	}

	usage := extractUpstreamUsage(body)
	if usage.PromptTokens != 12 || usage.CompletionTokens != 34 || usage.TotalTokens != 46 {
		t.Fatalf("usage = %+v, want 12/34/46", usage)
	}
}

func TestExtractUpstreamUsageSupportsGeminiMetadata(t *testing.T) {
	raw := []byte(`{"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":7,"thoughtsTokenCount":3,"totalTokenCount":15}}`)
	usage := extractUpstreamUsage(raw)
	if usage.PromptTokens != 5 || usage.CompletionTokens != 10 || usage.TotalTokens != 15 {
		t.Fatalf("usage = %+v, want 5/10/15", usage)
	}
}

func TestBuildTaskUpstreamResponseLogSnapshot(t *testing.T) {
	body := []byte(`{"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	task := &model.Task{
		TaskID: "task_video_123",
		Group:  "default",
		Properties: model.Properties{
			CreateRequestID:   "req_video_123",
			OriginModelName:   "doubao-seedance-2-0-pro",
			UpstreamModelName: "ep-video",
		},
	}
	ch := &model.Channel{Id: 9, Type: 123, Name: "video-channel"}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	snapshot := buildTaskUpstreamResponseLogSnapshot(task, ch, resp, body)
	row := snapshot.row
	if row.RequestId != "req_video_123" || row.BillingID != "task_video_123" || row.TaskID != "task_video_123" {
		t.Fatalf("ids = request:%q billing:%q task:%q", row.RequestId, row.BillingID, row.TaskID)
	}
	if row.ModelName != "doubao-seedance-2-0-pro" || row.UpstreamModelName != "ep-video" {
		t.Fatalf("models = model:%q upstream:%q", row.ModelName, row.UpstreamModelName)
	}
	if row.ChannelID != 9 || row.ChannelName != "video-channel" || row.ChannelType != 123 {
		t.Fatalf("channel = id:%d name:%q type:%d", row.ChannelID, row.ChannelName, row.ChannelType)
	}
	if row.Method != http.MethodGet || row.Path != "/v1/videos/task_video_123" || row.StatusCode != http.StatusOK || row.ContentType != "application/json" {
		t.Fatalf("http fields = method:%q path:%q status:%d content:%q", row.Method, row.Path, row.StatusCode, row.ContentType)
	}
	if string(snapshot.body) != string(body) {
		t.Fatalf("snapshot body changed")
	}
}

func TestApplyUpstreamResponseRetryMetadata(t *testing.T) {
	snapshot := upstreamResponseLogSnapshot{row: model.UpstreamResponseLog{
		Other: `{"group_fallback":{"enabled":true,"source_group":"source","target_group":"backup"}}`,
	}}
	applyUpstreamResponseRetryMetadata(&snapshot, UpstreamResponseRetryMetadata{
		RetryCount:           2,
		MaxRetries:           2,
		RetryReason:          " image_url_resource_download_failed ",
		ResourceParam:        " content[3].image_url ",
		FailedResourceParams: []string{"content[1].video_url", " content[3].image_url "},
		FinalAttempt:         true,
	})

	if snapshot.row.RetryCount != 2 {
		t.Fatalf("retry_count=%d", snapshot.row.RetryCount)
	}
	var other map[string]interface{}
	if err := json.Unmarshal([]byte(snapshot.row.Other), &other); err != nil {
		t.Fatal(err)
	}
	if other["attempt"] != float64(3) || other["retry_count"] != float64(2) || other["max_retries"] != float64(2) {
		t.Fatalf("numeric retry metadata=%v", other)
	}
	if other["retry_reason"] != sd2ImageDownloadRetryReasonForTest || other["final_attempt"] != true {
		t.Fatalf("retry metadata=%v", other)
	}
	if other["resource_param"] != "content[3].image_url" {
		t.Fatalf("resource_param=%v", other["resource_param"])
	}
	failedParams, ok := other["failed_resource_params"].([]interface{})
	if !ok || len(failedParams) != 2 || failedParams[0] != "content[1].video_url" || failedParams[1] != "content[3].image_url" {
		t.Fatalf("failed_resource_params=%v", other["failed_resource_params"])
	}
	groupFallback, ok := other["group_fallback"].(map[string]interface{})
	if !ok || groupFallback["target_group"] != "backup" {
		t.Fatalf("group_fallback=%v", other["group_fallback"])
	}
}

func TestBuildUpstreamResponseLogSnapshotRecordsGroupFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Set(string(constant.ContextKeyTokenGroup), "source")
	c.Set(string(constant.ContextKeyGroupFallbackApplied), true)
	c.Set(string(constant.ContextKeyGroupFallbackSourceGroup), "source")
	c.Set(string(constant.ContextKeyUsingGroup), "backup")
	c.Set(string(constant.ContextKeyChannelId), 42)
	c.Set(string(constant.ContextKeyChannelName), "backup-channel")
	info := &relaycommon.RelayInfo{
		TokenGroup:           "source",
		UsingGroup:           "backup",
		GroupFallbackApplied: true,
		ChannelMeta:          &relaycommon.ChannelMeta{},
	}
	body := []byte(`{"id":"upstream-response"}`)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	snapshot := buildUpstreamResponseLogSnapshot(c, info, resp, body)
	if string(snapshot.body) != string(body) {
		t.Fatalf("upstream body changed: %s", snapshot.body)
	}
	var other map[string]interface{}
	if err := json.Unmarshal([]byte(snapshot.row.Other), &other); err != nil {
		t.Fatal(err)
	}
	metadata, ok := other["group_fallback"].(map[string]interface{})
	if !ok {
		t.Fatalf("group_fallback missing: %v", other)
	}
	if metadata["enabled"] != true || metadata["source_group"] != "source" || metadata["target_group"] != "backup" {
		t.Fatalf("group_fallback=%v", metadata)
	}
	if metadata["channel_id"] != float64(42) || metadata["channel_name"] != "backup-channel" {
		t.Fatalf("group_fallback channel=%v", metadata)
	}
}

const sd2ImageDownloadRetryReasonForTest = "image_url_resource_download_failed"
