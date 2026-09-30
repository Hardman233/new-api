package volcengine

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func TestXiongdaSeedreamRequestURLIsChannelScoped(t *testing.T) {
	adaptor := &Adaptor{}
	tests := []struct {
		name     string
		model    string
		baseURL  string
		expected string
	}{
		{
			name:     "Xiongda Seedream uses async create",
			model:    ModelSeedream5Lite,
			baseURL:  "http://118.196.64.1",
			expected: "http://118.196.64.1/api/v1/doubao/create_image",
		},
		{
			name:     "full Xiongda create URL is normalized",
			model:    ModelSeedream5Lite,
			baseURL:  "https://ai.zhuque.me/api/v1/doubao/create_image",
			expected: "https://ai.zhuque.me/api/v1/doubao/create_image",
		},
		{
			name:     "other Seedream provider stays synchronous",
			model:    ModelSeedream5Lite,
			baseURL:  "https://aigateway.example.com/v2/gws/test",
			expected: "https://aigateway.example.com/v2/gws/test/api/v3/images/generations",
		},
		{
			name:     "other models on Xiongda host stay unchanged",
			model:    "doubao-seedream-4-5-251128",
			baseURL:  "http://118.196.64.1",
			expected: "http://118.196.64.1/api/v3/images/generations",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := newXiongdaSeedreamTestInfo(test.model, test.baseURL)
			actual, err := adaptor.GetRequestURL(info)
			if err != nil {
				t.Fatalf("GetRequestURL returned error: %v", err)
			}
			if actual != test.expected {
				t.Fatalf("URL = %q, want %q", actual, test.expected)
			}
		})
	}
}

func TestXiongdaSeedreamUnifiedEditUsesAsyncCreateURL(t *testing.T) {
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Lite, "http://118.196.64.1")
	info.RelayMode = relayconstant.RelayModeImagesEdits

	actual, err := (&Adaptor{}).GetRequestURL(info)
	if err != nil {
		t.Fatalf("GetRequestURL returned error: %v", err)
	}
	if actual != "http://118.196.64.1/api/v1/doubao/create_image" {
		t.Fatalf("URL = %q", actual)
	}
}

func TestXiongdaSeedreamUnifiedEditNormalizesURLReferences(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		image          json.RawMessage
		images         json.RawMessage
		n              *uint
		wantImages     []string
		wantPromptPart string
	}{
		{
			name:       "Lite single image",
			model:      ModelSeedream5Lite,
			image:      json.RawMessage(`"https://example.com/reference.png"`),
			wantImages: []string{"https://example.com/reference.png"},
		},
		{
			name:   "Lite images alias and group count",
			model:  ModelSeedream5Lite,
			images: json.RawMessage(`["https://example.com/one.png","https://example.com/two.png"]`),
			n: func() *uint {
				value := uint(2)
				return &value
			}(),
			wantImages:     []string{"https://example.com/one.png", "https://example.com/two.png"},
			wantPromptPart: "generate no more than 2 images",
		},
		{
			name:       "Pro multiple references",
			model:      ModelSeedream5Pro,
			image:      json.RawMessage(`["https://example.com/one.png","https://example.com/two.png"]`),
			wantImages: []string{"https://example.com/one.png", "https://example.com/two.png"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := newXiongdaSeedreamTestInfo(test.model, "http://118.196.64.1")
			info.RelayMode = relayconstant.RelayModeImagesEdits
			request := dto.ImageRequest{
				Model:  test.model,
				Prompt: "Edit the reference image",
				Image:  test.image,
				Images: test.images,
				N:      test.n,
			}

			converted, err := (&Adaptor{}).ConvertImageRequest(nil, info, request)
			if err != nil {
				t.Fatalf("ConvertImageRequest returned error: %v", err)
			}
			payload := converted.(*seedream5ImageRequest)
			var actualImages []string
			if len(test.wantImages) == 1 {
				var image string
				if err := json.Unmarshal(payload.Image, &image); err != nil {
					t.Fatalf("unmarshal image: %v", err)
				}
				actualImages = []string{image}
			} else if err := json.Unmarshal(payload.Image, &actualImages); err != nil {
				t.Fatalf("unmarshal images: %v", err)
			}
			if strings.Join(actualImages, "|") != strings.Join(test.wantImages, "|") {
				t.Fatalf("images = %#v, want %#v", actualImages, test.wantImages)
			}
			if test.wantPromptPart != "" && !strings.Contains(payload.Prompt, test.wantPromptPart) {
				t.Fatalf("prompt = %q, want substring %q", payload.Prompt, test.wantPromptPart)
			}
		})
	}
}

func TestSeedream5UnifiedEditContractWorksAcrossLiteSuppliers(t *testing.T) {
	two := uint(2)
	request := dto.ImageRequest{
		Model:          ModelSeedream5Lite,
		Prompt:         "Create two edited product views",
		Image:          json.RawMessage(`"https://example.com/reference.png"`),
		N:              &two,
		Size:           "2K",
		ResponseFormat: "url",
		Extra: map[string]json.RawMessage{
			"stream": json.RawMessage(`false`),
		},
	}

	wangsuInfo := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesEdits,
		OriginModelName: ModelSeedream5Lite,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    "https://aigateway.edgecloudapp.com/v2/gws/test",
			UpstreamModelName: ModelSeedream5Lite,
		},
	}
	wangsuConverted, err := (&Adaptor{}).ConvertImageRequest(nil, wangsuInfo, request)
	if err != nil {
		t.Fatalf("Wangsu conversion returned error: %v", err)
	}
	if payload := wangsuConverted.(*wangsuImageEditRequest); payload.N != 2 {
		t.Fatalf("Wangsu n = %d, want 2", payload.N)
	}

	xiongdaInfo := newXiongdaSeedreamTestInfo(ModelSeedream5Lite, "http://118.196.64.1")
	xiongdaInfo.RelayMode = relayconstant.RelayModeImagesEdits
	xiongdaConverted, err := (&Adaptor{}).ConvertImageRequest(nil, xiongdaInfo, request)
	if err != nil {
		t.Fatalf("Xiongda conversion returned error: %v", err)
	}
	xiongdaPayload := xiongdaConverted.(*seedream5ImageRequest)
	if xiongdaPayload.SequentialImageGeneration != "auto" || !strings.Contains(xiongdaPayload.Prompt, "generate no more than 2 images") {
		t.Fatalf("Xiongda payload = %#v", xiongdaPayload)
	}
}

func TestXiongdaSeedreamUnifiedEditRejectsUnsupportedInputs(t *testing.T) {
	two := uint(2)
	tests := []struct {
		name        string
		model       string
		request     dto.ImageRequest
		contentType string
		message     string
	}{
		{
			name:    "missing image",
			model:   ModelSeedream5Lite,
			request: dto.ImageRequest{Prompt: "edit"},
			message: "image is required",
		},
		{
			name:  "mask on Xiongda",
			model: ModelSeedream5Lite,
			request: dto.ImageRequest{
				Prompt: "edit",
				Image:  json.RawMessage(`"https://example.com/source.png"`),
				Mask:   json.RawMessage(`"https://example.com/mask.png"`),
			},
			message: "mask is not supported",
		},
		{
			name:        "multipart file upload",
			model:       ModelSeedream5Lite,
			request:     dto.ImageRequest{Prompt: "edit"},
			contentType: "multipart/form-data; boundary=test",
			message:     "multipart file uploads are not supported",
		},
		{
			name:  "Pro group output",
			model: ModelSeedream5Pro,
			request: dto.ImageRequest{
				Prompt: "edit",
				Image:  json.RawMessage(`"https://example.com/source.png"`),
				N:      &two,
			},
			message: "n greater than 1 is not supported",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := newXiongdaSeedreamTestInfo(test.model, "http://118.196.64.1")
			info.RelayMode = relayconstant.RelayModeImagesEdits
			test.request.Model = test.model
			var ctx *gin.Context
			if test.contentType != "" {
				ctx = newXiongdaSeedreamTestContextForMode(httptest.NewRecorder(), relayconstant.RelayModeImagesEdits)
				ctx.Request.Header.Set("Content-Type", test.contentType)
			}
			_, err := (&Adaptor{}).ConvertImageRequest(ctx, info, test.request)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func TestXiongdaSeedreamLiteUsesSupplierLimits(t *testing.T) {
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Lite, "http://118.196.64.1")

	valid := dto.ImageRequest{
		Model:  ModelSeedream5Lite,
		Prompt: "test",
		Size:   "3K",
		Extra: map[string]json.RawMessage{
			"optimize_prompt_options": json.RawMessage(`{"mode":"standard"}`),
		},
	}
	if _, err := (&Adaptor{}).ConvertImageRequest(nil, info, valid); err != nil {
		t.Fatalf("valid Xiongda request returned error: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*dto.ImageRequest)
		message string
	}{
		{
			name: "4K tier",
			mutate: func(request *dto.ImageRequest) {
				request.Size = "4K"
			},
			message: "size must be one of 2K, 3K",
		},
		{
			name: "custom size above supplier maximum",
			mutate: func(request *dto.ImageRequest) {
				request.Size = "4096x4096"
			},
			message: "10404496",
		},
		{
			name: "fast prompt optimization",
			mutate: func(request *dto.ImageRequest) {
				request.Extra["optimize_prompt_options"] = json.RawMessage(`{"mode":"fast"}`)
			},
			message: "must be one of standard",
		},
		{
			name: "web search blocked while supplier wrapper hangs",
			mutate: func(request *dto.ImageRequest) {
				request.Extra["tools"] = json.RawMessage(`[{"type":"web_search"}]`)
			},
			message: `unsupported parameter "tools"`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			request.Extra = map[string]json.RawMessage{
				"optimize_prompt_options": json.RawMessage(`{"mode":"standard"}`),
			}
			test.mutate(&request)
			_, err := (&Adaptor{}).ConvertImageRequest(nil, info, request)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("error = %v, want substring %q", err, test.message)
			}
		})
	}
}

func TestXiongdaSeedreamNestedOptionsUseSupplierCompatibilityFallback(t *testing.T) {
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Lite, "http://118.196.64.1")
	request := dto.ImageRequest{
		Model:  ModelSeedream5Lite,
		Prompt: "Generate a coordinated product series",
		Size:   "2K",
		Extra: map[string]json.RawMessage{
			"optimize_prompt_options":             json.RawMessage(`{"mode":"standard"}`),
			"sequential_image_generation":         json.RawMessage(`"auto"`),
			"sequential_image_generation_options": json.RawMessage(`{"max_images":2}`),
		},
	}

	converted, err := (&Adaptor{}).ConvertImageRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	body, err := common.Marshal(converted)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	for _, omitted := range []string{"optimize_prompt_options", "sequential_image_generation_options"} {
		if strings.Contains(string(body), omitted) {
			t.Fatalf("Xiongda compatibility payload unexpectedly contains %q: %s", omitted, body)
		}
	}
	for _, expected := range []string{`"sequential_image_generation":"auto"`, "Important: generate no more than 2 images."} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("Xiongda compatibility payload missing %q: %s", expected, body)
		}
	}
}

func TestXiongdaSeedreamProUsesSupplierLimits(t *testing.T) {
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Pro, "http://118.196.64.1")
	valid := dto.ImageRequest{
		Model:  ModelSeedream5Pro,
		Prompt: "test",
		Size:   "2048x2048",
		Extra: map[string]json.RawMessage{
			"optimize_prompt_options": json.RawMessage(`{"mode":"standard"}`),
		},
	}
	if _, err := (&Adaptor{}).ConvertImageRequest(nil, info, valid); err != nil {
		t.Fatalf("valid Xiongda Pro request returned error: %v", err)
	}
	for _, documentedSize := range []string{
		"1024x1024", "1152x864", "864x1152", "1312x736", "736x1312", "1248x832", "832x1248", "1568x672",
		"2048x2048", "2304x1728", "1728x2304", "2848x1600", "1600x2848", "2496x1664", "1664x2496", "3136x1344",
	} {
		request := valid
		request.Size = documentedSize
		if _, err := (&Adaptor{}).ConvertImageRequest(nil, info, request); err != nil {
			t.Errorf("documented size %s returned error: %v", documentedSize, err)
		}
	}

	tooLarge := valid
	tooLarge.Size = "3000x1500"
	if _, err := (&Adaptor{}).ConvertImageRequest(nil, info, tooLarge); err == nil || !strings.Contains(err.Error(), "4194304") {
		t.Fatalf("oversized Pro error = %v", err)
	}

	fast := valid
	fast.Extra = map[string]json.RawMessage{
		"optimize_prompt_options": json.RawMessage(`{"mode":"fast"}`),
	}
	if _, err := (&Adaptor{}).ConvertImageRequest(nil, info, fast); err == nil || !strings.Contains(err.Error(), "must be one of standard") {
		t.Fatalf("fast Pro optimization error = %v", err)
	}
}

func TestXiongdaSeedreamProConvertsDocumentedRequest(t *testing.T) {
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Pro, "http://118.196.64.1")
	watermark := false
	request := dto.ImageRequest{
		Model:          ModelSeedream5Pro,
		Prompt:         "Blend the material from image one into the subject in image two",
		Image:          json.RawMessage(`["https://example.com/reference.png","data:image/png;base64,aGVsbG8="]`),
		Size:           "2848x1600",
		OutputFormat:   json.RawMessage(`"png"`),
		ResponseFormat: "url",
		Watermark:      &watermark,
		Extra: map[string]json.RawMessage{
			"optimize_prompt_options":     json.RawMessage(`{"mode":"standard"}`),
			"sequential_image_generation": json.RawMessage(`"disabled"`),
			"stream":                      json.RawMessage(`false`),
		},
	}

	converted, err := (&Adaptor{}).ConvertImageRequest(nil, info, request)
	if err != nil {
		t.Fatalf("ConvertImageRequest returned error: %v", err)
	}
	body, err := common.Marshal(converted)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"model":"doubao-seedream-5-0-pro-260628"`,
		`"size":"2848x1600"`,
		`"output_format":"png"`,
		`"response_format":"url"`,
		`"watermark":false`,
		`"data:image/png;base64,aGVsbG8="`,
	} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("payload missing %q: %s", expected, body)
		}
	}
	if strings.Contains(string(body), "optimize_prompt_options") ||
		strings.Contains(string(body), "sequential_image_generation") ||
		strings.Contains(string(body), `"stream"`) {
		t.Fatalf("payload contains supplier-incompatible fields: %s", body)
	}
}

func TestClassifyXiongdaSeedreamResult(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		state     xiongdaSeedreamPollState
		message   string
		wantError bool
	}{
		{name: "pending without status", body: `{}`, state: xiongdaSeedreamPollPending},
		{name: "processing", body: `{"status":"processing"}`, state: xiongdaSeedreamPollPending},
		{name: "success", body: `{"data":[{"url":"https://example.com/result.jpg"}]}`, state: xiongdaSeedreamPollSucceeded},
		{name: "partial success", body: `{"data":[{"error":{"code":"blocked"}},{"b64_json":"abc"}]}`, state: xiongdaSeedreamPollSucceeded},
		{name: "top level error", body: `{"error":{"code":"content_filter","message":"blocked"}}`, state: xiongdaSeedreamPollFailed, message: "blocked"},
		{name: "failed status", body: `{"status":"failed","message":"supplier failed"}`, state: xiongdaSeedreamPollFailed, message: "supplier failed"},
		{name: "completed without image", body: `{"status":"completed","data":[]}`, state: xiongdaSeedreamPollFailed, message: "without generated images"},
		{name: "invalid JSON", body: `{`, wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, message, err := classifyXiongdaSeedreamResult([]byte(test.body))
			if test.wantError {
				if err == nil {
					t.Fatal("classify unexpectedly succeeded")
				}
				return
			}
			if err != nil {
				t.Fatalf("classify returned error: %v", err)
			}
			if state != test.state {
				t.Fatalf("state = %v, want %v", state, test.state)
			}
			if test.message != "" && !strings.Contains(message, test.message) {
				t.Fatalf("message = %q, want substring %q", message, test.message)
			}
		})
	}
}

func TestPollXiongdaSeedreamResultPendingThenSuccess(t *testing.T) {
	var queryCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != xiongdaSeedreamQueryPath {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer upstream-key" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"id":"task_123"}` {
			t.Errorf("query body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		if queryCount.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"status":"processing"}`)
			return
		}
		_, _ = io.WriteString(w, `{"model":"doubao-seedream-5-0-260128","data":[{"url":"https://example.com/result.jpg"}],"usage":{"generated_images":1,"output_tokens":16384,"total_tokens":16384}}`)
	}))
	defer server.Close()

	ctx := newXiongdaSeedreamTestContext(httptest.NewRecorder())
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Lite, server.URL+xiongdaSeedreamCreatePath)
	info.ApiKey = "upstream-key"
	result, err := pollXiongdaSeedreamResult(ctx, info, "task_123", xiongdaSeedreamPollOptions{
		Interval: time.Millisecond,
		Timeout:  time.Second,
	})
	if err != nil {
		t.Fatalf("poll returned error: %v", err)
	}
	if queryCount.Load() != 2 {
		t.Fatalf("query count = %d, want 2", queryCount.Load())
	}
	if !strings.Contains(string(result.Body), `"generated_images":1`) {
		t.Fatalf("final body = %s", result.Body)
	}
	if result.Response != nil && result.Response.Body != nil {
		_ = result.Response.Body.Close()
	}
}

func TestFetchXiongdaSeedreamResultFallsBackToDoubaoPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == xiongdaSeedreamQueryPath {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"detail":"Not Found"}`)
			return
		}
		if r.URL.Path != xiongdaSeedreamFallbackQueryPath {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"url":"https://example.com/fallback.jpg"}]}`)
	}))
	defer server.Close()

	info := newXiongdaSeedreamTestInfo(ModelSeedream5Lite, server.URL+xiongdaSeedreamCreatePath)
	info.ApiKey = "upstream-key"
	result, err := fetchXiongdaSeedreamResult(context.Background(), info, "task_fallback")
	if err != nil {
		t.Fatalf("fetch returned error: %v", err)
	}
	if result.Response == nil || result.Response.StatusCode != http.StatusOK {
		t.Fatalf("response = %#v", result.Response)
	}
	if !strings.Contains(string(result.Body), "fallback.jpg") {
		t.Fatalf("body = %s", result.Body)
	}
	_ = result.Response.Body.Close()
}

func TestPollXiongdaSeedreamResultTimeoutRetainsLastResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"status":"processing"}`)
	}))
	defer server.Close()

	ctx := newXiongdaSeedreamTestContext(httptest.NewRecorder())
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Lite, server.URL+xiongdaSeedreamCreatePath)
	result, err := pollXiongdaSeedreamResult(ctx, info, "task_timeout", xiongdaSeedreamPollOptions{
		Interval: time.Millisecond,
		Timeout:  15 * time.Millisecond,
	})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline exceeded", err)
	}
	if !strings.Contains(string(result.Body), `"processing"`) {
		t.Fatalf("last response body = %s", result.Body)
	}
	if result.Response != nil && result.Response.Body != nil {
		_ = result.Response.Body.Close()
	}
}

func TestHandleXiongdaSeedreamResponseReturnsFinalImage(t *testing.T) {
	oldUpstreamLogEnabled := common.UpstreamResponseLogEnabled
	common.UpstreamResponseLogEnabled = false
	t.Cleanup(func() { common.UpstreamResponseLogEnabled = oldUpstreamLogEnabled })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"model":"doubao-seedream-5-0-260128",
			"data":[{"url":"https://example.com/result.jpg","size":"2048x2048"}],
			"usage":{"generated_images":1,"output_tokens":16384,"total_tokens":16384}
		}`)
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	ctx := newXiongdaSeedreamTestContext(recorder)
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Lite, server.URL+xiongdaSeedreamCreatePath)
	info.PriceData = types.PriceData{UsePrice: true}
	createResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"task_success"}`)),
	}

	usageAny, apiErr := (&Adaptor{}).DoResponse(ctx, createResponse, info)
	if apiErr != nil {
		t.Fatalf("DoResponse returned error: %v", apiErr)
	}
	usage, ok := usageAny.(*dto.Usage)
	if !ok || usage.CompletionTokens != 16384 {
		t.Fatalf("usage = %#v", usageAny)
	}
	if info.PriceData.OtherRatios["n"] != 1 {
		t.Fatalf("billing n ratio = %v", info.PriceData.OtherRatios["n"])
	}
	for _, expected := range []string{`"url":"https://example.com/result.jpg"`, `"generated_images":1`} {
		if !strings.Contains(recorder.Body.String(), expected) {
			t.Fatalf("response missing %s: %s", expected, recorder.Body.String())
		}
	}
}

func TestHandleXiongdaSeedreamUnifiedEditResponseReturnsFinalImage(t *testing.T) {
	oldUpstreamLogEnabled := common.UpstreamResponseLogEnabled
	common.UpstreamResponseLogEnabled = false
	t.Cleanup(func() { common.UpstreamResponseLogEnabled = oldUpstreamLogEnabled })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"model":"doubao-seedream-5-0-260128",
			"data":[{"url":"https://example.com/edited.jpg"}],
			"usage":{"generated_images":1,"output_tokens":16384,"total_tokens":16384}
		}`)
	}))
	defer server.Close()

	recorder := httptest.NewRecorder()
	ctx := newXiongdaSeedreamTestContextForMode(recorder, relayconstant.RelayModeImagesEdits)
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Lite, server.URL+xiongdaSeedreamCreatePath)
	info.RelayMode = relayconstant.RelayModeImagesEdits
	createResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"task_edit_success"}`)),
	}

	usageAny, apiErr := (&Adaptor{}).DoResponse(ctx, createResponse, info)
	if apiErr != nil {
		t.Fatalf("DoResponse returned error: %v", apiErr)
	}
	usage := usageAny.(*dto.Usage)
	if usage.CompletionTokens != 16384 {
		t.Fatalf("usage = %#v", usage)
	}
	if !strings.Contains(recorder.Body.String(), `"url":"https://example.com/edited.jpg"`) {
		t.Fatalf("response = %s", recorder.Body.String())
	}
}

func TestHandleXiongdaSeedreamResponseRejectsMissingTaskID(t *testing.T) {
	oldUpstreamLogEnabled := common.UpstreamResponseLogEnabled
	common.UpstreamResponseLogEnabled = false
	t.Cleanup(func() { common.UpstreamResponseLogEnabled = oldUpstreamLogEnabled })

	ctx := newXiongdaSeedreamTestContext(httptest.NewRecorder())
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Lite, "http://118.196.64.1")
	createResponse := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"invalid_request","message":"bad request"}}`)),
	}

	_, apiErr := (&Adaptor{}).DoResponse(ctx, createResponse, info)
	if apiErr == nil || apiErr.StatusCode != http.StatusBadGateway || !strings.Contains(apiErr.Error(), "bad request") {
		t.Fatalf("error = %#v", apiErr)
	}
}

func TestXiongdaSeedreamLiteRejectsReferenceAndOutputTotalAboveFifteen(t *testing.T) {
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Lite, "http://118.196.64.1")
	images := make([]string, 14)
	for index := range images {
		images[index] = "https://example.com/reference.png"
	}
	imageJSON, err := common.Marshal(images)
	if err != nil {
		t.Fatal(err)
	}
	request := dto.ImageRequest{
		Model:  ModelSeedream5Lite,
		Prompt: "generate a pair",
		Image:  imageJSON,
		Extra: map[string]json.RawMessage{
			"sequential_image_generation":         json.RawMessage(`"auto"`),
			"sequential_image_generation_options": json.RawMessage(`{"max_images":2}`),
		},
	}
	_, err = (&Adaptor{}).ConvertImageRequest(nil, info, request)
	if err == nil || !strings.Contains(err.Error(), "must not exceed 15") {
		t.Fatalf("combined image limit error = %v", err)
	}
}

func TestXiongdaSeedreamProAcceptsOnlyDisabledSequentialMode(t *testing.T) {
	info := newXiongdaSeedreamTestInfo(ModelSeedream5Pro, "http://118.196.64.1")
	request := dto.ImageRequest{
		Model:  ModelSeedream5Pro,
		Prompt: "generate one image",
		Extra: map[string]json.RawMessage{
			"sequential_image_generation": json.RawMessage(`"disabled"`),
		},
	}
	converted, err := (&Adaptor{}).ConvertImageRequest(nil, info, request)
	if err != nil {
		t.Fatalf("disabled mode returned error: %v", err)
	}
	payload := converted.(*seedream5ImageRequest)
	if payload.SequentialImageGeneration != "" {
		t.Fatalf("Xiongda Pro payload must omit explicit disabled mode, got %q", payload.SequentialImageGeneration)
	}

	request.Extra["sequential_image_generation"] = json.RawMessage(`"auto"`)
	if _, err := (&Adaptor{}).ConvertImageRequest(nil, info, request); err == nil || !strings.Contains(err.Error(), "is not supported") {
		t.Fatalf("auto mode error = %v", err)
	}
}

func newXiongdaSeedreamTestInfo(model, baseURL string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeImagesGenerations,
		OriginModelName: model,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelBaseUrl:    baseURL,
			UpstreamModelName: model,
		},
	}
}

func newXiongdaSeedreamTestContext(recorder *httptest.ResponseRecorder) *gin.Context {
	return newXiongdaSeedreamTestContextForMode(recorder, relayconstant.RelayModeImagesGenerations)
}

func newXiongdaSeedreamTestContextForMode(recorder *httptest.ResponseRecorder, mode int) *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(recorder)
	path := "/v1/images/generations"
	if mode == relayconstant.RelayModeImagesEdits {
		path = "/v1/images/edits"
	}
	ctx.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return ctx
}
