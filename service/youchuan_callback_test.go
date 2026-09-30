package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func TestApplyYouchuanTaskResultMetadataPreservesAllOutputPositions(t *testing.T) {
	task := &model.Task{
		PrivateData: model.TaskPrivateData{Youchuan: &model.YouchuanTaskPrivateData{}},
	}
	result := &relaycommon.TaskInfo{Outputs: []relaycommon.TaskOutput{
		{Index: 0, RemoteURL: " https://example.com/0.png ", MediaKind: model.GeneratedMediaKindImage},
		{Index: 1, Audit: "blocked", MediaKind: model.GeneratedMediaKindImage},
		{Index: 2, RemoteURL: "https://example.com/2.mp4", MediaKind: model.GeneratedMediaKindVideo},
	}}
	body := []byte(`{"id":"upstream-1","seed":4294967295,"cost":{"jobId":"upstream-1","fastCost":1.25}}`)

	applyYouchuanTaskResultMetadata(task, result, body)

	require.Len(t, task.PrivateData.Youchuan.Outputs, 3)
	require.Equal(t, "https://example.com/0.png", task.PrivateData.Youchuan.Outputs[0].RemoteURL)
	require.Equal(t, "blocked", task.PrivateData.Youchuan.Outputs[1].Audit)
	require.Empty(t, task.PrivateData.Youchuan.Outputs[1].RemoteURL)
	require.Equal(t, model.GeneratedMediaKindVideo, task.PrivateData.Youchuan.Outputs[2].MediaKind)
	require.EqualValues(t, 4294967295, task.PrivateData.Youchuan.Seed)
	var cost struct {
		JobID string `json:"jobId"`
	}
	require.NoError(t, json.Unmarshal(task.PrivateData.Youchuan.RawCost, &cost))
	require.Equal(t, "upstream-1", cost.JobID)
}

func TestApplyYouchuanResponseMediaSizesPreservesURLShape(t *testing.T) {
	data := []byte(`{"id":"upstream-1","urls":["https://oss.example/0.png","","https://oss.example/2.mp4"]}`)
	outputs := []model.YouchuanTaskOutput{
		{Index: 0, SizeBytes: 12345},
		{Index: 1, Audit: "blocked"},
		{Index: 2, SizeBytes: 67890},
	}

	updated, err := model.ApplyYouchuanResponseMediaSizes(data, outputs)
	require.NoError(t, err)
	var response map[string]any
	require.NoError(t, json.Unmarshal(updated, &response))
	require.Equal(t, []any{"https://oss.example/0.png", "", "https://oss.example/2.mp4"}, response["urls"])
	require.Equal(t, []any{float64(12345), float64(0), float64(67890)}, response["size_bytes"])
}

func TestEnsureYouchuanResponseMediaSizesBackfillsCompletedTask(t *testing.T) {
	task := &model.Task{
		Data: json.RawMessage(`{"id":"upstream-legacy","status":2,"urls":["https://oss.example/0.png"]}`),
		PrivateData: model.TaskPrivateData{Youchuan: &model.YouchuanTaskPrivateData{
			Outputs: []model.YouchuanTaskOutput{{Index: 0, SizeBytes: 45678}},
		}},
	}

	require.NoError(t, ensureYouchuanResponseMediaSizes(task))
	var response map[string]any
	require.NoError(t, json.Unmarshal(task.Data, &response))
	require.Equal(t, []any{float64(45678)}, response["size_bytes"])
}

func TestSendYouchuanClientCallbackUsesStableIdempotencyHeaders(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	originalClient := httpClient
	t.Cleanup(func() {
		*fetchSetting = originalFetchSetting
		httpClient = originalClient
	})
	fetchSetting.EnableSSRFProtection = false

	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header.Clone()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	httpClient = server.Client()
	task := &model.Task{
		TaskID:         "public-task-1",
		UpstreamTaskID: "upstream-task-1",
		Data:           json.RawMessage(`{"id":"upstream-task-1","status":2}`),
	}

	err := sendYouchuanClientCallback(context.Background(), task, server.URL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP 503")
	require.Equal(t, task.TaskID, headers.Get("X-New-Api-Callback-Id"))
	require.Equal(t, task.TaskID, headers.Get("X-New-Api-Task-Id"))
	require.Equal(t, task.UpstreamTaskID, headers.Get("X-New-Api-Upstream-Task-Id"))
	require.Equal(t, task.TaskID, headers.Get("X-New-Api-Billing-Id"))
}

func TestSendYouchuanClientCallbackRejectsPrivateNetwork(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	t.Cleanup(func() { *fetchSetting = originalFetchSetting })
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = []string{"80", "443"}
	fetchSetting.ApplyIPFilterForDomain = true

	task := &model.Task{
		TaskID:   "public-task-2",
		Platform: constant.TaskPlatform("58"),
		Data:     json.RawMessage(`{"id":"upstream-task-2","status":2}`),
	}
	err := sendYouchuanClientCallback(context.Background(), task, "http://127.0.0.1/callback")
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "rejected") || strings.Contains(err.Error(), "private"), err.Error())
}
