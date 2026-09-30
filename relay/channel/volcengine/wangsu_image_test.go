package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func TestWangsuSeedreamRequestURLs(t *testing.T) {
	baseURL := "https://aigateway.edgecloudapp.com/v2/gws/test-gateway"
	adaptor := &Adaptor{}
	for _, test := range []struct {
		mode int
		want string
	}{
		{mode: relayconstant.RelayModeImagesGenerations, want: baseURL + wangsuImageGenerationsPath},
		{mode: relayconstant.RelayModeImagesEdits, want: baseURL + wangsuImageEditsPath},
	} {
		info := newWangsuSeedreamRelayInfo(test.mode, baseURL)
		got, err := adaptor.GetRequestURL(info)
		if err != nil {
			t.Fatalf("GetRequestURL returned error: %v", err)
		}
		if got != test.want {
			t.Fatalf("URL = %q, want %q", got, test.want)
		}
	}
}

func TestWangsuSeedreamDetectionFallsBackToRequestModel(t *testing.T) {
	info := &relaycommon.RelayInfo{
		Request: &dto.ImageRequest{Model: ModelSeedream5Lite},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://aigateway.edgecloudapp.com/v2/gws/test",
		},
	}
	if !isWangsuSeedream5Relay(info) {
		t.Fatal("Wangsu Seedream request was not detected from request model")
	}
}

func TestWangsuSeedreamGenerationUsesOfficialN(t *testing.T) {
	two := uint(2)
	request := dto.ImageRequest{
		Model:          ModelSeedream5Lite,
		Prompt:         "Generate two related product views",
		N:              &two,
		Size:           "2K",
		ResponseFormat: "url",
	}
	converted, err := (&Adaptor{}).ConvertImageRequest(nil, newWangsuSeedreamRelayInfo(
		relayconstant.RelayModeImagesGenerations,
		"https://aigateway.edgecloudapp.com/v2/gws/test",
	), request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	payload, ok := converted.(*wangsuImageGenerationRequest)
	if !ok {
		t.Fatalf("converted type = %T", converted)
	}
	if payload.N != 2 || payload.Prompt != request.Prompt || payload.ResponseFormat != "url" || payload.Size != "2K" {
		t.Fatalf("payload = %#v", payload)
	}
	body, err := common.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	for _, unsupported := range []string{"sequential_image_generation", "output_format", "watermark"} {
		if strings.Contains(string(body), unsupported) {
			t.Fatalf("Wangsu payload unexpectedly contains %q: %s", unsupported, body)
		}
	}
}

func TestWangsuSeedreamGenerationMapsGroupLimitToN(t *testing.T) {
	request := dto.ImageRequest{
		Model:  ModelSeedream5Lite,
		Prompt: "Generate a three-frame sequence",
		Extra: map[string]json.RawMessage{
			"sequential_image_generation":         json.RawMessage(`"auto"`),
			"sequential_image_generation_options": json.RawMessage(`{"max_images":3}`),
		},
	}
	converted, err := (&Adaptor{}).ConvertImageRequest(nil, newWangsuSeedreamRelayInfo(
		relayconstant.RelayModeImagesGenerations,
		"https://aigateway.edgecloudapp.com/v1/account/gateway",
	), request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	if got := converted.(*wangsuImageGenerationRequest).N; got != 3 {
		t.Fatalf("n = %d, want 3", got)
	}

	two := uint(2)
	request.N = &two
	if _, err := (&Adaptor{}).ConvertImageRequest(nil, newWangsuSeedreamRelayInfo(
		relayconstant.RelayModeImagesGenerations,
		"https://aigateway.edgecloudapp.com/v1/account/gateway",
	), request); err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("conflicting n error = %v", err)
	}
}

func TestWangsuSeedreamGenerationRejectsUndocumentedFields(t *testing.T) {
	request := dto.ImageRequest{
		Model:  ModelSeedream5Lite,
		Prompt: "test",
		Image:  json.RawMessage(`"https://example.com/reference.png"`),
	}
	_, err := (&Adaptor{}).ConvertImageRequest(nil, newWangsuSeedreamRelayInfo(
		relayconstant.RelayModeImagesGenerations,
		"https://aigateway.edgecloudapp.com/v2/gws/test",
	), request)
	if err == nil || !strings.Contains(err.Error(), "image is not supported") {
		t.Fatalf("reference image error = %v", err)
	}
}

func TestWangsuSeedreamJSONEditBuildsSupplierCompatibleJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(`{}`))
	ctx.Request.Header.Set("Content-Type", "application/json")

	two := uint(2)
	request := dto.ImageRequest{
		Model:          ModelSeedream5Lite,
		Prompt:         "Replace the object",
		N:              &two,
		Size:           "2048x2048",
		ResponseFormat: "b64_json",
		OutputFormat:   json.RawMessage(`"png"`),
		Watermark:      boolPointer(false),
		Image: json.RawMessage(`[
			"https://example.com/one.png",
			"[reference](https://example.com/two.png)"
		]`),
	}
	converted, err := (&Adaptor{}).ConvertImageRequest(ctx, newWangsuSeedreamRelayInfo(
		relayconstant.RelayModeImagesEdits,
		"https://aigateway.edgecloudapp.com/v2/gws/test",
	), request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	payload, ok := converted.(*wangsuImageEditRequest)
	if !ok {
		t.Fatalf("converted type = %T", converted)
	}
	images, ok := payload.Image.([]string)
	if !ok || len(images) != 2 || images[0] != "https://example.com/one.png" || images[1] != "https://example.com/two.png" {
		t.Fatalf("image values = %#v", payload.Image)
	}
	if payload.N != 2 || payload.ResponseFormat != "b64_json" || payload.OutputFormat != "png" || payload.Watermark == nil || *payload.Watermark {
		t.Fatalf("payload = %#v", payload)
	}
	if got := ctx.Request.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
}

func TestWangsuSeedreamUnifiedEditMapsGroupLimitToN(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(`{}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	request := dto.ImageRequest{
		Model:  ModelSeedream5Lite,
		Prompt: "Create two edited views",
		Image:  json.RawMessage(`"https://example.com/source.png"`),
		Extra: map[string]json.RawMessage{
			"sequential_image_generation":         json.RawMessage(`"auto"`),
			"sequential_image_generation_options": json.RawMessage(`{"max_images":2}`),
		},
	}

	converted, err := (&Adaptor{}).ConvertImageRequest(ctx, newWangsuSeedreamRelayInfo(
		relayconstant.RelayModeImagesEdits,
		"https://aigateway.edgecloudapp.com/v2/gws/test",
	), request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	if payload := converted.(*wangsuImageEditRequest); payload.N != 2 {
		t.Fatalf("n = %d, want 2", payload.N)
	}
}

func TestWangsuSeedreamMultipartEditUploadsFilesAndMaskToOSS(t *testing.T) {
	originalUpload := uploadWangsuImageInputBytes
	uploadWangsuImageInputBytes = func(_ context.Context, data []byte, contentType string) (string, error) {
		return fmt.Sprintf("https://oss.example/%s/%s", contentType, string(data)), nil
	}
	t.Cleanup(func() { uploadWangsuImageInputBytes = originalUpload })

	gin.SetMode(gin.TestMode)
	var incoming bytes.Buffer
	incomingWriter := multipart.NewWriter(&incoming)
	_ = incomingWriter.WriteField("model", ModelSeedream5Lite)
	_ = incomingWriter.WriteField("prompt", "Edit only the masked region")
	_ = incomingWriter.WriteField("image[]", "https://example.com/reference.png")
	imagePart, err := incomingWriter.CreateFormFile("image[]", "source.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = imagePart.Write([]byte("source-image"))
	maskPart, err := incomingWriter.CreateFormFile("mask", "mask.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = maskPart.Write([]byte("mask-image"))
	if err := incomingWriter.Close(); err != nil {
		t.Fatal(err)
	}

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", &incoming)
	ctx.Request.Header.Set("Content-Type", incomingWriter.FormDataContentType())
	request := dto.ImageRequest{Model: ModelSeedream5Lite, Prompt: "Edit only the masked region"}
	converted, err := (&Adaptor{}).ConvertImageRequest(ctx, newWangsuSeedreamRelayInfo(
		relayconstant.RelayModeImagesEdits,
		"https://aigateway.edgecloudapp.com/v2/gws/test",
	), request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	payload := converted.(*wangsuImageEditRequest)
	images, ok := payload.Image.([]string)
	if !ok || len(images) != 2 {
		t.Fatalf("images = %#v", payload.Image)
	}
	if images[0] != "https://example.com/reference.png" || images[1] != "https://oss.example/image/png/source-image" {
		t.Fatalf("images = %#v", images)
	}
	if payload.Mask != "https://oss.example/image/png/mask-image" {
		t.Fatalf("mask = %q", payload.Mask)
	}
	if got := ctx.Request.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content type = %q", got)
	}
}

func TestWangsuSeedreamJSONEditRejectsBase64Input(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(`{}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	request := dto.ImageRequest{
		Model:  ModelSeedream5Lite,
		Prompt: "edit",
		Image:  json.RawMessage(`"data:image/png;base64,aW1hZ2U="`),
	}
	_, err := (&Adaptor{}).ConvertImageRequest(ctx, newWangsuSeedreamRelayInfo(
		relayconstant.RelayModeImagesEdits,
		"https://aigateway.edgecloudapp.com/v2/gws/test",
	), request)
	if err == nil || !strings.Contains(err.Error(), "Base64 image input is not supported") {
		t.Fatalf("error = %v", err)
	}
}

func TestWangsuSeedreamEditDoRequestUsesJSON(t *testing.T) {
	service.InitHttpClient()
	gin.SetMode(gin.TestMode)
	var receivedPath string
	var receivedContentType string
	var received wangsuImageEditRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Errorf("parse upstream JSON: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"https://example.com/result.png"}]}`))
	}))
	defer server.Close()

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(`{}`))
	ctx.Request.Header.Set("Content-Type", "application/json")
	request := dto.ImageRequest{
		Model:  ModelSeedream5Lite,
		Prompt: "edit",
		Image:  json.RawMessage(`"https://example.com/source.png"`),
	}
	info := newWangsuSeedreamRelayInfo(
		relayconstant.RelayModeImagesEdits,
		server.URL+"/edgecloudapp.com/v2/gws/test",
	)
	info.ApiKey = "test-key"
	converted, err := (&Adaptor{}).ConvertImageRequest(ctx, info, request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	body, err := common.Marshal(converted)
	if err != nil {
		t.Fatalf("marshal converted request: %v", err)
	}
	response, err := (&Adaptor{}).DoRequest(ctx, info, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("DoRequest returned error: %v", err)
	}
	defer response.(*http.Response).Body.Close()

	if receivedPath != "/edgecloudapp.com/v2/gws/test/images/edits" {
		t.Fatalf("upstream path = %q", receivedPath)
	}
	if receivedContentType != "application/json" {
		t.Fatalf("upstream content type = %q", receivedContentType)
	}
	if received.Image != "https://example.com/source.png" {
		t.Fatalf("upstream image = %#v", received.Image)
	}
}

func TestWangsuSeedreamEditResponseUsesActualImageCount(t *testing.T) {
	oldUpstreamLogEnabled := common.UpstreamResponseLogEnabled
	common.UpstreamResponseLogEnabled = false
	t.Cleanup(func() { common.UpstreamResponseLogEnabled = oldUpstreamLogEnabled })

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	info := newWangsuSeedreamRelayInfo(
		relayconstant.RelayModeImagesEdits,
		"https://aigateway.edgecloudapp.com/v2/gws/test",
	)
	info.PriceData.UsePrice = true
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"created":1784560000,
			"data":[
				{"url":"https://example.com/one.png"},
				{"url":"https://example.com/two.png"}
			]
		}`)),
	}
	usage, apiErr := (&Adaptor{}).DoResponse(ctx, response, info)
	if apiErr != nil {
		t.Fatalf("DoResponse returned error: %v", apiErr)
	}
	if usage.(*dto.Usage).TotalTokens != 0 {
		t.Fatalf("unexpected synthetic upstream usage: %#v", usage)
	}
	if got := info.PriceData.OtherRatios["n"]; got != 2 {
		t.Fatalf("billing n ratio = %v, want 2", got)
	}
}

func newWangsuSeedreamRelayInfo(mode int, baseURL string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode:       mode,
		OriginModelName: ModelSeedream5Lite,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    baseURL,
			UpstreamModelName: ModelSeedream5Lite,
		},
	}
}

func boolPointer(value bool) *bool {
	return &value
}
