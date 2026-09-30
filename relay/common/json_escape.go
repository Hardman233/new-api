package common

import (
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/constant"
)

// ShouldEscapeNonASCIIJSONForUpstream returns true for OpenAI-compatible
// Gemini image gateways that are known to corrupt raw UTF-8 prompt text.
func ShouldEscapeNonASCIIJSONForUpstream(info *RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	if info.ChannelType != constant.ChannelTypeOpenAI &&
		info.ChannelType != constant.ChannelTypeCustom &&
		info.ChannelType != constant.ChannelTypeGemini {
		return false
	}

	models := []string{info.OriginModelName, info.UpstreamModelName}
	isGeminiImage := false
	for _, model := range models {
		model = strings.ToLower(strings.TrimSpace(model))
		model = strings.TrimPrefix(model, "gemini.")
		if strings.HasPrefix(model, "gemini-") && strings.Contains(model, "image") {
			isGeminiImage = true
			break
		}
	}
	if !isGeminiImage {
		return false
	}
	return true
}

// EscapeNonASCIIJSON converts UTF-8 JSON bytes to ASCII-only JSON bytes by
// escaping non-ASCII runes as \uXXXX. JSON parsers recover the original text.
func EscapeNonASCIIJSON(data []byte) []byte {
	if len(data) == 0 || !containsNonASCII(data) {
		return data
	}

	var b strings.Builder
	b.Grow(len(data) + len(data)/8)
	for len(data) > 0 {
		r, size := utf8.DecodeRune(data)
		if r == utf8.RuneError && size == 1 {
			b.WriteByte(data[0])
			data = data[1:]
			continue
		}
		if r < utf8.RuneSelf {
			b.WriteByte(byte(r))
		} else if r <= 0xffff {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&b, "\\u%04x\\u%04x", hi, lo)
		}
		data = data[size:]
	}
	return []byte(b.String())
}

func containsNonASCII(data []byte) bool {
	for _, ch := range data {
		if ch >= utf8.RuneSelf {
			return true
		}
	}
	return false
}
