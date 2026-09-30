package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
)

func TestPopulateXAITaskPrivateData(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	context.Set("xai_video_operation", "generate")
	expiresAfter := int64(3600)
	publicExpiresAfter := int64(600)
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Duration:    6,
		AspectRatio: "1:1",
		Resolution:  "480p",
		Output:      &relaycommon.TaskVideoOutput{UploadURL: " https://uploads.example.com/result "},
		StorageOptions: &relaycommon.TaskStorageOptions{
			Filename:     "result.mp4",
			ExpiresAfter: &expiresAfter,
			PublicURL:    &relaycommon.TaskPublicURLOptions{ExpiresAfter: &publicExpiresAfter},
		},
	})

	task := &model.Task{}
	populateXAITaskPrivateData(context, task)

	if task.PrivateData.XAI == nil {
		t.Fatal("xAI private data is nil")
	}
	xai := task.PrivateData.XAI
	if xai.Operation != "generate" || xai.Duration != 6 || xai.AspectRatio != "1:1" || xai.Resolution != "480p" {
		t.Fatalf("unexpected xAI metadata: %+v", xai)
	}
	if xai.OutputUploadURL != "https://uploads.example.com/result" {
		t.Fatalf("output upload URL = %q", xai.OutputUploadURL)
	}
	if xai.StorageOptions == nil || xai.StorageOptions.Filename != "result.mp4" || !xai.StorageOptions.CreatePublicURL {
		t.Fatalf("unexpected xAI storage metadata: %+v", xai.StorageOptions)
	}
}
