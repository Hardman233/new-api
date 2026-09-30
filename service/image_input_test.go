package service

import (
	"context"
	"strings"
	"testing"
)

func TestBuildImageInputOSSKeyUsesDedicatedInputPrefix(t *testing.T) {
	t.Setenv("GENERATED_MEDIA_INPUT_OSS_KEY_PREFIX", "seedream-inputs")
	key := buildImageInputOSSKey("image/png", strings.Repeat("a", 64))
	if !strings.HasPrefix(key, "seedream-inputs/") {
		t.Fatalf("key = %q", key)
	}
	if !strings.HasSuffix(key, "/image/"+strings.Repeat("a", 64)+".png") {
		t.Fatalf("key = %q", key)
	}
}

func TestUploadImageInputBytesRejectsNonImageBeforeOSS(t *testing.T) {
	_, err := UploadImageInputBytes(context.Background(), []byte("plain text"), "text/plain")
	if err == nil || !strings.Contains(err.Error(), "unsupported image input content type") {
		t.Fatalf("error = %v", err)
	}
}
