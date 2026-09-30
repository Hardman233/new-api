package openai

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

func TestConvertImageRequestScopesResponseFormatToAPIPro(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}

	gotAny, err := adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{
		Model:          "gpt-image-2",
		Prompt:         "cat",
		ResponseFormat: "b64_json",
	})
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	got := gotAny.(dto.ImageRequest)
	if got.ResponseFormat != "" {
		t.Fatalf("gpt-image-2 response_format = %q, want empty", got.ResponseFormat)
	}

	gotAny, err = adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{
		Model:          "gpt-image-1.5",
		Prompt:         "cat",
		ResponseFormat: "b64_json",
	})
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	got = gotAny.(dto.ImageRequest)
	if got.ResponseFormat != "" {
		t.Fatalf("other image response_format = %q, want empty", got.ResponseFormat)
	}

	gotAny, err = adaptor.ConvertImageRequest(nil, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.apipro.ai",
		},
	}, dto.ImageRequest{
		Model:          "gpt-image-2",
		Prompt:         "cat",
		ResponseFormat: "b64_json",
		Extra: map[string]json.RawMessage{
			"aspect_ratio": json.RawMessage(`"16:9"`),
			"mode":         json.RawMessage(`"imageEdit"`),
		},
	})
	if err != nil {
		t.Fatalf("APIPro ConvertImageRequest returned error: %v", err)
	}
	apiProBody := gotAny.(map[string]json.RawMessage)
	if _, exists := apiProBody["response_format"]; exists {
		t.Fatal("APIPro gpt-image-2 response_format should be omitted")
	}
	if _, exists := apiProBody["aspect_ratio"]; exists {
		t.Fatal("APIPro gpt-image-2 aspect_ratio should be omitted")
	}
	if _, exists := apiProBody["mode"]; exists {
		t.Fatal("APIPro mode should be omitted")
	}

	gotAny, err = adaptor.ConvertImageRequest(nil, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.apipro.ai",
		},
	}, dto.ImageRequest{
		Model:          "grok-imagine-image",
		Prompt:         "cat",
		ResponseFormat: "url",
	})
	if err != nil {
		t.Fatalf("APIPro Grok ConvertImageRequest returned error: %v", err)
	}
	apiProBody = gotAny.(map[string]json.RawMessage)
	if string(apiProBody["response_format"]) != `"url"` {
		t.Fatalf("APIPro Grok response_format = %s", apiProBody["response_format"])
	}
}

func TestConvertImageGenerationURLInputsStayJSON(t *testing.T) {
	ctx := newImageTestContext("/v1/images/generations")
	n := uint(1)

	gotAny, err := (&Adaptor{}).ConvertImageRequest(ctx, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesGenerations,
	}, dto.ImageRequest{
		Model:  "gpt-image-2",
		Prompt: "edit using refs",
		N:      &n,
		Images: json.RawMessage(`[
			"https://example.com/ref1.png",
			"[https://example.com/ref2.png](https://example.com/ref2.png)"
		]`),
	})
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}

	got, ok := gotAny.(dto.ImageRequest)
	if !ok {
		t.Fatalf("converted request type = %T, want dto.ImageRequest", gotAny)
	}
	if got.Prompt != "edit using refs" {
		t.Fatalf("prompt = %q", got.Prompt)
	}
	if got.N == nil || *got.N != 1 {
		t.Fatalf("n = %#v", got.N)
	}
	if string(got.Images) == "" {
		t.Fatalf("images should stay in JSON request")
	}
	if ct := ctx.Request.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
}

func TestConvertImageEditJSONURLInputsToMultipart(t *testing.T) {
	ctx := newImageTestContext("/v1/images/edits")
	service.InitHttpClient()
	fetchSetting := system_setting.GetFetchSetting()
	oldFetchSetting := *fetchSetting
	fetchSetting.EnableSSRFProtection = false
	defer func() {
		*fetchSetting = oldFetchSetting
	}()

	imageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("fake image bytes"))
	}))
	defer imageServer.Close()

	gotAny, err := (&Adaptor{}).ConvertImageRequest(ctx, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesEdits,
	}, dto.ImageRequest{
		Model:  "gpt-image-2",
		Prompt: "edit using refs",
		Images: json.RawMessage(`["` + imageServer.URL + `/ref1.png", "` + imageServer.URL + `/ref2.png"]`),
	})
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}

	form := readMultipartForm(t, ctx, gotAny)
	files := form.File["image[]"]
	if len(files) != 2 {
		t.Fatalf("image[] files = %#v, want 2 files", files)
	}
	if files[0].Filename != "ref1.png" || files[1].Filename != "ref2.png" {
		t.Fatalf("filenames = %q, %q", files[0].Filename, files[1].Filename)
	}
	file, err := files[0].Open()
	if err != nil {
		t.Fatalf("open image file: %v", err)
	}
	defer file.Close()
	body, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read image file: %v", err)
	}
	if string(body) != "fake image bytes" {
		t.Fatalf("image body = %q", body)
	}
}

func TestConvertAPIProImageEditKeepsJSONAndNormalizesURLInputs(t *testing.T) {
	ctx := newImageTestContext("/v1/images/edits")
	urls := `["https://example.com/ref.png", "https://example.com/ref.png"]`
	gotAny, err := (&Adaptor{}).ConvertImageRequest(ctx, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesEdits,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl: "https://api.apipro.ai/v1",
		},
	}, dto.ImageRequest{
		Model:          "gpt-image-2",
		Prompt:         "edit using refs",
		Images:         json.RawMessage(urls),
		ResponseFormat: "url",
		Extra: map[string]json.RawMessage{
			"mode": json.RawMessage(`"imageEdit"`),
			"seed": json.RawMessage(`123`),
		},
	})
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	if got := ctx.Request.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("content-type = %q, want application/json", got)
	}
	body, ok := gotAny.(map[string]json.RawMessage)
	if !ok {
		t.Fatalf("converted request type = %T, want map[string]json.RawMessage", gotAny)
	}
	if _, exists := body["mode"]; exists {
		t.Fatal("APIPro mode should be omitted")
	}
	if string(body["seed"]) != "123" {
		t.Fatalf("seed=%s", body["seed"])
	}
	if _, exists := body["response_format"]; exists {
		t.Fatal("APIPro gpt-image-2 response_format should be omitted")
	}
	var images []map[string]string
	if err := json.Unmarshal(body["images"], &images); err != nil {
		t.Fatalf("unmarshal images: %v", err)
	}
	if len(images) != 2 {
		t.Fatalf("images count = %d, want 2", len(images))
	}
	for index, image := range images {
		if image["image_url"] != "https://example.com/ref.png" {
			t.Fatalf("images[%d] = %#v", index, image)
		}
	}
}

func TestWriteImageMultipartFieldsFiltersModeOnlyForAPIPro(t *testing.T) {
	request := dto.ImageRequest{
		Model:  "gpt-image-2",
		Prompt: "edit using refs",
		Extra: map[string]json.RawMessage{
			"mode": json.RawMessage(`"imageEdit"`),
			"seed": json.RawMessage(`123`),
		},
	}

	for _, tt := range []struct {
		name     string
		baseURL  string
		wantMode bool
	}{
		{name: "APIPro overseas", baseURL: "https://api.apipro.ai", wantMode: false},
		{name: "APIPro domestic", baseURL: "https://api.wenwen-ai.com", wantMode: false},
		{name: "other compatible channel", baseURL: "https://images.example.com", wantMode: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelBaseUrl: tt.baseURL}}
			if err := writeImageRequestMultipartFields(writer, info, request); err != nil {
				t.Fatalf("write fields: %v", err)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("close writer: %v", err)
			}

			form, err := multipart.NewReader(bytes.NewReader(body.Bytes()), writer.Boundary()).ReadForm(1 << 20)
			if err != nil {
				t.Fatalf("read form: %v", err)
			}
			_, hasMode := form.Value["mode"]
			if hasMode != tt.wantMode {
				t.Fatalf("mode present = %t, want %t", hasMode, tt.wantMode)
			}
			if got := form.Value["seed"]; len(got) != 1 || got[0] != "123" {
				t.Fatalf("seed field = %#v, want [123]", got)
			}
		})
	}
}

func TestAPIProGrokOnlyImageFieldsAreModelScoped(t *testing.T) {
	for _, tt := range []struct {
		model    string
		wantSkip bool
	}{
		{model: "gpt-image-2", wantSkip: true},
		{model: "grok-imagine-image", wantSkip: false},
	} {
		info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://api.apipro.ai",
			UpstreamModelName: tt.model,
		}}
		if got := shouldSkipOpenAIImageUpstreamField(info, "aspect_ratio"); got != tt.wantSkip {
			t.Fatalf("model %s skip aspect_ratio = %t, want %t", tt.model, got, tt.wantSkip)
		}
		if got := shouldSkipAPIProImageJSONField(tt.model, "resolution"); got != tt.wantSkip {
			t.Fatalf("model %s skip resolution = %t, want %t", tt.model, got, tt.wantSkip)
		}
	}
}

func newImageTestContext(path string) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodPost, path, nil)
	ctx.Request.Header.Set("Content-Type", "application/json")
	return ctx
}

func readMultipartForm(t *testing.T, ctx *gin.Context, gotAny any) *multipart.Form {
	t.Helper()

	buf, ok := gotAny.(*bytes.Buffer)
	if !ok {
		t.Fatalf("converted request type = %T, want *bytes.Buffer", gotAny)
	}
	mediaType, params, err := mime.ParseMediaType(ctx.Request.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content-type: %v", err)
	}
	if mediaType != "multipart/form-data" {
		t.Fatalf("media type = %q, want multipart/form-data", mediaType)
	}
	reader := multipart.NewReader(bytes.NewReader(buf.Bytes()), params["boundary"])
	form, err := reader.ReadForm(32 << 20)
	if err != nil {
		t.Fatalf("read multipart form: %v", err)
	}
	return form
}
