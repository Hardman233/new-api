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

func TestOpenWhiteForceChannelIDs(t *testing.T) {
	t.Setenv("OPEN_WHITE_FORCE_CHANNEL_IDS", "11, 7;bad 11\n0\t6")

	got := openWhiteForceChannelIDs()
	want := []int{11, 7, 6}
	if len(got) != len(want) {
		t.Fatalf("ids len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ids[%d] = %d, want %d: %#v", i, got[i], want[i], got)
		}
	}
}

func TestOpenWhiteRouteForForcedChannelDefaultsToOpenWhite(t *testing.T) {
	channel := &model.Channel{Id: 11}
	if got := openWhiteRouteForChannel(channel); got != "open_white" {
		t.Fatalf("route = %q, want open_white", got)
	}
}

func TestFilterForcedOpenWhiteChannelsUsesChannelModelList(t *testing.T) {
	sd20 := &model.Channel{Id: 6, Models: "doubao-seedance-2-0-260128,doubao-seedance-2-0-fast-260128"}
	sd20.SetOtherSettings(dto.ChannelOtherSettings{SeedanceRouteTags: []string{"xiongda_open_white"}})
	sd25 := &model.Channel{Id: 17, Models: "doubao-seedance-2-5-260628"}
	sd25.SetOtherSettings(dto.ChannelOtherSettings{SeedanceRouteTags: []string{"zhuque"}})

	got, err := filterSeedanceOpenWhiteChannelsByModelAndRoute(
		[]*model.Channel{sd20, sd25},
		"doubao-seedance-2-5-260628",
		"all",
	)
	if err != nil {
		t.Fatalf("filter forced channels: %v", err)
	}
	if len(got) != 1 || got[0].Id != 17 {
		t.Fatalf("channels = %#v, want only channel 17", got)
	}
}

func TestSeedanceOpenWhiteChannelSupportsModelTrimsAndMatchesCase(t *testing.T) {
	channel := &model.Channel{Models: " first-model, DOUBAO-SEEDANCE-2-5-260628 "}
	if !seedanceOpenWhiteChannelSupportsModel(channel, "doubao-seedance-2-5-260628") {
		t.Fatal("channel model list should match case-insensitively after trimming")
	}
	if seedanceOpenWhiteChannelSupportsModel(channel, "doubao-seedance-2-0-260128") {
		t.Fatal("channel must not match a model absent from its model list")
	}
}

func TestNoSeedanceOpenWhiteChannelResultIsExplicitFailure(t *testing.T) {
	result := noSeedanceOpenWhiteChannelResult()
	if result.Status != model.SeedanceOpenWhiteStatusFailed {
		t.Fatalf("status = %q, want failed", result.Status)
	}
	if result.ErrorCode != "no_open_white_channel" {
		t.Fatalf("error code = %q, want no_open_white_channel", result.ErrorCode)
	}
}

func TestSummarizeOpenWhiteBatchIgnoresSkippedPeer(t *testing.T) {
	results := []SeedanceOpenWhiteBatchResult{
		{Status: model.SeedanceOpenWhiteStatusActive},
		{Status: seedanceOpenWhiteStatusSkipped},
	}
	if got := summarizeOpenWhiteBatchStatus(results); got != model.SeedanceOpenWhiteStatusActive {
		t.Fatalf("status = %q, want active", got)
	}
}

func TestCallSeedanceOpenWhitePeerIgnoresSkippedResponse(t *testing.T) {
	t.Setenv("OPEN_WHITE_INTERNAL_KEY", "test-internal-key")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/seedance/open_white/create_asset" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("X-Internal-Api-Key") != "test-internal-key" {
			t.Fatal("internal API key header is missing")
		}
		var req SeedanceOpenWhiteCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode peer request: %v", err)
		}
		if !req.SkipIfNoLocalChannel {
			t.Fatal("peer request must enable skip_if_no_local_channel")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"ow_test","object":"seedance.open_white","status":"skipped","assets":[]}`))
	}))
	defer server.Close()

	got := callSeedanceOpenWhitePeer(context.Background(), seedanceOpenWhitePeer{
		NodeID:  "overseas",
		BaseURL: server.URL,
		Region:  "overseas",
	}, SeedanceOpenWhiteCreateRequest{Model: "doubao-seedance-2-5-260628"})
	if len(got) != 0 {
		t.Fatalf("results = %#v, want no synthetic failure asset", got)
	}
}

func TestSeedanceOpenWhiteModelDefaultsAndAllowlist(t *testing.T) {
	if got := NormalizeSeedanceOpenWhiteModel(""); got != DefaultSeedanceOpenWhiteModel() {
		t.Fatalf("default model = %q, want %q", got, DefaultSeedanceOpenWhiteModel())
	}
	for _, modelName := range SeedanceOpenWhiteAllowedModels() {
		if !IsSeedanceOpenWhiteModelAllowed(modelName) {
			t.Fatalf("model %q should be allowed", modelName)
		}
	}
	if IsSeedanceOpenWhiteModelAllowed("gpt-image-2") {
		t.Fatal("gpt-image-2 should not be allowed for Seedance open-white")
	}
}

func TestSeedanceOpenWhiteKeepsSD20DefaultWhenSD25IsRequested(t *testing.T) {
	if got := NormalizeSeedanceOpenWhiteModel("doubao-seedance-2-5-260628"); got != "doubao-seedance-2-5-260628" {
		t.Fatalf("normalized model = %q", got)
	}
	if got := NormalizeSeedanceOpenWhiteModel(""); got != "doubao-seedance-2-0-260128" {
		t.Fatalf("empty model default = %q, want SD2.0", got)
	}
}

func TestNormalizeSeedanceOpenWhiteModelsCanonicalizesAndDeduplicates(t *testing.T) {
	got, err := normalizeSeedanceOpenWhiteModels([]string{
		" doubao-seedance-2-0-260128 ",
		"DOUBAO-SEEDANCE-2-5-260628",
		"doubao-seedance-2-0-260128",
	})
	if err != nil {
		t.Fatalf("normalize models: %v", err)
	}
	want := []string{"doubao-seedance-2-0-260128", "doubao-seedance-2-5-260628"}
	if len(got) != len(want) {
		t.Fatalf("models = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("models[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestNormalizeSeedanceOpenWhiteModelsRejectsInvalidInput(t *testing.T) {
	for _, models := range [][]string{nil, {""}, {"gpt-image-2"}} {
		if _, err := normalizeSeedanceOpenWhiteModels(models); err == nil {
			t.Fatalf("models %#v should fail", models)
		}
	}
}

func TestSummarizeOpenWhiteBatchStatus(t *testing.T) {
	tests := []struct {
		name    string
		results []SeedanceOpenWhiteBatchResult
		want    string
	}{
		{name: "all active", results: []SeedanceOpenWhiteBatchResult{{Status: model.SeedanceOpenWhiteStatusActive}, {Status: model.SeedanceOpenWhiteStatusActive}}, want: model.SeedanceOpenWhiteStatusActive},
		{name: "mixed", results: []SeedanceOpenWhiteBatchResult{{Status: model.SeedanceOpenWhiteStatusActive}, {Status: model.SeedanceOpenWhiteStatusFailed}}, want: model.SeedanceOpenWhiteStatusPartialFailed},
		{name: "all failed", results: []SeedanceOpenWhiteBatchResult{{Status: model.SeedanceOpenWhiteStatusFailed}, {Status: model.SeedanceOpenWhiteStatusTimeout}}, want: model.SeedanceOpenWhiteStatusFailed},
		{name: "pending", results: []SeedanceOpenWhiteBatchResult{{Status: model.SeedanceOpenWhiteStatusPending}}, want: model.SeedanceOpenWhiteStatusPending},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := summarizeOpenWhiteBatchStatus(tt.results); got != tt.want {
				t.Fatalf("status = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestOpenWhiteChannelSupportsAssetAllowsZhuqueAudio(t *testing.T) {
	channel := &model.Channel{Id: 7, Name: "朱雀SD2"}
	channel.SetOtherSettings(dto.ChannelOtherSettings{SeedanceRouteTags: []string{"zhuque"}})
	record := model.SeedanceOpenWhiteRecord{MediaKind: "audio", AssetType: "Audio"}

	ok, code, message := openWhiteChannelSupportsAsset(record, channel, "zhuque")
	if !ok {
		t.Fatalf("zhuque audio should be allowed, code=%q message=%q", code, message)
	}
}

func TestOpenWhiteChannelSupportsAssetAllowsZhuqueImage(t *testing.T) {
	channel := &model.Channel{Id: 7, Name: "朱雀SD2"}
	channel.SetOtherSettings(dto.ChannelOtherSettings{SeedanceRouteTags: []string{"zhuque"}})
	record := model.SeedanceOpenWhiteRecord{MediaKind: "image", AssetType: "Image"}

	ok, code, message := openWhiteChannelSupportsAsset(record, channel, "zhuque")
	if !ok {
		t.Fatalf("zhuque image should be allowed, code=%q message=%q", code, message)
	}
}
