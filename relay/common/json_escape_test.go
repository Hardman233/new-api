package common

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
)

func TestEscapeNonASCIIJSONPreservesDecodedChinese(t *testing.T) {
	raw := []byte(`{"messages":[{"content":"生成一张白色陶瓷杯"}]}`)

	escaped := EscapeNonASCIIJSON(raw)

	if strings.Contains(string(escaped), "生成") {
		t.Fatalf("escaped JSON still contains raw Chinese: %s", escaped)
	}
	if !strings.Contains(string(escaped), `\u751f\u6210`) {
		t.Fatalf("escaped JSON does not contain unicode escapes: %s", escaped)
	}

	var decoded struct {
		Messages []struct {
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(escaped, &decoded); err != nil {
		t.Fatalf("unmarshal escaped JSON: %v", err)
	}
	if got := decoded.Messages[0].Content; got != "生成一张白色陶瓷杯" {
		t.Fatalf("decoded content = %q", got)
	}
}

func TestShouldEscapeNonASCIIJSONForWangsuGeminiImage(t *testing.T) {
	info := &RelayInfo{
		OriginModelName: "gemini-3-pro-image-preview",
		ChannelMeta: &ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			ChannelBaseUrl:    "https://api.edgecloudapp.com/v2/gws/openapi",
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}

	if !ShouldEscapeNonASCIIJSONForUpstream(info) {
		t.Fatal("expected Wangsu Gemini image channel to escape non-ASCII JSON")
	}
}

func TestShouldEscapeNonASCIIJSONForGoogleGeminiImage(t *testing.T) {
	info := &RelayInfo{
		OriginModelName: "gemini-3-pro-image-preview",
		ChannelMeta: &ChannelMeta{
			ChannelType:       constant.ChannelTypeGemini,
			ChannelBaseUrl:    "https://generativelanguage.googleapis.com",
			UpstreamModelName: "gemini-3-pro-image-preview",
		},
	}

	if !ShouldEscapeNonASCIIJSONForUpstream(info) {
		t.Fatal("expected native Google Gemini image channel to escape non-ASCII JSON")
	}
}
