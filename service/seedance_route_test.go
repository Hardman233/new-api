package service

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

func TestClassifySeedanceRouteTextToVideo(t *testing.T) {
	route := ClassifySeedanceRoute(relaycommon.TaskSubmitReq{
		Model:  "doubao-seedance-2-0-260128",
		Prompt: "make a product video",
	})

	requireSeedanceTags(t, route.RequiredTags, "text_to_video")
	if route.Route != "text_to_video" {
		t.Fatalf("route = %q, want text_to_video", route.Route)
	}
}

func TestClassifySeedanceRouteSingleImageAsFirstFrame(t *testing.T) {
	route := ClassifySeedanceRoute(relaycommon.TaskSubmitReq{
		Model:  "doubao-seedance-2-0-260128",
		Prompt: "animate it",
		Images: []string{"https://example.com/a.png"},
	})

	requireSeedanceTags(t, route.RequiredTags, "first_frame")
	if route.Route != "first_frame" {
		t.Fatalf("route = %q, want first_frame", route.Route)
	}
}

func TestClassifySeedanceRouteFirstLastAndAdvanced(t *testing.T) {
	route := ClassifySeedanceRoute(relaycommon.TaskSubmitReq{
		Model: "doubao-seedance-2-0-260128",
		Content: []map[string]interface{}{
			{"type": "text", "text": "transition naturally"},
			{"type": "image_url", "role": "first_frame", "image_url": map[string]interface{}{"url": "https://example.com/a.png"}},
			{"type": "image_url", "role": "last_frame", "image_url": map[string]interface{}{"url": "https://example.com/b.png"}},
		},
		Resolution:    "1080p",
		GenerateAudio: true,
	})

	requireSeedanceTags(t, route.RequiredTags, "first_last_frame", "1080p", "generate_audio")
	if route.Route != "first_last_frame" {
		t.Fatalf("route = %q, want first_last_frame", route.Route)
	}
}

func TestClassifySeedanceRouteReferencesFromMetadata(t *testing.T) {
	route := ClassifySeedanceRoute(relaycommon.TaskSubmitReq{
		Model:  "doubao-seedance-2-0-260128",
		Prompt: "use references",
		Metadata: map[string]interface{}{
			"content": []interface{}{
				map[string]interface{}{"type": "image_url", "role": "reference_image", "image_url": map[string]interface{}{"url": "https://example.com/a.png"}},
				map[string]interface{}{"type": "video_url", "role": "reference_video", "video_url": map[string]interface{}{"url": "https://example.com/a.mp4"}},
				map[string]interface{}{"type": "audio_url", "role": "reference_audio", "audio_url": map[string]interface{}{"url": "https://example.com/a.mp3"}},
			},
			"return_last_frame": true,
		},
	})

	requireSeedanceTags(t, route.RequiredTags, "reference_image", "reference_video", "reference_audio", "return_last_frame")
	if route.Route != "reference_audio" {
		t.Fatalf("route = %q, want reference_audio", route.Route)
	}
}

func TestSeedanceChannelSupportsTags(t *testing.T) {
	required := []string{"first_last_frame", "1080p", "generate_audio"}
	if !seedanceChannelSupportsTags([]string{"advanced", "generate_audio"}, required) {
		t.Fatal("advanced channel tags should satisfy first_last_frame + 1080p + generate_audio")
	}
	if seedanceChannelSupportsTags([]string{"basic"}, required) {
		t.Fatal("basic channel tags should not satisfy advanced route")
	}
	if !seedanceChannelSupportsTags([]string{"open_white"}, required) {
		t.Fatal("open_white should satisfy all route tags")
	}
}

func TestClassifySeedanceRouteExplicitFieldOverridesAutoSignals(t *testing.T) {
	route := ClassifySeedanceRoute(relaycommon.TaskSubmitReq{
		Model:         "doubao-seedance-2-0-260128",
		Prompt:        "use the named upstream route",
		SeedanceRoute: "zhuque",
		Resolution:    "1080p",
		GenerateAudio: true,
	})

	requireSeedanceTags(t, route.RequiredTags, "zhuque")
	if len(route.RequiredTags) != 1 {
		t.Fatalf("tags = %#v, want only explicit zhuque route", route.RequiredTags)
	}
	if route.Route != "zhuque" {
		t.Fatalf("route = %q, want zhuque", route.Route)
	}
	if route.Signals["route_source"] != "request" {
		t.Fatalf("route_source = %q, want request", route.Signals["route_source"])
	}
}

func TestClassifySeedanceRouteExplicitAliases(t *testing.T) {
	route := ClassifySeedanceRoute(relaycommon.TaskSubmitReq{
		Model:         "doubao-seedance-2-0-260128",
		SeedanceRoute: "\u6731\u96c0",
	})
	requireSeedanceTags(t, route.RequiredTags, "zhuque")
	if route.Route != "zhuque" {
		t.Fatalf("route = %q, want zhuque", route.Route)
	}
	if !seedanceRouteIsExplicit(route) {
		t.Fatal("explicit route should be marked strict")
	}

	openWhite := ClassifySeedanceRoute(relaycommon.TaskSubmitReq{
		Model:         "doubao-seedance-2-0-260128",
		SeedanceRoute: "\u5f00\u767d",
	})
	requireSeedanceTags(t, openWhite.RequiredTags, "open_white")
	if openWhite.Route != "open_white" {
		t.Fatalf("route = %q, want open_white", openWhite.Route)
	}
	if !seedanceChannelSupportsTags([]string{"open_white"}, route.RequiredTags) {
		t.Fatal("open_white channel tags should satisfy zhuque explicit route")
	}
}

func requireSeedanceTags(t *testing.T, got []string, want ...string) {
	t.Helper()
	set := map[string]struct{}{}
	for _, tag := range got {
		set[tag] = struct{}{}
	}
	for _, tag := range want {
		if _, ok := set[tag]; !ok {
			t.Fatalf("tags = %#v, missing %q", got, tag)
		}
	}
}
