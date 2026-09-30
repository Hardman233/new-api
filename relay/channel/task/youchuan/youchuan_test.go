package youchuan

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

func TestCredentialParsingAndFingerprint(t *testing.T) {
	credential, err := ParseCredential(`{"appId":"app-1","secretKey":"secret-1"}`)
	if err != nil || credential.AppID != "app-1" || credential.Secret != "secret-1" {
		t.Fatalf("ParseCredential() = %#v, %v", credential, err)
	}
	fingerprint := CredentialFingerprint(credential)
	if fingerprint == "" || strings.Contains(fingerprint, credential.Secret) || len(fingerprint) != 16 {
		t.Fatalf("invalid credential fingerprint %q", fingerprint)
	}
}

func TestValidatePromptParameters(t *testing.T) {
	valid := []string{
		"a city --ar 16:9 --v 7 --quality 4 --seed 4294967295 --stylize 500 --draft --bs 2",
		"portrait --v 6.1 --cref https://example.com/character.png --cw 80 --stop 50",
		"anime --v niji7 --iw 2 --new-official-flag value", // unknown flags are forwarded
		"a cat --new-official-flag value",
		"anime --niji 7 --profile studio",
		"anime --v niji 7 --iw 2",
	}
	for _, prompt := range valid {
		if err := ValidatePromptParameters(prompt); err != nil {
			t.Errorf("valid prompt %q returned %v", prompt, err)
		}
	}
	invalid := []string{
		"a city --ar nope",
		"a city --seed 4294967296",
		"a city --v 7 --stop 50",
		"a city --v niji7 --tile",
		"a city --v 7 --quality 3",
		"a city --no",
		"a city --profile=",
	}
	for _, prompt := range invalid {
		if err := ValidatePromptParameters(prompt); err == nil {
			t.Errorf("invalid prompt %q was accepted", prompt)
		}
	}
}

func TestBuildRequestBodyPreservesUnknownOfficialFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/tob/diffusion", strings.NewReader(`{"text":"a cat","futureField":{"n":1},"largeSeed":9007199254740993}`))
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	adaptor := &TaskAdaptor{}
	if taskErr := adaptor.ValidateRequestAndSetAction(c, info); taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() error = %v", taskErr)
	}
	body, err := adaptor.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("BuildRequestBody() error = %v", err)
	}
	encoded, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if string(payload["futureField"]) != `{"n":1}` || string(payload["largeSeed"]) != "9007199254740993" {
		t.Fatalf("unknown fields were not preserved: %s", encoded)
	}
}

func TestBuildRequestBodyKeepsClientCallbackAndSendsInternalCallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalServerAddress := system_setting.ServerAddress
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	system_setting.ServerAddress = "https://platform.example.com/"
	fetchSetting.EnableSSRFProtection = false
	t.Cleanup(func() {
		system_setting.ServerAddress = originalServerAddress
		*fetchSetting = originalFetchSetting
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/tob/diffusion", strings.NewReader(`{"text":"a cat","callback":"https://client.example.com/hook"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	adaptor := &TaskAdaptor{}
	if taskErr := adaptor.ValidateRequestAndSetAction(c, info); taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() error = %v", taskErr)
	}
	if got := c.GetString(clientCallbackContextKey); got != "https://client.example.com/hook" {
		t.Fatalf("client callback = %q", got)
	}
	body, err := adaptor.BuildRequestBody(c, info)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatal(err)
	}
	var callback string
	if err := json.Unmarshal(payload["callback"], &callback); err != nil {
		t.Fatal(err)
	}
	if callback != "https://platform.example.com/v1/tob/callback" {
		t.Fatalf("upstream callback = %q", callback)
	}
}

func TestBuildRequestBodyOmitsCallbackForReroll(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalServerAddress := system_setting.ServerAddress
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	system_setting.ServerAddress = "https://platform.example.com/"
	fetchSetting.EnableSSRFProtection = false
	t.Cleanup(func() {
		system_setting.ServerAddress = originalServerAddress
		*fetchSetting = originalFetchSetting
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/tob/reroll", strings.NewReader(`{"jobId":"job","callback":"https://client.example.com/hook"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{TaskRelayInfo: &relaycommon.TaskRelayInfo{}}
	adaptor := &TaskAdaptor{}
	if taskErr := adaptor.ValidateRequestAndSetAction(c, info); taskErr != nil {
		t.Fatalf("ValidateRequestAndSetAction() error = %v", taskErr)
	}
	if got := c.GetString(clientCallbackContextKey); got != "https://client.example.com/hook" {
		t.Fatalf("client callback = %q", got)
	}
	body, err := adaptor.BuildRequestBody(c, info)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, exists := payload["callback"]; exists {
		t.Fatalf("reroll upstream payload contains callback: %s", encoded)
	}
}

func TestValidateActionRequests(t *testing.T) {
	imageNo := 0
	typeCase := 0
	direction := 1
	videoType := 0
	requests := map[string]any{
		"diffusion":         &DiffusionRequest{Text: "a cat"},
		"variation":         &VariationRequest{JobID: "job", ImageNo: &imageNo, Type: &typeCase},
		"upscale":           &UpscaleRequest{JobID: "job", ImageNo: &imageNo, Type: &typeCase},
		"reroll":            &RerollRequest{JobID: "job"},
		"pan":               &PanRequest{JobID: "job", ImageNo: &imageNo, Direction: &direction, Scale: 1.5},
		"outpaint":          &OutpaintRequest{JobID: "job", ImageNo: &imageNo, Scale: 1.5},
		"inpaint":           &InpaintRequest{JobID: "job", ImageNo: &imageNo, Mask: Mask{URL: "https://example.com/mask.png"}},
		"remix":             &RemixRequest{JobID: "job", ImageNo: &imageNo, RemixPrompt: "new"},
		"edit":              &EditRequest{JobID: "job", ImageNo: &imageNo, Canvas: Canvas{Width: 100, Height: 100}, ImagePos: ImagePosition{Width: 100, Height: 100}, RemixPrompt: "new"},
		"upload-paint":      &UploadPaintRequest{ImageURL: "https://example.com/a.png", Mask: Mask{URL: "https://example.com/mask.png"}, Canvas: Canvas{Width: 100, Height: 100}, ImagePos: ImagePosition{Width: 100, Height: 100}, RemixPrompt: "new"},
		"retexture":         &RetextureRequest{ImageURL: "https://example.com/a.png", RemixPrompt: "new"},
		"remove-background": &RemoveBackgroundRequest{ImageURL: "https://example.com/a.png"},
		"enhance":           &EnhanceRequest{JobID: "job", ImageNo: &imageNo},
		"video-diffusion":   &VideoDiffusionRequest{Prompt: "a short video", VideoType: &videoType},
		"extend-video":      &ExtendVideoRequest{JobID: "job", VideoNo: &imageNo, Prompt: "continue"},
		"video-upscale":     &VideoUpscaleRequest{JobID: "job", VideoNo: &imageNo},
	}
	for action, request := range requests {
		if err := validateActionRequest(action, request); err != nil {
			t.Errorf("%s returned %v", action, err)
		}
	}
}

func TestParseTaskResponsePreservesIndexedOutputs(t *testing.T) {
	result := ParseTaskResponse(TaskResponse{
		ID: "job-1", Status: 2, Comment: "JobStatusSuccess",
		URLs:   []string{"https://example.com/0.png", "", "https://example.com/2.mp4"},
		Audits: []string{"", "blocked", ""}, Seed: 4294967295,
	})
	if result.Status != "SUCCESS" || len(result.Outputs) != 3 {
		t.Fatalf("unexpected result %#v", result)
	}
	if result.Outputs[1].RemoteURL != "" || result.Outputs[1].Audit != "blocked" {
		t.Fatalf("indexed audit/output relationship lost: %#v", result.Outputs)
	}
	if result.Outputs[2].MediaKind != "video" {
		t.Fatalf("video output kind = %q", result.Outputs[2].MediaKind)
	}
}

func TestParseTaskResponseFailureCommentWinsOverSuccessStatus(t *testing.T) {
	result := ParseTaskResponse(TaskResponse{ID: "job-1", Status: 2, Comment: "JobStatusFail"})
	if result.Status != "FAILURE" {
		t.Fatalf("status = %q, want FAILURE", result.Status)
	}
}

func TestClientOfficialContracts(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-youchuan-app") != "app" || r.Header.Get("x-youchuan-secret") != "secret" {
			t.Errorf("missing Youchuan credentials on %s", r.URL.Path)
		}
		calls = append(calls, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/moodboards"):
			_, _ = w.Write([]byte(`{"data":[],"total":0,"pageNo":1,"pageSize":10}`))
		case strings.Contains(r.URL.Path, "/moodboard"):
			_, _ = w.Write([]byte(`{"id":"m1","name":"m","description":""}`))
		case strings.HasSuffix(r.URL.Path, "/costs"):
			_, _ = w.Write([]byte(`{"costs":[]}`))
		case strings.HasSuffix(r.URL.Path, "/cost-monthly"):
			_, _ = w.Write([]byte(`{"costs":[]}`))
		case strings.HasSuffix(r.URL.Path, "/subscribe"):
			_, _ = w.Write([]byte(`{"currentAvail":0}`))
		case strings.HasSuffix(r.URL.Path, "/callback"):
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			_, _ = w.Write([]byte(`{"id":"job-1","status":0,"comment":"JobStatusCreated"}`))
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, Credential{AppID: "app", Secret: "secret"}, server.Client())
	ctx := context.Background()
	zero := 0
	if _, err := client.Diffusion(ctx, DiffusionRequest{Text: "cat"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Variation(ctx, VariationRequest{JobID: "j", ImageNo: &zero, Type: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Upscale(ctx, UpscaleRequest{JobID: "j", ImageNo: &zero, Type: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Reroll(ctx, RerollRequest{JobID: "j"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Pan(ctx, PanRequest{JobID: "j", ImageNo: &zero, Direction: &zero, Scale: 1.5}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Outpaint(ctx, OutpaintRequest{JobID: "j", ImageNo: &zero, Scale: 1.5}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Inpaint(ctx, InpaintRequest{JobID: "j", ImageNo: &zero, Mask: Mask{URL: "https://e/m"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Remix(ctx, RemixRequest{JobID: "j", ImageNo: &zero, RemixPrompt: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Edit(ctx, EditRequest{JobID: "j", ImageNo: &zero, Canvas: Canvas{Width: 1, Height: 1}, ImagePos: ImagePosition{Width: 1, Height: 1}, RemixPrompt: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UploadPaint(ctx, UploadPaintRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Retexture(ctx, RetextureRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.RemoveBackground(ctx, RemoveBackgroundRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Enhance(ctx, EnhanceRequest{JobID: "j", ImageNo: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.VideoDiffusion(ctx, VideoDiffusionRequest{Prompt: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.ExtendVideo(ctx, ExtendVideoRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.VideoUpscale(ctx, VideoUpscaleRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Job(ctx, "j"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Costs(ctx, CostQuery{PageNo: 1, PageSize: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CostMonthly(ctx, 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Subscribe(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CallbackDemo(ctx, CallbackDemoRequest{JobID: "j"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.CreateMoodboard(ctx, MoodboardRequest{Name: "m"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.UpdateMoodboard(ctx, "m", MoodboardRequest{Name: "m2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Moodboards(ctx, MoodboardListQuery{PageNo: 1, PageSize: 10}); err != nil {
		t.Fatal(err)
	}

	if len(calls) != 24 {
		t.Fatalf("official method count = %d, want 24; calls=%v", len(calls), calls)
	}
	for _, call := range calls {
		if !strings.Contains(call, "/v1/tob/") {
			t.Errorf("unexpected upstream path %s", call)
		}
	}
}

func TestClientDoesNotForwardCredentialsAcrossRedirects(t *testing.T) {
	var targetCalled atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetCalled.Store(true)
		if r.Header.Get("x-youchuan-app") != "" || r.Header.Get("x-youchuan-secret") != "" {
			t.Error("Youchuan credentials reached redirect target")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	client := NewClient(redirect.URL, Credential{AppID: "app", Secret: "secret"}, redirect.Client())
	_, err := client.Subscribe(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTTP 307") {
		t.Fatalf("Subscribe() error = %v, want redirect response", err)
	}
	if targetCalled.Load() {
		t.Fatal("redirect target was called")
	}
}

func TestErrorResponseShape(t *testing.T) {
	var response ErrorResponse
	if err := json.Unmarshal([]byte(`{"code":400,"message":"bad","reason":"Invalid_Argument"}`), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != 400 || response.Reason != "Invalid_Argument" {
		t.Fatalf("unexpected error response %#v", response)
	}
}
