package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/oss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractGeneratedMediaBase64FromB64JSON(t *testing.T) {
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	}
	body := []byte(`{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(png) + `"}]}`)

	got := ExtractGeneratedMediaBase64(body)

	require.Len(t, got, 1)
	assert.Equal(t, model.GeneratedMediaKindImage, got[0].MediaKind)
	assert.Equal(t, "image/png", got[0].ContentType)
	assert.Equal(t, png, got[0].Data)
	assert.Equal(t, "$.data[0].b64_json", got[0].FieldPath)
}

func TestExtractGeneratedMediaBase64FromDataURI(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46, 0x00, 0x01}
	body := []byte(`{"output":"data:image/jpeg;base64,` + base64.StdEncoding.EncodeToString(jpeg) + `"}`)

	got := ExtractGeneratedMediaBase64(body)

	require.Len(t, got, 1)
	assert.Equal(t, model.GeneratedMediaKindImage, got[0].MediaKind)
	assert.Equal(t, "image/jpeg", got[0].ContentType)
	assert.Equal(t, jpeg, got[0].Data)
}

func TestExtractGeneratedMediaBase64IgnoresNonMediaField(t *testing.T) {
	body := []byte(`{"token":"` + base64.StdEncoding.EncodeToString([]byte("not media")) + `"}`)

	got := ExtractGeneratedMediaBase64(body)

	assert.Empty(t, got)
}

func TestRewriteGeneratedMediaDataURLsStringFromMarkdownImage(t *testing.T) {
	png := []byte{
		0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
		0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	}
	body := "好的\n![image](data:image/png;base64," + base64.StdEncoding.EncodeToString(png) + ")\n完成"

	got, changed, err := rewriteGeneratedMediaDataURLsString(body, "$.choices[0].message.content", func(candidate GeneratedMediaCandidate) (string, error) {
		assert.Equal(t, model.GeneratedMediaKindImage, candidate.MediaKind)
		assert.Equal(t, "image/png", candidate.ContentType)
		assert.Equal(t, png, candidate.Data)
		assert.Equal(t, "$.choices[0].message.content.data_url[0]", candidate.FieldPath)
		return "https://oss.example/generated-media/a.png", nil
	})

	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "好的\n![image](https://oss.example/generated-media/a.png)\n完成", got)
	assert.NotContains(t, got, "base64")
}

func TestRewriteGeneratedMediaDataURLsStringFromImageURLValue(t *testing.T) {
	jpeg := []byte{0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46, 0x00, 0x01}
	body := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(jpeg)

	got, changed, err := rewriteGeneratedMediaDataURLsString(body, "$.content[1].image_url.url", func(candidate GeneratedMediaCandidate) (string, error) {
		assert.Equal(t, model.GeneratedMediaKindImage, candidate.MediaKind)
		assert.Equal(t, "image/jpeg", candidate.ContentType)
		assert.Equal(t, jpeg, candidate.Data)
		assert.Equal(t, "$.content[1].image_url.url.data_url[0]", candidate.FieldPath)
		return "https://oss.example/generated-media/b.jpg", nil
	})

	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "https://oss.example/generated-media/b.jpg", got)
}

func TestPutGeneratedMediaFileWithRetryReopensFile(t *testing.T) {
	t.Setenv("GENERATED_MEDIA_UPLOAD_RETRY_COUNT", "1")
	t.Setenv("GENERATED_MEDIA_UPLOAD_RETRY_DELAY_MS", "0")

	body := []byte("image-bytes")
	path := t.TempDir() + "/image.png"
	require.NoError(t, os.WriteFile(path, body, 0o600))

	uploader := &retryGeneratedMediaUploader{failures: 1}
	err := putGeneratedMediaFileWithRetry(context.Background(), uploader, "image/key.png", path, "image/png")

	require.NoError(t, err)
	require.Equal(t, 2, uploader.attempts)
	require.Len(t, uploader.bodies, 2)
	assert.Equal(t, body, uploader.bodies[0])
	assert.Equal(t, body, uploader.bodies[1])
}

func TestNormalizeGeminiProImageResponseLargestImageFirst(t *testing.T) {
	body := []byte(`{
		"choices": [{
			"message": {
				"content": [
					{"type":"image_url","image_url":{"url":"https://oss.example/preview.jpg","size_bytes":500000}},
					{"type":"image_url","image_url":{"url":"https://oss.example/final.jpg","size_bytes":2500000}}
				]
			}
		}]
	}`)

	normalized, changed, err := NormalizeGeminiProImageResponse(body, "gemini-3-pro-image-preview")

	require.NoError(t, err)
	require.True(t, changed)
	assert.Contains(t, string(normalized), `"url":"https://oss.example/final.jpg"`)
	assert.Regexp(t, `"content":\[\{"image_url":\{"size_bytes":2500000,"url":"https://oss\.example/final\.jpg"\},"type":"image_url"\},\{"image_url":\{"size_bytes":500000,"url":"https://oss\.example/preview\.jpg"\},"type":"image_url"\}\]`, string(normalized))
}

func TestNormalizeGeminiProImageResponseConvertsMarkdownImageContent(t *testing.T) {
	body := []byte(`{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "好的，以下是图片：\n\n![image](https://oss.example/generated-media/a.png)"
			}
		}]
	}`)

	normalized, changed, err := NormalizeGeminiProImageResponse(body, "gemini-3.1-flash-image-preview")

	require.NoError(t, err)
	require.True(t, changed)
	assert.NotContains(t, string(normalized), "![image]")

	var root map[string]any
	require.NoError(t, json.Unmarshal(normalized, &root))
	choices := root["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	content := message["content"].([]any)
	require.Len(t, content, 2)
	assert.Equal(t, "text", content[0].(map[string]any)["type"])
	assert.Equal(t, "image_url", content[1].(map[string]any)["type"])
	imageURL := content[1].(map[string]any)["image_url"].(map[string]any)
	assert.Equal(t, "https://oss.example/generated-media/a.png", imageURL["url"])
}

func TestNormalizeGeminiProImageResponseConvertsMarkdownImageTextPart(t *testing.T) {
	body := []byte(`{
		"choices": [{
			"message": {
				"content": [
					{"type":"text","text":"说明\n![image](https://oss.example/generated-media/b.png)"}
				]
			}
		}]
	}`)

	normalized, changed, err := NormalizeGeminiProImageResponse(body, "nano-banana-pro-preview")

	require.NoError(t, err)
	require.True(t, changed)
	assert.NotContains(t, string(normalized), "![image]")

	var root map[string]any
	require.NoError(t, json.Unmarshal(normalized, &root))
	choices := root["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	content := message["content"].([]any)
	require.Len(t, content, 2)
	assert.Equal(t, "text", content[0].(map[string]any)["type"])
	assert.Equal(t, "image_url", content[1].(map[string]any)["type"])
	imageURL := content[1].(map[string]any)["image_url"].(map[string]any)
	assert.Equal(t, "https://oss.example/generated-media/b.png", imageURL["url"])
}

func TestNormalizeGeminiProImageResponseKeepsEqualSizedOrder(t *testing.T) {
	body := []byte(`{
		"choices": [{
			"message": {
				"content": [
					{"type":"image_url","image_url":{"url":"https://oss.example/a.jpg","size_bytes":1000000}},
					{"type":"image_url","image_url":{"url":"https://oss.example/b.jpg","size_bytes":1000000}}
				]
			}
		}]
	}`)

	normalized, changed, err := NormalizeGeminiProImageResponse(body, "gemini-3-pro-image-preview")

	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, string(body), string(normalized))
}

func TestNormalizeGeminiProImageResponseAppliesToFlash31Image(t *testing.T) {
	body := []byte(`{
		"choices": [{
			"message": {
				"content": [
					{"type":"image_url","image_url":{"url":"https://oss.example/preview.jpg","size_bytes":500000}},
					{"type":"image_url","image_url":{"url":"https://oss.example/final.jpg","size_bytes":2500000}}
				]
			}
		}]
	}`)

	normalized, changed, err := NormalizeGeminiProImageResponse(body, "gemini-3.1-flash-image-preview")

	require.NoError(t, err)
	require.True(t, changed)
	assert.Contains(t, string(normalized), `"url":"https://oss.example/final.jpg"`)
}

func TestNormalizeGeminiProImageResponseSkipsOtherModels(t *testing.T) {
	body := []byte(`{
		"choices": [{
			"message": {
				"content": [
					{"type":"image_url","image_url":{"url":"https://oss.example/preview.jpg","size_bytes":500000}},
					{"type":"image_url","image_url":{"url":"https://oss.example/final.jpg","size_bytes":2500000}}
				]
			}
		}]
	}`)

	normalized, changed, err := NormalizeGeminiProImageResponse(body, "gpt-image-2")

	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, string(body), string(normalized))
}

type retryGeneratedMediaUploader struct {
	failures int
	attempts int
	bodies   [][]byte
}

func (u *retryGeneratedMediaUploader) Put(ctx context.Context, key string, data []byte, contentType string) error {
	return u.PutReader(ctx, key, bytes.NewReader(data), contentType)
}

func (u *retryGeneratedMediaUploader) PutReader(ctx context.Context, key string, reader io.Reader, contentType string) error {
	u.attempts++
	body, _ := io.ReadAll(reader)
	u.bodies = append(u.bodies, body)
	if u.attempts <= u.failures {
		return errors.New("temporary upload timeout")
	}
	return nil
}

func (u *retryGeneratedMediaUploader) Get(ctx context.Context, key string) ([]byte, error) {
	return nil, errors.New("not implemented")
}

func (u *retryGeneratedMediaUploader) Head(ctx context.Context, key string) (*oss.ObjectInfo, error) {
	return nil, errors.New("not implemented")
}

func (u *retryGeneratedMediaUploader) Delete(ctx context.Context, key string) error {
	return errors.New("not implemented")
}
