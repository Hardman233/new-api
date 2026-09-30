package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

func TestShouldUseSD2MaterialAsset(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{"https://cdn.example.com/a.png", true},
		{"http://cdn.example.com/a.png", true},
		{"asset://asset_123", false},
		{"data:image/png;base64,xxx", false},
		{"base64:xxx", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := ShouldUseSD2MaterialAsset(tt.raw); got != tt.want {
			t.Fatalf("ShouldUseSD2MaterialAsset(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestEnsureSD2MaterialAssetCreatesPollsAndCaches(t *testing.T) {
	truncate(t)

	createCalls := 0
	getCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			t.Fatalf("missing Authorization header")
		}
		if r.Header.Get("X-Date") == "" || r.Header.Get("X-Content-Sha256") == "" {
			t.Fatalf("missing signed headers")
		}
		switch r.URL.Query().Get("Action") {
		case "CreateAsset":
			createCalls++
			_, _ = w.Write([]byte(`{"Result":{"AssetId":"asset_test_1"}}`))
		case "GetAsset":
			getCalls++
			_, _ = w.Write([]byte(`{"Result":{"Asset":{"AssetId":"asset_test_1","Status":"Active"}}}`))
		default:
			t.Fatalf("unexpected action %q", r.URL.Query().Get("Action"))
		}
	}))
	defer server.Close()

	settings := dto.ChannelOtherSettings{
		SD2MaterialEnabled:         true,
		SD2MaterialAccessKeyID:     "ak",
		SD2MaterialSecretAccessKey: "sk",
		SD2MaterialAssetGroupID:    "group-1",
		SD2MaterialEndpoint:        server.URL,
		SD2MaterialPollTimeoutSec:  3,
		SD2MaterialPollIntervalSec: 1,
	}
	got, err := EnsureSD2MaterialAsset(context.Background(), 12, "https://cdn.example.com/a.png", "image", settings, "")
	if err != nil {
		t.Fatalf("EnsureSD2MaterialAsset() error = %v", err)
	}
	if got != "asset://asset_test_1" {
		t.Fatalf("asset url = %q", got)
	}
	if createCalls != 1 || getCalls != 1 {
		t.Fatalf("calls create/get = %d/%d, want 1/1", createCalls, getCalls)
	}

	cached, err := EnsureSD2MaterialAsset(context.Background(), 12, "https://cdn.example.com/a.png", "image", settings, "")
	if err != nil {
		t.Fatalf("cached EnsureSD2MaterialAsset() error = %v", err)
	}
	if cached != "asset://asset_test_1" {
		t.Fatalf("cached asset url = %q", cached)
	}
	if createCalls != 1 || getCalls != 1 {
		t.Fatalf("cached call should not hit server, create/get = %d/%d", createCalls, getCalls)
	}

	var row model.SD2MaterialAsset
	if err := model.DB.First(&row, "channel_id = ?", 12).Error; err != nil {
		t.Fatalf("material row not found: %v", err)
	}
	if row.Status != model.SD2MaterialStatusActive || row.AssetID != "asset_test_1" {
		t.Fatalf("row status/asset = %q/%q", row.Status, row.AssetID)
	}
}

func TestEnsureSD2MaterialAssetCreatesGroupWhenMissing(t *testing.T) {
	truncate(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("Action") {
		case "CreateAssetGroup":
			_, _ = w.Write([]byte(`{"Result":{"AssetGroupId":"group-created"}}`))
		case "CreateAsset":
			_, _ = w.Write([]byte(`{"Result":{"AssetId":"asset_test_2"}}`))
		case "GetAsset":
			_, _ = w.Write([]byte(`{"Result":{"Status":"Active"}}`))
		default:
			t.Fatalf("unexpected action %q", r.URL.Query().Get("Action"))
		}
	}))
	defer server.Close()

	settings := dto.ChannelOtherSettings{
		SD2MaterialEnabled:         true,
		SD2MaterialAccessKeyID:     "ak",
		SD2MaterialSecretAccessKey: "sk",
		SD2MaterialAssetGroupName:  "oc-test-group",
		SD2MaterialEndpoint:        server.URL,
		SD2MaterialPollTimeoutSec:  3,
		SD2MaterialPollIntervalSec: 1,
	}
	got, err := EnsureSD2MaterialAsset(context.Background(), 13, "https://cdn.example.com/b.png", "image", settings, "")
	if err != nil {
		t.Fatalf("EnsureSD2MaterialAsset() error = %v", err)
	}
	if got != "asset://asset_test_2" {
		t.Fatalf("asset url = %q", got)
	}

	var group model.SD2MaterialGroup
	if err := model.DB.First(&group, "channel_id = ?", 13).Error; err != nil {
		t.Fatalf("material group not found: %v", err)
	}
	if group.AssetGroupID != "group-created" || group.Status != model.SD2MaterialGroupStatusReady {
		t.Fatalf("group id/status = %q/%q", group.AssetGroupID, group.Status)
	}
}

func TestEnsureSD2MaterialAssetUsesAudioAssetType(t *testing.T) {
	truncate(t)

	var createBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("Action") {
		case "CreateAsset":
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Fatalf("decode CreateAsset body: %v", err)
			}
			_, _ = w.Write([]byte(`{"Result":{"AssetId":"asset_audio_1"}}`))
		case "GetAsset":
			_, _ = w.Write([]byte(`{"Result":{"Asset":{"AssetId":"asset_audio_1","Status":"Active"}}}`))
		default:
			t.Fatalf("unexpected action %q", r.URL.Query().Get("Action"))
		}
	}))
	defer server.Close()

	settings := dto.ChannelOtherSettings{
		SD2MaterialEnabled:         true,
		SD2MaterialAccessKeyID:     "ak",
		SD2MaterialSecretAccessKey: "sk",
		SD2MaterialAssetGroupID:    "group-audio",
		SD2MaterialEndpoint:        server.URL,
		SD2MaterialPollTimeoutSec:  3,
		SD2MaterialPollIntervalSec: 1,
	}
	got, err := EnsureSD2MaterialAsset(context.Background(), 14, "https://cdn.example.com/ref.mp3", "audio", settings, "")
	if err != nil {
		t.Fatalf("EnsureSD2MaterialAsset() error = %v", err)
	}
	if got != "asset://asset_audio_1" {
		t.Fatalf("asset url = %q", got)
	}
	if createBody["AssetType"] != "Audio" {
		t.Fatalf("CreateAsset AssetType = %#v, want Audio; body=%v", createBody["AssetType"], createBody)
	}
	if createBody["Url"] != "https://cdn.example.com/ref.mp3" {
		t.Fatalf("CreateAsset Url = %#v", createBody["Url"])
	}
	if createBody["AssetGroupId"] != "group-audio" {
		t.Fatalf("SD2.0 CreateAsset AssetGroupId = %#v", createBody["AssetGroupId"])
	}
	if _, exists := createBody["GroupId"]; exists {
		t.Fatalf("SD2.0 CreateAsset unexpectedly used GroupId: %v", createBody)
	}
}

func TestSD2MaterialProjectNameIsIncludedInAllDefaultBodies(t *testing.T) {
	client := &sd2MaterialClient{
		assetGroupID:   "group-sd25",
		assetGroupName: "sd25-assets",
		projectName:    "0807Oc",
		imageAssetType: "Image",
	}

	tests := []struct {
		name  string
		build func() ([]byte, error)
	}{
		{
			name: "create asset",
			build: func() ([]byte, error) {
				return client.buildCreateBody("https://cdn.example.com/sd25.png", "image")
			},
		},
		{name: "create asset group", build: client.buildCreateGroupBody},
		{
			name: "get asset",
			build: func() ([]byte, error) {
				return client.buildGetBody("asset-sd25")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := tt.build()
			if err != nil {
				t.Fatalf("build body: %v", err)
			}
			var payload map[string]interface{}
			if err := json.Unmarshal(body, &payload); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if payload["ProjectName"] != "0807Oc" {
				t.Fatalf("ProjectName = %#v, body=%s", payload["ProjectName"], body)
			}
			switch tt.name {
			case "create asset":
				if payload["URL"] != "https://cdn.example.com/sd25.png" {
					t.Fatalf("SD2.5 CreateAsset URL = %#v, body=%s", payload["URL"], body)
				}
				if _, exists := payload["Url"]; exists {
					t.Fatalf("SD2.5 CreateAsset used legacy Url: %s", body)
				}
				if payload["GroupId"] != "group-sd25" {
					t.Fatalf("SD2.5 CreateAsset GroupId = %#v, body=%s", payload["GroupId"], body)
				}
				if _, exists := payload["AssetGroupId"]; exists {
					t.Fatalf("SD2.5 CreateAsset used legacy AssetGroupId: %s", body)
				}
			case "get asset":
				if payload["Id"] != "asset-sd25" {
					t.Fatalf("SD2.5 GetAsset Id = %#v, body=%s", payload["Id"], body)
				}
				if _, exists := payload["AssetId"]; exists {
					t.Fatalf("SD2.5 GetAsset used legacy AssetId: %s", body)
				}
			}
		})
	}
}

func TestSD2MaterialProjectNameIsOmittedWhenUnconfigured(t *testing.T) {
	client := &sd2MaterialClient{
		assetGroupID:   "group-sd20",
		assetGroupName: "sd20-assets",
		imageAssetType: "Image",
	}
	body, err := client.buildCreateBody("https://cdn.example.com/sd20.png", "image")
	if err != nil {
		t.Fatalf("build body: %v", err)
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, exists := payload["ProjectName"]; exists {
		t.Fatalf("unconfigured ProjectName leaked into SD2.0 body: %s", body)
	}
	if payload["AssetGroupId"] != "group-sd20" {
		t.Fatalf("SD2.0 CreateAsset AssetGroupId = %#v, body=%s", payload["AssetGroupId"], body)
	}
	if _, exists := payload["GroupId"]; exists {
		t.Fatalf("unconfigured SD2.0 body used GroupId: %s", body)
	}
}
