package model_setting

import "testing"

func TestGeminiStableAndPreviewImageModelsAreDistinctByDefault(t *testing.T) {
	oldSettings := geminiSettings
	geminiSettings = defaultGeminiSettings
	t.Cleanup(func() {
		geminiSettings = oldSettings
	})

	if got := ResolveGeminiModelAlias("gemini-3.1-flash-lite-image"); got != "gemini-3.1-flash-lite-image" {
		t.Fatalf("stable lite model should not be aliased by default, got %q", got)
	}
	if got := ResolveGeminiModelAlias("gemini-3.1-flash-lite-image-preview"); got != "gemini-3.1-flash-lite-image-preview" {
		t.Fatalf("preview lite model should stay preview, got %q", got)
	}
	if !IsGeminiModelSupportImagine("gemini-3.1-flash-lite-image") {
		t.Fatal("stable lite model should support image generation")
	}
	if !IsGeminiModelSupportImagine("gemini-3.1-flash-lite-image-preview") {
		t.Fatal("preview lite model should support image generation")
	}
	if got := GetGeminiImageReferenceLimit("gemini-3.1-flash-lite-image-preview"); got != 1 {
		t.Fatalf("preview lite model should share stable capability limits, got %d", got)
	}
}

func TestGeminiProPreviewIsTextOutputModelByDefault(t *testing.T) {
	oldSettings := geminiSettings
	geminiSettings = defaultGeminiSettings
	t.Cleanup(func() {
		geminiSettings = oldSettings
	})

	if IsGeminiModelSupportImagine("gemini-3.1-pro-preview") {
		t.Fatal("gemini-3.1-pro-preview should not be treated as an image output model")
	}
	if !IsGeminiModelSupportImagine("gemini-3-pro-image-preview") {
		t.Fatal("gemini-3-pro-image-preview should support image generation")
	}
}
