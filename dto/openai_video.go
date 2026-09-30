package dto

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	VideoStatusUnknown    = "unknown"
	VideoStatusQueued     = "queued"
	VideoStatusInProgress = "in_progress"
	VideoStatusCompleted  = "completed"
	VideoStatusFailed     = "failed"
)

type OpenAIVideo struct {
	ID                 string            `json:"id"`
	TaskID             string            `json:"task_id,omitempty"`
	Object             string            `json:"object"`
	BillingID          string            `json:"billing_id,omitempty"`
	Model              string            `json:"model"`
	Status             string            `json:"status"`
	Progress           int               `json:"progress"`
	CreatedAt          int64             `json:"created_at"`
	CompletedAt        int64             `json:"completed_at,omitempty"`
	ExpiresAt          int64             `json:"expires_at,omitempty"`
	Seconds            string            `json:"seconds,omitempty"`
	Size               string            `json:"size,omitempty"`
	RemixedFromVideoID string            `json:"remixed_from_video_id,omitempty"`
	OriginalURL        string            `json:"original_url,omitempty"`
	Error              *OpenAIVideoError `json:"error,omitempty"`
	Usage              map[string]any    `json:"usage,omitempty"`
	Metadata           map[string]any    `json:"metadata,omitempty"`
}

func (m OpenAIVideo) MarshalJSON() ([]byte, error) {
	type alias OpenAIVideo
	out := alias(m)
	if out.BillingID == "" {
		out.BillingID = out.ID
	}
	return json.Marshal(out)
}

func (m *OpenAIVideo) SetProgressStr(progress string) {
	progress = strings.TrimSuffix(progress, "%")
	m.Progress, _ = strconv.Atoi(progress)
}

func (m *OpenAIVideo) SetMetadata(k string, v any) {
	if m.Metadata == nil {
		m.Metadata = make(map[string]any)
	}
	m.Metadata[k] = v
}

func NewOpenAIVideo() *OpenAIVideo {
	return &OpenAIVideo{
		Object: "video",
		Status: VideoStatusQueued,
	}
}

type OpenAIVideoError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
}
