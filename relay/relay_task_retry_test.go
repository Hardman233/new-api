package relay

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
)

const sd2ImageDownloadFailureBody = `{"error":{"code":"InvalidParameter","message":"The parameter ` + "`" + `content[1].image_url` + "`" + ` specified in the request is not valid: resource download failed. Request id: test","param":"content[1].image_url","type":"BadRequest"}}`
const sd2VideoDownloadFailureBody = `{"error":{"code":"InvalidParameter","message":"The parameter ` + "`" + `content[1].video_url` + "`" + ` specified in the request is not valid: resource download failed. Request id: test","param":"content[1].video_url","type":"BadRequest"}}`
const sd2AudioDownloadFailureBody = `{"error":{"code":"InvalidParameter","message":"The parameter ` + "`" + `content[1].audio_url` + "`" + ` specified in the request is not valid: resource download failed. Request id: test","param":"content[1].audio_url","type":"BadRequest"}}`

func withZeroTaskSubmitRetryDelays(t *testing.T) {
	t.Helper()
	original := sd2SubmitRetryDelays
	sd2SubmitRetryDelays = []time.Duration{0, 0}
	t.Cleanup(func() { sd2SubmitRetryDelays = original })
}

func taskSubmitResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func TestExecuteTaskSubmitRequestRetriesTwiceWithIdenticalBody(t *testing.T) {
	withZeroTaskSubmitRetryDelays(t)
	wantBody := []byte(`{"model":"doubao-seedance-2-0-260128","content":[{"type":"text","text":"test"}]}`)
	statuses := []int{http.StatusBadRequest, http.StatusBadRequest, http.StatusOK}
	var requestBodies [][]byte
	var attempts []service.UpstreamResponseRetryMetadata

	resp, state, err := executeTaskSubmitRequest(
		context.Background(),
		true,
		bytes.NewReader(wantBody),
		func(body io.Reader) (*http.Response, error) {
			got, readErr := io.ReadAll(body)
			if readErr != nil {
				return nil, readErr
			}
			requestBodies = append(requestBodies, got)
			status := statuses[len(requestBodies)-1]
			if status == http.StatusOK {
				return taskSubmitResponse(status, `{"id":"upstream-task"}`), nil
			}
			return taskSubmitResponse(status, sd2ImageDownloadFailureBody), nil
		},
		func(_ *http.Response, _ []byte, metadata service.UpstreamResponseRetryMetadata) {
			attempts = append(attempts, metadata)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if state.RetryCount != 2 || state.RetryReason != sd2ImageDownloadRetryReason {
		t.Fatalf("state = %+v", state)
	}
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("response status = %v", resp)
	}
	if len(requestBodies) != 3 || len(attempts) != 3 {
		t.Fatalf("requests=%d logs=%d, want 3/3", len(requestBodies), len(attempts))
	}
	for i, got := range requestBodies {
		if !bytes.Equal(got, wantBody) {
			t.Fatalf("request body %d changed: %q", i+1, got)
		}
		if attempts[i].RetryCount != i {
			t.Fatalf("attempt %d retry_count=%d", i+1, attempts[i].RetryCount)
		}
		if attempts[i].FinalAttempt != (i == 2) {
			t.Fatalf("attempt %d final_attempt=%v", i+1, attempts[i].FinalAttempt)
		}
	}
	gotResponseBody, readErr := io.ReadAll(resp.Body)
	if readErr != nil || string(gotResponseBody) != `{"id":"upstream-task"}` {
		t.Fatalf("final response body = %q, err=%v", gotResponseBody, readErr)
	}
}

func TestExecuteTaskSubmitRequestStopsAfterRetryExhaustion(t *testing.T) {
	withZeroTaskSubmitRetryDelays(t)
	calls := 0
	resp, state, err := executeTaskSubmitRequest(
		context.Background(),
		true,
		bytes.NewBufferString(`{"prompt":"test"}`),
		func(io.Reader) (*http.Response, error) {
			calls++
			return taskSubmitResponse(http.StatusBadRequest, sd2ImageDownloadFailureBody), nil
		},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || state.RetryCount != 2 {
		t.Fatalf("calls=%d state=%+v", calls, state)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestExecuteTaskSubmitRequestDoesNotRetryOtherFailures(t *testing.T) {
	withZeroTaskSubmitRetryDelays(t)
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"wrong status", http.StatusInternalServerError, sd2ImageDownloadFailureBody},
		{"wrong code", http.StatusBadRequest, `{"error":{"code":"BadRequest","param":"content[1].image_url","message":"resource download failed"}}`},
		{"wrong param", http.StatusBadRequest, `{"error":{"code":"InvalidParameter","param":"content[1].url","message":"resource download failed"}}`},
		{"wrong message", http.StatusBadRequest, `{"error":{"code":"InvalidParameter","param":"content[1].image_url","message":"invalid image"}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			_, state, err := executeTaskSubmitRequest(
				context.Background(),
				true,
				bytes.NewBufferString(`{"prompt":"test"}`),
				func(io.Reader) (*http.Response, error) {
					calls++
					return taskSubmitResponse(test.status, test.body), nil
				},
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || state.RetryCount != 0 {
				t.Fatalf("calls=%d state=%+v", calls, state)
			}
		})
	}
}

func TestExecuteTaskSubmitRequestStopsWhenContextCanceled(t *testing.T) {
	original := sd2SubmitRetryDelays
	sd2SubmitRetryDelays = []time.Duration{time.Hour, time.Hour}
	t.Cleanup(func() { sd2SubmitRetryDelays = original })

	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, state, err := executeTaskSubmitRequest(
		ctx,
		true,
		bytes.NewBufferString(`{"prompt":"test"}`),
		func(io.Reader) (*http.Response, error) {
			calls++
			return taskSubmitResponse(http.StatusBadRequest, sd2ImageDownloadFailureBody), nil
		},
		func(_ *http.Response, _ []byte, _ service.UpstreamResponseRetryMetadata) {
			cancel()
		},
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v, want context.Canceled", err)
	}
	if calls != 1 || state.RetryCount != 0 {
		t.Fatalf("calls=%d state=%+v", calls, state)
	}
}

func TestSD2ResourceDownloadRetryReasonMatchesSupportedMedia(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantReason string
	}{
		{"image", sd2ImageDownloadFailureBody, sd2ImageDownloadRetryReason},
		{"video", sd2VideoDownloadFailureBody, sd2VideoDownloadRetryReason},
		{"audio", sd2AudioDownloadFailureBody, sd2AudioDownloadRetryReason},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reason, resourceParam, matched := sd2ResourceDownloadRetryReason(http.StatusBadRequest, []byte(test.body))
			if !matched || reason != test.wantReason || resourceParam == "" {
				t.Fatalf("matched=%v reason=%q resource_param=%q, want true/%q/non-empty", matched, reason, resourceParam, test.wantReason)
			}
		})
	}
}

func TestExecuteTaskSubmitRequestAccumulatesDifferentFailedResources(t *testing.T) {
	withZeroTaskSubmitRetryDelays(t)
	bodies := []string{
		sd2ImageDownloadFailureBody,
		sd2VideoDownloadFailureBody,
		sd2AudioDownloadFailureBody,
	}
	var attempts []service.UpstreamResponseRetryMetadata

	_, state, err := executeTaskSubmitRequest(
		context.Background(),
		true,
		bytes.NewBufferString(`{"prompt":"test"}`),
		func(io.Reader) (*http.Response, error) {
			return taskSubmitResponse(http.StatusBadRequest, bodies[len(attempts)]), nil
		},
		func(_ *http.Response, _ []byte, metadata service.UpstreamResponseRetryMetadata) {
			attempts = append(attempts, metadata)
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	wantParams := []string{"content[1].image_url", "content[1].video_url", "content[1].audio_url"}
	if state.RetryCount != 2 || len(attempts) != 3 || len(state.FailedResourceParams) != len(wantParams) {
		t.Fatalf("state=%+v attempts=%d", state, len(attempts))
	}
	for i, wantParam := range wantParams {
		if attempts[i].ResourceParam != wantParam {
			t.Fatalf("attempt %d resource_param=%q, want %q", i+1, attempts[i].ResourceParam, wantParam)
		}
		if len(attempts[i].FailedResourceParams) != i+1 {
			t.Fatalf("attempt %d failed_resource_params=%v", i+1, attempts[i].FailedResourceParams)
		}
	}
}

func TestOriginalVideoURLInjectionAndTaskDTO(t *testing.T) {
	task := &model.Task{
		TaskID: "task_video_123",
		PrivateData: model.TaskPrivateData{
			ResultURL:    "https://upstream.example/video.mp4",
			OssResultURL: "https://oss.example/video.mp4",
		},
	}
	body, err := addOriginalVideoURL([]byte(`{"metadata":{"url":"https://oss.example/video.mp4"}}`), task)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"metadata":{"url":"https://oss.example/video.mp4"},"original_url":"https://upstream.example/video.mp4"}`
	if string(body) != want {
		t.Fatalf("body=%s\nwant=%s", body, want)
	}

	dtoTask := TaskModel2Dto(task)
	if dtoTask.ResultURL != "https://oss.example/video.mp4" || dtoTask.OriginalURL != "https://upstream.example/video.mp4" {
		t.Fatalf("task dto result=%q original=%q", dtoTask.ResultURL, dtoTask.OriginalURL)
	}
}
