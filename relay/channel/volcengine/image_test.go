package volcengine

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func TestSeedream5ModelsRegistered(t *testing.T) {
	for _, expected := range []string{ModelSeedream5Lite, ModelSeedream5Pro} {
		found := false
		for _, model := range ModelList {
			if model == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s is not registered", expected)
		}
	}
}

func TestConvertSeedream5LiteImageRequest(t *testing.T) {
	n := uint(1)
	watermark := false
	request := dto.ImageRequest{
		Model:          ModelSeedream5Lite,
		Prompt:         "Create a current weather poster",
		N:              &n,
		Size:           "3K",
		ResponseFormat: "url",
		OutputFormat:   json.RawMessage(`"png"`),
		Watermark:      &watermark,
		Image:          json.RawMessage(`["https://example.com/a.png","https://example.com/b.png"]`),
		Extra: map[string]json.RawMessage{
			"optimize_prompt_options":             json.RawMessage(`{"mode":"fast"}`),
			"sequential_image_generation":         json.RawMessage(`"auto"`),
			"sequential_image_generation_options": json.RawMessage(`{"max_images":3}`),
			"stream":                              json.RawMessage(`false`),
			"tools":                               json.RawMessage(`[{"type":"web_search"}]`),
		},
	}

	// ImageHelper deep-copies requests before conversion. Provider extras must
	// survive that copy without becoming globally serializable.
	copied, err := common.DeepCopy(&request)
	if err != nil {
		t.Fatalf("DeepCopy returned error: %v", err)
	}
	if len(copied.Extra) != len(request.Extra) {
		t.Fatalf("copied extras = %d, want %d", len(copied.Extra), len(request.Extra))
	}

	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: ModelSeedream5Lite,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: ModelSeedream5Lite,
		},
	}
	converted, err := (&Adaptor{}).ConvertImageRequest(nil, info, *copied)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}

	body, err := common.Marshal(converted)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := common.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	assertJSONField(t, payload, "model", `"`+ModelSeedream5Lite+`"`)
	assertJSONField(t, payload, "prompt", `"Create a current weather poster"`)
	assertJSONField(t, payload, "size", `"3K"`)
	assertJSONField(t, payload, "output_format", `"png"`)
	assertJSONField(t, payload, "response_format", `"url"`)
	assertJSONField(t, payload, "watermark", `false`)
	assertJSONField(t, payload, "image", `["https://example.com/a.png","https://example.com/b.png"]`)
	assertJSONField(t, payload, "optimize_prompt_options", `{"mode":"fast"}`)
	assertJSONField(t, payload, "sequential_image_generation", `"auto"`)
	assertJSONField(t, payload, "sequential_image_generation_options", `{"max_images":3}`)
	assertJSONField(t, payload, "tools", `[{"type":"web_search"}]`)
	for _, field := range []string{"n", "stream", "quality", "user"} {
		if _, ok := payload[field]; ok {
			t.Fatalf("payload unexpectedly contains %q: %s", field, body)
		}
	}
}

func TestConvertSeedream5ProImageRequest(t *testing.T) {
	images := make([]string, 10)
	for i := range images {
		images[i] = "https://example.com/reference.png"
	}
	imageJSON, err := common.Marshal(images)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	watermark := true
	request := dto.ImageRequest{
		Model:          ModelSeedream5Pro,
		Prompt:         "Edit the marked box at coordinates (120, 80) and follow the arrow direction",
		Size:           "1K",
		ResponseFormat: "b64_json",
		OutputFormat:   json.RawMessage(`"jpeg"`),
		Watermark:      &watermark,
		Image:          imageJSON,
		Extra: map[string]json.RawMessage{
			"optimize_prompt_options": json.RawMessage(`{"mode":"standard"}`),
			"stream":                  json.RawMessage(`false`),
		},
	}
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: ModelSeedream5Pro,
	}

	converted, err := (&Adaptor{}).ConvertImageRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	body, err := common.Marshal(converted)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := common.Unmarshal(body, &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	assertJSONField(t, payload, "model", `"`+ModelSeedream5Pro+`"`)
	assertJSONField(t, payload, "size", `"1K"`)
	assertJSONField(t, payload, "output_format", `"jpeg"`)
	assertJSONField(t, payload, "response_format", `"b64_json"`)
	assertJSONField(t, payload, "optimize_prompt_options", `{"mode":"standard"}`)
	assertJSONField(t, payload, "image", string(imageJSON))
	for _, field := range []string{"n", "stream", "sequential_image_generation", "sequential_image_generation_options", "tools"} {
		if _, ok := payload[field]; ok {
			t.Fatalf("Pro payload unexpectedly contains %q: %s", field, body)
		}
	}
}

func TestConvertSeedream5ProRejectsUnsupportedCapabilities(t *testing.T) {
	elevenImages := make([]string, 11)
	for i := range elevenImages {
		elevenImages[i] = "https://example.com/reference.png"
	}
	elevenImagesJSON, err := common.Marshal(elevenImages)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*dto.ImageRequest)
		message string
	}{
		{
			name: "Lite 3K tier",
			mutate: func(request *dto.ImageRequest) {
				request.Size = "3K"
			},
			message: "size must be one of 1K, 2K",
		},
		{
			name: "custom size above Pro maximum",
			mutate: func(request *dto.ImageRequest) {
				request.Size = "4096x4096"
			},
			message: "size total pixels must be between 921600 and 4194304",
		},
		{
			name: "eleven reference images",
			mutate: func(request *dto.ImageRequest) {
				request.Image = elevenImagesJSON
			},
			message: "at most 10 reference images",
		},
		{
			name: "group generation",
			mutate: func(request *dto.ImageRequest) {
				request.Extra["sequential_image_generation"] = json.RawMessage(`"auto"`)
			},
			message: `sequential_image_generation=auto is not supported`,
		},
		{
			name: "group options",
			mutate: func(request *dto.ImageRequest) {
				request.Extra["sequential_image_generation_options"] = json.RawMessage(`{"max_images":2}`)
			},
			message: `unsupported parameter "sequential_image_generation_options"`,
		},
		{
			name: "web search",
			mutate: func(request *dto.ImageRequest) {
				request.Extra["tools"] = json.RawMessage(`[{"type":"web_search"}]`)
			},
			message: `unsupported parameter "tools"`,
		},
		{
			name: "streaming",
			mutate: func(request *dto.ImageRequest) {
				request.Extra["stream"] = json.RawMessage(`true`)
			},
			message: "stream=true is not supported",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := dto.ImageRequest{
				Model:  ModelSeedream5Pro,
				Prompt: "test",
				Size:   "2K",
				Extra:  map[string]json.RawMessage{},
			}
			test.mutate(&request)
			_, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{
				RelayMode:       relayconstant.RelayModeImagesGenerations,
				OriginModelName: ModelSeedream5Pro,
			}, request)
			if err == nil {
				t.Fatalf("ConvertImageRequest unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %q, want substring %q", err, test.message)
			}
			var apiErr *types.NewAPIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadRequest {
				t.Fatalf("error = %#v, want HTTP 400", err)
			}
		})
	}
}

func TestSeedream5ProCustomSize(t *testing.T) {
	for _, size := range []string{"1024x1024", "2048x1024", "1280x720"} {
		t.Run(size, func(t *testing.T) {
			request := dto.ImageRequest{
				Model:  ModelSeedream5Pro,
				Prompt: "test",
				Size:   size,
			}
			if _, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{
				RelayMode:       relayconstant.RelayModeImagesGenerations,
				OriginModelName: ModelSeedream5Pro,
			}, request); err != nil {
				t.Fatalf("size %s returned error: %v", size, err)
			}
		})
	}
}

func TestConvertSeedream5LiteRejectsInvalidParameters(t *testing.T) {
	tooManyImages := make([]string, 15)
	for i := range tooManyImages {
		tooManyImages[i] = "https://example.com/ref.png"
	}
	tooManyImagesJSON, err := common.Marshal(tooManyImages)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	two := uint(2)

	tests := []struct {
		name    string
		mutate  func(*dto.ImageRequest)
		message string
	}{
		{
			name: "streaming",
			mutate: func(request *dto.ImageRequest) {
				request.Extra["stream"] = json.RawMessage(`true`)
			},
			message: "stream=true is not supported",
		},
		{
			name: "unsupported 1K tier",
			mutate: func(request *dto.ImageRequest) {
				request.Size = "1K"
			},
			message: "size must be one of 2K, 3K, 4K",
		},
		{
			name: "custom size below minimum pixels",
			mutate: func(request *dto.ImageRequest) {
				request.Size = "1500x1500"
			},
			message: "size total pixels must be between",
		},
		{
			name: "too many reference images",
			mutate: func(request *dto.ImageRequest) {
				request.Image = tooManyImagesJSON
			},
			message: "at most 14 reference images",
		},
		{
			name: "too many generated images",
			mutate: func(request *dto.ImageRequest) {
				request.Extra["sequential_image_generation_options"] = json.RawMessage(`{"max_images":16}`)
			},
			message: "max_images must be between 1 and 15",
		},
		{
			name: "unsupported tool",
			mutate: func(request *dto.ImageRequest) {
				request.Extra["tools"] = json.RawMessage(`[{"type":"code_interpreter"}]`)
			},
			message: "tools.type must be web_search",
		},
		{
			name: "unknown field",
			mutate: func(request *dto.ImageRequest) {
				request.Extra["seed"] = json.RawMessage(`1`)
			},
			message: "unsupported parameter",
		},
		{
			name: "unknown optimize prompt option",
			mutate: func(request *dto.ImageRequest) {
				request.Extra["optimize_prompt_options"] = json.RawMessage(`{"mode":"fast","unknown":true}`)
			},
			message: `unknown field "unknown"`,
		},
		{
			name: "OpenAI n",
			mutate: func(request *dto.ImageRequest) {
				request.N = &two
			},
			message: "n is not supported",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := dto.ImageRequest{
				Model:  ModelSeedream5Lite,
				Prompt: "test",
				Size:   "2K",
				Extra:  map[string]json.RawMessage{},
			}
			test.mutate(&request)
			_, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{
				RelayMode:       relayconstant.RelayModeImagesGenerations,
				OriginModelName: ModelSeedream5Lite,
			}, request)
			if err == nil {
				t.Fatalf("ConvertImageRequest unexpectedly succeeded")
			}
			if !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %q, want substring %q", err, test.message)
			}
			var apiErr *types.NewAPIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("error type = %T, want *types.NewAPIError", err)
			}
			if apiErr.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", apiErr.StatusCode)
			}
			if !types.IsSkipRetryError(apiErr) {
				t.Fatalf("invalid client request should skip channel retry")
			}
		})
	}
}

func TestSeedream5LiteStreamCannotBypassConversion(t *testing.T) {
	_, err := validateSeedream5RequestBody(strings.NewReader(`{"model":"` + ModelSeedream5Lite + `","prompt":"test","stream":true}`))
	if err == nil || !strings.Contains(err.Error(), "stream=true is not supported") {
		t.Fatalf("validateSeedream5RequestBody error = %v", err)
	}

	reader, err := validateSeedream5RequestBody(strings.NewReader(`{"model":"` + ModelSeedream5Lite + `","prompt":"test","stream":false}`))
	if err != nil {
		t.Fatalf("stream=false returned error: %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read validated body: %v", err)
	}
	if !strings.Contains(string(body), `"stream":false`) {
		t.Fatalf("validated body changed unexpectedly: %s", body)
	}
}

func TestSeedream5LiteFieldsDoNotAffectOtherModels(t *testing.T) {
	request := dto.ImageRequest{
		Model:  "doubao-seedream-4-0-250828",
		Prompt: "test",
		Extra: map[string]json.RawMessage{
			"sequential_image_generation": json.RawMessage(`"auto"`),
			"stream":                      json.RawMessage(`true`),
		},
	}
	converted, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: request.Model,
	}, request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	if _, ok := converted.(dto.ImageRequest); !ok {
		t.Fatalf("converted type = %T, want dto.ImageRequest", converted)
	}
	body, err := common.Marshal(converted)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	if strings.Contains(string(body), "sequential_image_generation") || strings.Contains(string(body), `"stream"`) {
		t.Fatalf("Seedream 4.0 request behavior changed: %s", body)
	}
}

func TestSeedream5LiteResponseUsesGeneratedImageCount(t *testing.T) {
	oldUpstreamLogEnabled := common.UpstreamResponseLogEnabled
	common.UpstreamResponseLogEnabled = false
	t.Cleanup(func() { common.UpstreamResponseLogEnabled = oldUpstreamLogEnabled })

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: ModelSeedream5Lite,
		PriceData: types.PriceData{
			UsePrice: true,
		},
	}
	responseBody := `{
		"created":1784560000,
		"model":"doubao-seedream-5-0-260128",
		"data":[
			{"url":"https://example.com/one.jpeg","size":"3072x3072"},
			{"url":"https://example.com/two.jpeg","size":"3072x3072"}
		],
		"tools":[{"type":"web_search"}],
		"usage":{
			"generated_images":2,
			"input_images":1,
			"output_tokens":73728,
			"total_tokens":73728,
			"tool_usage":{"web_search":1}
		}
	}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}

	usageAny, apiErr := (&Adaptor{}).DoResponse(ctx, resp, info)
	if apiErr != nil {
		t.Fatalf("DoResponse returned error: %v", apiErr)
	}
	usage, ok := usageAny.(*dto.Usage)
	if !ok {
		t.Fatalf("usage type = %T, want *dto.Usage", usageAny)
	}
	if usage.CompletionTokens != 73728 || usage.TotalTokens != 73728 {
		t.Fatalf("usage = %#v", usage)
	}
	if usage.CompletionTokenDetails.ImageTokens != 73728 {
		t.Fatalf("image output tokens = %d, want 73728", usage.CompletionTokenDetails.ImageTokens)
	}
	if got := info.PriceData.OtherRatios["n"]; got != 2 {
		t.Fatalf("billing n ratio = %v, want 2", got)
	}
	for _, expected := range []string{`"generated_images":2`, `"web_search":1`, `"url":"https://example.com/one.jpeg"`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("response body missing %s: %s", expected, recorder.Body.String())
		}
	}
}

func TestSeedream5ProUsesSharedNonStreamingResponseHandler(t *testing.T) {
	oldUpstreamLogEnabled := common.UpstreamResponseLogEnabled
	common.UpstreamResponseLogEnabled = false
	t.Cleanup(func() { common.UpstreamResponseLogEnabled = oldUpstreamLogEnabled })

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: ModelSeedream5Pro,
		PriceData:       types.PriceData{UsePrice: true},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"model":"doubao-seedream-5-0-pro-260628",
			"data":[{"url":"https://example.com/pro.jpeg","output_format":"jpeg","size":"2048x1024"}],
			"usage":{"generated_images":1,"input_images":2,"output_tokens":8192,"total_tokens":8192}
		}`)),
	}

	usageAny, apiErr := (&Adaptor{}).DoResponse(ctx, resp, info)
	if apiErr != nil {
		t.Fatalf("DoResponse returned error: %v", apiErr)
	}
	usage, ok := usageAny.(*dto.Usage)
	if !ok || usage.CompletionTokens != 8192 || usage.CompletionTokenDetails.ImageTokens != 8192 {
		t.Fatalf("usage = %#v", usageAny)
	}
	if got := info.PriceData.OtherRatios["n"]; got != 1 {
		t.Fatalf("billing n ratio = %v, want 1", got)
	}
	for _, expected := range []string{`"output_format":"jpeg"`, `"size":"2048x1024"`, `"input_images":2`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("response body missing %s: %s", expected, recorder.Body.String())
		}
	}
}

func TestSeedream5LiteResponseFallsBackToSuccessfulDataCount(t *testing.T) {
	oldUpstreamLogEnabled := common.UpstreamResponseLogEnabled
	common.UpstreamResponseLogEnabled = false
	t.Cleanup(func() { common.UpstreamResponseLogEnabled = oldUpstreamLogEnabled })

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: ModelSeedream5Lite,
		PriceData:       types.PriceData{UsePrice: true},
	}
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{
			"data":[
				{"url":"https://example.com/success.jpeg"},
				{"error":{"code":"content_filter","message":"blocked"}}
			],
			"usage":{"output_tokens":16384,"total_tokens":16384}
		}`)),
	}

	_, apiErr := (&Adaptor{}).DoResponse(ctx, resp, info)
	if apiErr != nil {
		t.Fatalf("DoResponse returned error: %v", apiErr)
	}
	if got := info.PriceData.OtherRatios["n"]; got != 1 {
		t.Fatalf("billing n ratio = %v, want 1", got)
	}
}

func assertJSONField(t *testing.T, payload map[string]json.RawMessage, field, expected string) {
	t.Helper()
	actual, ok := payload[field]
	if !ok {
		t.Fatalf("payload missing %q", field)
	}
	var actualValue any
	if err := common.Unmarshal(actual, &actualValue); err != nil {
		t.Fatalf("decode actual %s: %v", field, err)
	}
	var expectedValue any
	if err := common.Unmarshal([]byte(expected), &expectedValue); err != nil {
		t.Fatalf("decode expected %s: %v", field, err)
	}
	actualJSON, _ := common.Marshal(actualValue)
	expectedJSON, _ := common.Marshal(expectedValue)
	if string(actualJSON) != string(expectedJSON) {
		t.Fatalf("%s = %s, want %s", field, actualJSON, expectedJSON)
	}
}
