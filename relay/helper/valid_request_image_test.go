package helper

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
)

func TestMultipartImageEditParsesResponseFormat(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	_ = writer.WriteField("model", "doubao-seedream-5-0-260128")
	_ = writer.WriteField("prompt", "edit")
	_ = writer.WriteField("response_format", "b64_json")
	_ = writer.WriteField("output_format", "png")
	_ = writer.WriteField("watermark", "false")
	image, err := writer.CreateFormFile("image", "source.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = image.Write([]byte("image"))
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &body)
	ctx.Request.Header.Set("Content-Type", writer.FormDataContentType())
	request, err := GetAndValidOpenAIImageRequest(ctx, relayconstant.RelayModeImagesEdits)
	if err != nil {
		t.Fatalf("GetAndValidOpenAIImageRequest returned error: %v", err)
	}
	if request.ResponseFormat != "b64_json" {
		t.Fatalf("response_format = %q", request.ResponseFormat)
	}
	if string(request.OutputFormat) != `"png"` {
		t.Fatalf("output_format = %s", request.OutputFormat)
	}
	if request.Watermark == nil || *request.Watermark {
		t.Fatalf("watermark = %v", request.Watermark)
	}
}

func TestImageRequestPreservesAbsentNAndRejectsExplicitZero(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"doubao-seedream-5-0-260128","prompt":"test"}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	request, err := GetAndValidOpenAIImageRequest(ctx, relayconstant.RelayModeImagesGenerations)
	if err != nil {
		t.Fatalf("absent n returned error: %v", err)
	}
	if request.N != nil {
		t.Fatalf("absent n became %v", *request.N)
	}

	ctx, _ = gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(`{"model":"doubao-seedream-5-0-260128","prompt":"test","n":0}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	if _, err := GetAndValidOpenAIImageRequest(ctx, relayconstant.RelayModeImagesGenerations); err == nil || !strings.Contains(err.Error(), "greater than 0") {
		t.Fatalf("explicit n=0 error = %v", err)
	}
}

func TestNonSeedreamImageRequestKeepsLegacyNDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, body := range []string{
		`{"model":"image-model","prompt":"test"}`,
		`{"model":"image-model","prompt":"test","n":0}`,
	} {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", strings.NewReader(body))
		ctx.Request.Header.Set("Content-Type", "application/json")
		request, err := GetAndValidOpenAIImageRequest(ctx, relayconstant.RelayModeImagesGenerations)
		if err != nil {
			t.Fatalf("legacy image request returned error: %v", err)
		}
		if request.N == nil || *request.N != 1 {
			t.Fatalf("legacy n = %v, want 1", request.N)
		}
	}
}

func TestJSONImageEditPreservesUnifiedSeedreamFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(`{
		"model":"doubao-seedream-5-0-260128",
		"prompt":"edit both references",
		"image":["https://example.com/one.png","https://example.com/two.png"],
		"n":2,
		"sequential_image_generation":"auto",
		"sequential_image_generation_options":{"max_images":2},
		"stream":false
	}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	request, err := GetAndValidOpenAIImageRequest(ctx, relayconstant.RelayModeImagesEdits)
	if err != nil {
		t.Fatalf("GetAndValidOpenAIImageRequest returned error: %v", err)
	}
	if request.N == nil || *request.N != 2 {
		t.Fatalf("n = %v", request.N)
	}
	if !strings.Contains(string(request.Image), "https://example.com/two.png") {
		t.Fatalf("image = %s", request.Image)
	}
	for _, field := range []string{"sequential_image_generation", "sequential_image_generation_options", "stream"} {
		if _, ok := request.Extra[field]; !ok {
			t.Fatalf("missing extra field %q: %#v", field, request.Extra)
		}
	}
}
