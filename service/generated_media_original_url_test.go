package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestOriginalURLIsNotAGeneratedVideoRewriteField(t *testing.T) {
	if isGeneratedVideoURLField("original_url") {
		t.Fatal("original_url must preserve the upstream URL")
	}
	for _, field := range []string{"url", "result_url", "video_url", "file_url", "download_url", "remote_url"} {
		if !isGeneratedVideoURLField(field) {
			t.Fatalf("%s should remain an OSS rewrite field", field)
		}
	}
}

func TestResolveGeneratedMediaContentTypeRejectsHTMLForVideo(t *testing.T) {
	_, err := resolveGeneratedMediaContentType(
		model.GeneratedMediaKindVideo,
		"text/html",
		"text/html",
	)
	if err == nil || !strings.Contains(err.Error(), "unexpected content type") {
		t.Fatalf("HTML video response error = %v", err)
	}
}

func TestResolveGeneratedMediaContentTypeAcceptsVideoFallbacks(t *testing.T) {
	tests := []struct {
		name     string
		header   string
		detected string
		want     string
	}{
		{name: "explicit video", header: "video/mp4", detected: "application/octet-stream", want: "video/mp4"},
		{name: "detected video", header: "", detected: "video/mp4", want: "video/mp4"},
		{name: "generic binary", header: "application/octet-stream", detected: "application/octet-stream", want: "video/mp4"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := resolveGeneratedMediaContentType(model.GeneratedMediaKindVideo, test.header, test.detected)
			if err != nil {
				t.Fatalf("resolve content type error = %v", err)
			}
			if got != test.want {
				t.Fatalf("content type = %q, want %q", got, test.want)
			}
		})
	}
}

func TestChooseTaskGeneratedMediaURLSkipsClientUploadTarget(t *testing.T) {
	uploadURL := "https://uploads.example.com/signed-target"
	task := &model.Task{
		TaskID: "task_output",
		PrivateData: model.TaskPrivateData{
			ResultURL: uploadURL,
			XAI:       &model.XAITaskPrivateData{OutputUploadURL: uploadURL},
		},
		Data: []byte(`{"video":{"url":"https://uploads.example.com/signed-target"}}`),
	}
	result := &relaycommon.TaskInfo{Url: uploadURL, RemoteUrl: uploadURL}
	if got := chooseTaskGeneratedMediaURL(task, result); got != "" {
		t.Fatalf("client upload target must not be re-ingested, got %q", got)
	}

	result.Url = "https://downloads.example.com/result.mp4"
	if got := chooseTaskGeneratedMediaURL(task, result); got != result.Url {
		t.Fatalf("separate download URL = %q, want %q", got, result.Url)
	}
}

func TestGeneratedMediaTaskOutputsKeepsLegacyTasksOnSingleResultPath(t *testing.T) {
	task := &model.Task{Platform: "54"}
	result := &relaycommon.TaskInfo{
		Url: "https://example.com/legacy.mp4",
		Outputs: []relaycommon.TaskOutput{
			{Index: 0, RemoteURL: "https://example.com/first.mp4"},
			{Index: 1, RemoteURL: "https://example.com/second.mp4"},
		},
	}

	outputs := generatedMediaTaskOutputs(task, result)
	if len(outputs) != 1 || outputs[0].RemoteURL != result.Url {
		t.Fatalf("legacy outputs = %#v, want one existing result URL", outputs)
	}
}

func TestGeneratedMediaTaskOutputsKeepsAllYouchuanResults(t *testing.T) {
	task := &model.Task{Platform: "58", PrivateData: model.TaskPrivateData{Youchuan: &model.YouchuanTaskPrivateData{}}}
	result := &relaycommon.TaskInfo{Outputs: []relaycommon.TaskOutput{
		{Index: 0, RemoteURL: "https://example.com/first.png", MediaKind: model.GeneratedMediaKindImage},
		{Index: 1, RemoteURL: "https://example.com/second.png", MediaKind: model.GeneratedMediaKindImage},
	}}

	outputs := generatedMediaTaskOutputs(task, result)
	if len(outputs) != 2 {
		t.Fatalf("Youchuan outputs = %#v, want both results", outputs)
	}
}

func TestRewriteVideoURLStringSkipsClientUploadTarget(t *testing.T) {
	uploadURL := "https://uploads.example.com/signed-target"
	got, sizeBytes, err := rewriteVideoURLStringToOSS(
		t.Context(),
		GeneratedMediaUploadParams{ClientOutputUploadURL: uploadURL},
		uploadURL,
		"$.video.url",
	)
	if err != nil {
		t.Fatalf("rewrite upload target error = %v", err)
	}
	if got != uploadURL || sizeBytes != 0 {
		t.Fatalf("rewrite upload target = %q, size=%d", got, sizeBytes)
	}
}
