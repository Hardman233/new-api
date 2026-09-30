package youchuan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
)

const (
	requestContextKey        = "youchuan_request"
	rawRequestContextKey     = "youchuan_raw_request"
	clientCallbackContextKey = "youchuan_client_callback"
)

type TaskAdaptor struct {
	taskcommon.BaseBilling
	baseURL string
	apiKey  string
	action  string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.baseURL = strings.TrimRight(strings.TrimSpace(info.ChannelBaseUrl), "/")
	if a.baseURL == "" {
		a.baseURL = DefaultBaseURL
	}
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	action := actionFromRequest(c)
	if _, ok := ModelForAction(action); !ok {
		return service.TaskErrorWrapperLocal(fmt.Errorf("unsupported Youchuan action %q", action), "invalid_request", http.StatusBadRequest)
	}
	request := newActionRequest(action)
	if request == nil {
		return service.TaskErrorWrapperLocal(errors.New("unsupported Youchuan request"), "invalid_request", http.StatusBadRequest)
	}
	if err := common.UnmarshalBodyReusable(c, request); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if storage, storageErr := common.GetBodyStorage(c); storageErr != nil {
		return service.TaskErrorWrapperLocal(storageErr, "invalid_request", http.StatusBadRequest)
	} else if bodyBytes, bytesErr := storage.Bytes(); bytesErr != nil {
		return service.TaskErrorWrapperLocal(bytesErr, "invalid_request", http.StatusBadRequest)
	} else if len(bodyBytes) > 0 {
		var rawRequest map[string]json.RawMessage
		if err := json.Unmarshal(bodyBytes, &rawRequest); err != nil {
			return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
		}
		c.Set(rawRequestContextKey, rawRequest)
	}
	if err := validateActionRequest(action, request); err != nil {
		return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
	}
	if prompt := requestPrompt(request); strings.TrimSpace(prompt) != "" {
		if err := ValidatePromptParameters(prompt); err != nil {
			return service.TaskErrorWrapperLocal(err, "invalid_prompt_parameter", http.StatusBadRequest)
		}
	}
	if callback := requestCallback(request); strings.TrimSpace(callback) != "" {
		if err := validateHTTPURL(callback, "callback"); err != nil {
			return service.TaskErrorWrapperLocal(err, "invalid_request", http.StatusBadRequest)
		}
	}
	a.action = action
	info.Action = action
	c.Set(requestContextKey, request)
	c.Set(clientCallbackContextKey, requestCallback(request))
	c.Set("task_request", relaycommon.TaskSubmitReq{
		Model:  info.OriginModelName,
		Prompt: requestPrompt(request),
	})
	return nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	action := a.action
	if info != nil && info.Action != "" {
		action = info.Action
	}
	if _, ok := ModelForAction(action); !ok {
		return "", fmt.Errorf("unsupported Youchuan action %q", action)
	}
	return strings.TrimRight(a.baseURL, "/") + "/v1/tob/" + action, nil
}

func (a *TaskAdaptor) BuildRequestHeader(_ *gin.Context, req *http.Request, _ *relaycommon.RelayInfo) error {
	credential, err := ParseCredential(a.apiKey)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-youchuan-app", credential.AppID)
	req.Header.Set("x-youchuan-secret", credential.Secret)
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, _ *relaycommon.RelayInfo) (io.Reader, error) {
	var body map[string]json.RawMessage
	if raw, exists := c.Get(rawRequestContextKey); exists {
		if rawRequest, ok := raw.(map[string]json.RawMessage); ok {
			body = make(map[string]json.RawMessage, len(rawRequest)+1)
			for key, value := range rawRequest {
				body[key] = append(json.RawMessage(nil), value...)
			}
		}
	}
	if body == nil {
		request, exists := c.Get(requestContextKey)
		if !exists {
			return nil, errors.New("Youchuan request is missing from context")
		}
		payload, err := common.Marshal(request)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(payload, &body); err != nil {
			return nil, err
		}
	}
	callback := internalCallbackURL()
	// The reroll endpoint rejects the callback field even though the other
	// asynchronous task endpoints accept it. Keep the client callback in
	// context for platform delivery, but omit it from the upstream payload.
	if a.action == "reroll" {
		delete(body, "callback")
	} else if callback == "" {
		delete(body, "callback")
	} else {
		callbackJSON, err := json.Marshal(callback)
		if err != nil {
			return nil, err
		}
		body["callback"] = callbackJSON
	}
	payload, err := common.Marshal(body)
	if err != nil {
		return nil, err
	}
	return bytes.NewReader(payload), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, body io.Reader) (*http.Response, error) {
	requestURL, err := a.BuildRequestURL(info)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, requestURL, body)
	if err != nil {
		return nil, fmt.Errorf("new Youchuan request: %w", err)
	}
	if err = a.BuildRequestHeader(c, req, info); err != nil {
		return nil, fmt.Errorf("setup Youchuan request header: %w", err)
	}
	client, err := service.GetHttpClientWithProxy(info.ChannelSetting.Proxy)
	if err != nil {
		return nil, err
	}
	return CloneNoRedirectClient(client).Do(req)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (string, []byte, *dto.TaskError) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
	}
	_ = resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", body, taskErrorFromResponse(resp.StatusCode, body)
	}
	var response TaskResponse
	if err = common.Unmarshal(body, &response); err != nil {
		return "", body, service.TaskErrorWrapper(fmt.Errorf("decode Youchuan response: %w", err), "invalid_response", http.StatusBadGateway)
	}
	if strings.TrimSpace(response.ID) == "" {
		return "", body, service.TaskErrorWrapper(errors.New("Youchuan response id is empty"), "invalid_response", http.StatusBadGateway)
	}
	if c != nil {
		c.Header("X-New-Api-Task-Id", info.PublicTaskID)
		c.Header("X-New-Api-Upstream-Task-Id", response.ID)
		c.Header("X-New-Api-Billing-Id", info.PublicTaskID)
		c.Data(http.StatusOK, "application/json; charset=utf-8", service.AppendGroupFallbackResponseMetadata(c, body))
	}
	return response.ID, body, nil
}

func (a *TaskAdaptor) FetchTask(baseURL, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, _ := body["task_id"].(string)
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("invalid task_id")
	}
	credential, err := ParseCredential(key)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	requestURL := strings.TrimRight(baseURL, "/") + "/v1/tob/job/" + url.PathEscape(taskID)
	req, err := http.NewRequest(http.MethodGet, requestURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-youchuan-app", credential.AppID)
	req.Header.Set("x-youchuan-secret", credential.Secret)
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, err
	}
	if client == nil {
		client = http.DefaultClient
	}
	return CloneNoRedirectClient(client).Do(req)
}

func (a *TaskAdaptor) ParseTaskResult(body []byte) (*relaycommon.TaskInfo, error) {
	var response TaskResponse
	if err := common.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("decode Youchuan task response: %w", err)
	}
	if strings.TrimSpace(response.ID) == "" {
		return nil, errors.New("Youchuan task response id is empty")
	}
	return ParseTaskResponse(response), nil
}

func (a *TaskAdaptor) GetModelList() []string {
	return append([]string(nil), ModelList...)
}

func (a *TaskAdaptor) GetChannelName() string {
	return "ali-youchuan"
}

func actionFromRequest(c *gin.Context) string {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(c.Request.URL.Path, "/v1/tob/"))
}

func newActionRequest(action string) any {
	switch action {
	case "diffusion":
		return &DiffusionRequest{}
	case "variation":
		return &VariationRequest{}
	case "upscale":
		return &UpscaleRequest{}
	case "reroll":
		return &RerollRequest{}
	case "pan":
		return &PanRequest{}
	case "outpaint":
		return &OutpaintRequest{}
	case "inpaint":
		return &InpaintRequest{}
	case "remix":
		return &RemixRequest{}
	case "edit":
		return &EditRequest{}
	case "upload-paint":
		return &UploadPaintRequest{}
	case "retexture":
		return &RetextureRequest{}
	case "remove-background":
		return &RemoveBackgroundRequest{}
	case "enhance":
		return &EnhanceRequest{}
	case "video-diffusion":
		return &VideoDiffusionRequest{}
	case "extend-video":
		return &ExtendVideoRequest{}
	case "video-upscale":
		return &VideoUpscaleRequest{}
	default:
		return nil
	}
}

func requestCallback(request any) string {
	switch value := request.(type) {
	case *DiffusionRequest:
		return value.Callback
	case *VariationRequest:
		return value.Callback
	case *UpscaleRequest:
		return value.Callback
	case *RerollRequest:
		return value.Callback
	case *PanRequest:
		return value.Callback
	case *OutpaintRequest:
		return value.Callback
	case *InpaintRequest:
		return value.Callback
	case *RemixRequest:
		return value.Callback
	case *EditRequest:
		return value.Callback
	case *UploadPaintRequest:
		return value.Callback
	case *RetextureRequest:
		return value.Callback
	case *RemoveBackgroundRequest:
		return value.Callback
	case *EnhanceRequest:
		return value.Callback
	case *VideoDiffusionRequest:
		return value.Callback
	case *ExtendVideoRequest:
		return value.Callback
	case *VideoUpscaleRequest:
		return value.Callback
	default:
		return ""
	}
}

func requestPrompt(request any) string {
	switch value := request.(type) {
	case *DiffusionRequest:
		return value.Text
	case *VariationRequest:
		return value.RemixPrompt
	case *PanRequest:
		return value.RemixPrompt
	case *OutpaintRequest:
		return value.RemixPrompt
	case *InpaintRequest:
		return value.RemixPrompt
	case *RemixRequest:
		return value.RemixPrompt
	case *EditRequest:
		return value.RemixPrompt
	case *UploadPaintRequest:
		return value.RemixPrompt
	case *RetextureRequest:
		return value.RemixPrompt
	case *VideoDiffusionRequest:
		return value.Prompt
	case *ExtendVideoRequest:
		return value.Prompt
	default:
		return ""
	}
}

func internalCallbackURL() string {
	baseURL := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	if baseURL == "" {
		return ""
	}
	return baseURL + "/v1/tob/callback"
}

func taskErrorFromResponse(status int, body []byte) *dto.TaskError {
	var upstream ErrorResponse
	_ = json.Unmarshal(body, &upstream)
	message := strings.TrimSpace(upstream.Message)
	if message == "" {
		message = strings.TrimSpace(string(body))
	}
	if message == "" {
		message = http.StatusText(status)
	}
	code := strings.TrimSpace(upstream.Reason)
	if code == "" {
		code = "youchuan_upstream_error"
	}
	return service.TaskErrorWrapperUpstreamStatus(errors.New(message), code, status, status)
}

var _ channel.TaskAdaptor = (*TaskAdaptor)(nil)
