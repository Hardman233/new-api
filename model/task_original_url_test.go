package model

import "testing"

func TestTaskResultURLsKeepOSSAndOriginalSeparate(t *testing.T) {
	task := &Task{
		TaskID: "task_video_123",
		PrivateData: TaskPrivateData{
			ResultURL:    "https://upstream.example/video.mp4",
			OssResultURL: "https://oss.example/video.mp4",
		},
	}

	if got := task.GetResultURL(); got != "https://oss.example/video.mp4" {
		t.Fatalf("GetResultURL() = %q", got)
	}
	if got := task.GetOriginalResultURL(); got != "https://upstream.example/video.mp4" {
		t.Fatalf("GetOriginalResultURL() = %q", got)
	}
	video := task.ToOpenAIVideo()
	if video.OriginalURL != "https://upstream.example/video.mp4" {
		t.Fatalf("OpenAI original_url = %q", video.OriginalURL)
	}
}

func TestTaskOriginalResultURLLegacyFallback(t *testing.T) {
	task := &Task{FailReason: "https://legacy.example/video.mp4"}
	if got := task.GetOriginalResultURL(); got != task.FailReason {
		t.Fatalf("GetOriginalResultURL() = %q", got)
	}
}

func TestTaskOriginalResultURLOmitsFailureTextAndProxyURL(t *testing.T) {
	tests := []struct {
		name string
		task Task
	}{
		{
			name: "failure text",
			task: Task{FailReason: "resource download failed"},
		},
		{
			name: "internal proxy",
			task: Task{
				TaskID: "task_video_123",
				PrivateData: TaskPrivateData{
					ResultURL: "https://api.example/v1/videos/task_video_123/content",
				},
			},
		},
		{
			name: "data URI",
			task: Task{PrivateData: TaskPrivateData{ResultURL: "data:video/mp4;base64,AAAA"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.task.GetOriginalResultURL(); got != "" {
				t.Fatalf("GetOriginalResultURL() = %q, want empty", got)
			}
		})
	}
}
