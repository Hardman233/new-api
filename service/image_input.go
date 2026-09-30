package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/oss"
)

// UploadImageInputBytes stores an input image at a public OSS URL for upstreams
// that accept JSON URLs but reject multipart and inline Base64 inputs.
func UploadImageInputBytes(ctx context.Context, data []byte, contentType string) (string, error) {
	if len(data) == 0 {
		return "", errors.New("image input is empty")
	}
	contentType = normalizeContentType(contentType)
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = normalizeContentType(http.DetectContentType(data))
	}
	if !strings.HasPrefix(contentType, "image/") {
		return "", fmt.Errorf("unsupported image input content type: %s", contentType)
	}

	uploader, err := oss.GetUploader()
	if err != nil {
		return "", err
	}
	key := buildImageInputOSSKey(contentType, fmt.Sprintf("%x", sha256.Sum256(data)))
	publicURL := generatedMediaPublicURL(key)
	if publicURL == "" {
		return "", errors.New("image input OSS public URL is not configured")
	}
	if err = uploader.Put(ctx, key, data, contentType); err != nil {
		return "", err
	}
	return publicURL, nil
}

func buildImageInputOSSKey(contentType, shaHex string) string {
	prefix := strings.Trim(common.GetEnvOrDefaultString("GENERATED_MEDIA_INPUT_OSS_KEY_PREFIX", ""), "/")
	if prefix == "" {
		prefix = strings.Trim(common.GetEnvOrDefaultString("GENERATED_MEDIA_OSS_KEY_PREFIX", ""), "/")
		if prefix != "" {
			prefix += "/inputs"
		}
	}
	if prefix == "" {
		prefix = strings.Trim(common.AuditOSSKeyPrefix, "/")
		if prefix != "" {
			prefix += "/generated-media/inputs"
		} else {
			prefix = "generated-media/inputs"
		}
	}
	now := time.Now()
	return fmt.Sprintf("%s/%04d/%02d/%02d/image/%s%s",
		prefix, now.Year(), now.Month(), now.Day(), shaHex, extensionForContentType(contentType, ""))
}
