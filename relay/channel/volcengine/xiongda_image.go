package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const (
	xiongdaSeedreamCreatePath        = "/api/v1/doubao/create_image"
	xiongdaSeedreamQueryPath         = "/api/v1/seedance/get_result"
	xiongdaSeedreamFallbackQueryPath = "/api/v1/doubao/get_result"
	xiongdaSeedreamPollInterval      = 3 * time.Second
	xiongdaSeedreamPollTimeout       = 240 * time.Second
	xiongdaSeedreamMaxResponseSize   = 256 * 1024 * 1024
)

type xiongdaSeedreamCreateResponse struct {
	ID     string `json:"id"`
	TaskID string `json:"task_id"`
	Data   *struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
	} `json:"data,omitempty"`
	Error   *seedreamImageError `json:"error,omitempty"`
	Code    json.RawMessage     `json:"code,omitempty"`
	Message string              `json:"message,omitempty"`
	Detail  string              `json:"detail,omitempty"`
}

func (response xiongdaSeedreamCreateResponse) taskID() string {
	for _, candidate := range []string{response.ID, response.TaskID} {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	if response.Data != nil {
		for _, candidate := range []string{response.Data.ID, response.Data.TaskID} {
			if value := strings.TrimSpace(candidate); value != "" {
				return value
			}
		}
	}
	return ""
}

func (response xiongdaSeedreamCreateResponse) errorMessage() string {
	if response.Error != nil && strings.TrimSpace(response.Error.Message) != "" {
		return strings.TrimSpace(response.Error.Message)
	}
	for _, candidate := range []string{response.Message, response.Detail, jsonScalarString(response.Code)} {
		if value := strings.TrimSpace(candidate); value != "" {
			return value
		}
	}
	return ""
}

type xiongdaSeedreamQueryResponse struct {
	seedreamImageResponse
	Status     json.RawMessage `json:"status,omitempty"`
	TaskStatus json.RawMessage `json:"task_status,omitempty"`
	State      json.RawMessage `json:"state,omitempty"`
	Code       json.RawMessage `json:"code,omitempty"`
	Message    string          `json:"message,omitempty"`
	Detail     string          `json:"detail,omitempty"`
}

type xiongdaSeedreamPollState int

const (
	xiongdaSeedreamPollPending xiongdaSeedreamPollState = iota
	xiongdaSeedreamPollSucceeded
	xiongdaSeedreamPollFailed
)

type xiongdaSeedreamPollOptions struct {
	Interval time.Duration
	Timeout  time.Duration
}

type xiongdaSeedreamPollResult struct {
	Response *http.Response
	Body     []byte
}

func isXiongdaSeedream5Relay(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil || seedream5SpecForRelay(info) == nil {
		return false
	}
	baseURL := strings.ToLower(strings.TrimSpace(info.ChannelBaseUrl))
	return strings.Contains(baseURL, "118.196.64.1") ||
		strings.Contains(baseURL, "ai.zhuque.me") ||
		strings.Contains(baseURL, "/api/v1/doubao")
}

func buildXiongdaSeedreamCreateURL(baseURL string) string {
	return xiongdaSeedreamRootURL(baseURL) + xiongdaSeedreamCreatePath
}

func buildXiongdaSeedreamQueryURL(baseURL string) string {
	return xiongdaSeedreamRootURL(baseURL) + xiongdaSeedreamQueryPath
}

func convertXiongdaSeedream5ImageRequest(
	c *gin.Context,
	info *relaycommon.RelayInfo,
	request dto.ImageRequest,
	spec *seedream5ModelSpec,
) (*seedream5ImageRequest, error) {
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations:
		if err := normalizeXiongdaSeedreamImageCount(&request, spec); err != nil {
			return nil, err
		}
		return convertSeedream5ImageRequest(info, request, spec)
	case relayconstant.RelayModeImagesEdits:
		if c != nil && c.Request != nil && strings.Contains(strings.ToLower(c.Request.Header.Get("Content-Type")), "multipart/form-data") {
			return nil, seedream5BadRequest("Xiongda Seedream image edits require JSON image URLs; multipart file uploads are not supported")
		}
		if hasJSONValue(request.Mask) {
			return nil, seedream5BadRequest("mask is not supported by the Xiongda Seedream API")
		}

		images, err := wangsuImageValuesFromRequest(request)
		if err != nil {
			return nil, err
		}
		images = uniqueWangsuImageInputs(images)
		if len(images) == 0 {
			return nil, seedream5BadRequest("image is required for /v1/images/edits")
		}
		if len(images) > spec.MaxReferenceImages {
			return nil, seedream5BadRequest(fmt.Sprintf("image supports at most %d reference images for %s", spec.MaxReferenceImages, spec.ModelID))
		}

		if len(images) == 1 {
			request.Image, err = common.Marshal(images[0])
		} else {
			request.Image, err = common.Marshal(images)
		}
		if err != nil {
			return nil, seedream5BadRequest("failed to normalize image references")
		}
		request.Images = nil
		if err := normalizeXiongdaSeedreamImageCount(&request, spec); err != nil {
			return nil, err
		}
		return convertSeedream5ImageRequest(info, request, spec)
	default:
		return nil, seedream5BadRequest("Xiongda Seedream 5.0 models must use /v1/images/generations or /v1/images/edits")
	}
}

func normalizeXiongdaSeedreamImageCount(request *dto.ImageRequest, spec *seedream5ModelSpec) error {
	if request == nil || request.N == nil || *request.N <= 1 {
		return nil
	}
	if spec == nil {
		return seedream5BadRequest("unsupported Seedream 5.0 model")
	}
	if !spec.SupportsGroup {
		return seedream5BadRequest(fmt.Sprintf("n greater than 1 is not supported by %s", spec.ModelID))
	}

	var mode string
	if raw, ok := request.Extra["sequential_image_generation"]; ok && hasJSONValue(raw) {
		if err := decodeSeedreamJSONField("sequential_image_generation", raw, &mode); err != nil {
			return err
		}
		if mode != "auto" {
			return seedream5BadRequest("n greater than 1 requires sequential_image_generation=auto")
		}
	}

	var options seedreamSequentialImageGenerationOptions
	if raw, ok := request.Extra["sequential_image_generation_options"]; ok && hasJSONValue(raw) {
		if err := decodeSeedreamJSONField("sequential_image_generation_options", raw, &options); err != nil {
			return err
		}
		if options.MaxImages != nil && *options.MaxImages != int(*request.N) {
			return seedream5BadRequest("n conflicts with sequential_image_generation_options.max_images")
		}
	}

	maxImages := int(*request.N)
	options.MaxImages = &maxImages
	if request.Extra == nil {
		request.Extra = make(map[string]json.RawMessage)
	}
	request.Extra["sequential_image_generation"] = json.RawMessage(`"auto"`)
	request.Extra["sequential_image_generation_options"], _ = common.Marshal(options)
	request.N = nil
	return nil
}

func xiongdaSeedreamRootURL(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	for _, suffix := range []string{
		xiongdaSeedreamCreatePath,
		xiongdaSeedreamQueryPath,
		"/api/v1/doubao",
		"/api/v1/seedance",
		"/api/v1",
	} {
		if strings.HasSuffix(strings.ToLower(base), suffix) {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	return strings.TrimRight(base, "/")
}

func handleXiongdaSeedream5ImageResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(errors.New("empty Xiongda Seedream create response"), types.ErrorCodeEmptyResponse, http.StatusBadGateway)
	}

	createBody, err := readXiongdaSeedreamResponseBody(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusBadGateway)
	}
	// The create row must be inserted before the final row so billing queries
	// consistently select the generated-image response as the latest record.
	service.RecordUpstreamResponseLogSync(c, info, resp, createBody)

	var createResponse xiongdaSeedreamCreateResponse
	if err := common.Unmarshal(createBody, &createResponse); err != nil {
		return nil, types.NewOpenAIError(fmt.Errorf("invalid Xiongda Seedream create response: %w", err), types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	taskID := createResponse.taskID()
	if taskID == "" {
		message := createResponse.errorMessage()
		if message == "" {
			message = "Xiongda Seedream create response did not contain a task id"
		}
		return nil, types.NewOpenAIError(errors.New(message), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}

	result, pollErr := pollXiongdaSeedreamResult(c, info, taskID, xiongdaSeedreamPollOptions{
		Interval: xiongdaSeedreamPollInterval,
		Timeout:  xiongdaSeedreamPollTimeout,
	})
	if result.Response != nil {
		defer service.CloseResponseBodyGracefully(result.Response)
	}
	if pollErr != nil {
		if len(result.Body) > 0 {
			service.RecordUpstreamResponseLog(c, info, result.Response, result.Body)
		}
		statusCode := http.StatusBadGateway
		errorCode := types.ErrorCodeBadResponse
		if errors.Is(pollErr, context.DeadlineExceeded) {
			statusCode = http.StatusGatewayTimeout
			errorCode = types.ErrorCodeChannelResponseTimeExceeded
		} else if errors.Is(pollErr, context.Canceled) {
			statusCode = http.StatusRequestTimeout
			errorCode = types.ErrorCodeChannelResponseTimeExceeded
		}
		return nil, types.NewOpenAIError(pollErr, errorCode, statusCode)
	}

	normalizedBody, normalizeErr := normalizeXiongdaSeedreamResponse(result.Body, info)
	if normalizeErr != nil {
		service.RecordUpstreamResponseLog(c, info, result.Response, result.Body)
		return nil, types.NewOpenAIError(normalizeErr, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}
	return handleSeedream5ImageResponseBodyWithUpstreamBody(c, result.Response, info, normalizedBody, result.Body)
}

func normalizeXiongdaSeedreamResponse(body []byte, info *relaycommon.RelayInfo) ([]byte, error) {
	var input map[string]json.RawMessage
	if err := common.Unmarshal(body, &input); err != nil {
		return nil, fmt.Errorf("invalid Xiongda Seedream final response: %w", err)
	}

	output := make(map[string]json.RawMessage, 4)
	for _, field := range []string{"data", "usage", "error"} {
		if raw, ok := input[field]; ok && hasJSONValue(raw) {
			output[field] = raw
		}
	}

	modelName := jsonScalarString(input["model"])
	if modelName == "" && info != nil {
		modelName = firstNonEmptyString(info.OriginModelName, info.UpstreamModelName)
	}
	modelJSON, err := common.Marshal(modelName)
	if err != nil {
		return nil, err
	}
	output["model"] = modelJSON

	created := jsonScalarString(input["created"])
	if created == "" {
		created = jsonScalarString(input["created_at"])
	}
	if created == "" && info != nil && !info.StartTime.IsZero() {
		created = fmt.Sprintf("%d", info.StartTime.Unix())
	}
	if created == "" {
		created = fmt.Sprintf("%d", time.Now().Unix())
	}
	output["created"] = json.RawMessage(created)

	return common.Marshal(output)
}

func pollXiongdaSeedreamResult(c *gin.Context, info *relaycommon.RelayInfo, taskID string, options xiongdaSeedreamPollOptions) (xiongdaSeedreamPollResult, error) {
	if options.Interval <= 0 {
		options.Interval = xiongdaSeedreamPollInterval
	}
	if options.Timeout <= 0 {
		options.Timeout = xiongdaSeedreamPollTimeout
	}

	parent := context.Background()
	if c != nil && c.Request != nil {
		parent = c.Request.Context()
	}
	pollContext, cancel := context.WithTimeout(parent, options.Timeout)
	defer cancel()

	var lastResult xiongdaSeedreamPollResult
	for {
		result, err := fetchXiongdaSeedreamResult(pollContext, info, taskID)
		if result.Response != nil || len(result.Body) > 0 {
			lastResult = result
		}
		if err == nil {
			state, message, classifyErr := classifyXiongdaSeedreamResult(result.Body)
			if classifyErr != nil {
				return result, classifyErr
			}
			switch state {
			case xiongdaSeedreamPollSucceeded:
				return result, nil
			case xiongdaSeedreamPollFailed:
				if message == "" {
					message = "Xiongda Seedream task failed"
				}
				return result, errors.New(message)
			}
		} else if !isRetryableXiongdaSeedreamPollError(result.Response) {
			return result, err
		}

		timer := time.NewTimer(options.Interval)
		select {
		case <-pollContext.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			if errors.Is(pollContext.Err(), context.DeadlineExceeded) {
				return lastResult, fmt.Errorf("Xiongda Seedream task %s timed out after %s: %w", taskID, options.Timeout, context.DeadlineExceeded)
			}
			return lastResult, fmt.Errorf("Xiongda Seedream task %s canceled: %w", taskID, pollContext.Err())
		case <-timer.C:
		}
	}
}

func fetchXiongdaSeedreamResult(ctx context.Context, info *relaycommon.RelayInfo, taskID string) (xiongdaSeedreamPollResult, error) {
	if info == nil {
		return xiongdaSeedreamPollResult{}, errors.New("missing relay info")
	}
	payload, err := common.Marshal(map[string]string{"id": taskID})
	if err != nil {
		return xiongdaSeedreamPollResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, buildXiongdaSeedreamQueryURL(info.ChannelBaseUrl), bytes.NewReader(payload))
	if err != nil {
		return xiongdaSeedreamPollResult{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+info.ApiKey)

	client, err := service.GetHttpClientWithProxy(info.ChannelSetting.Proxy)
	if err != nil {
		return xiongdaSeedreamPollResult{}, fmt.Errorf("create Xiongda Seedream HTTP client: %w", err)
	}
	if client == nil {
		client = http.DefaultClient
	}
	result, queryErr := doXiongdaSeedreamQuery(client, req)
	if result.Response != nil && result.Response.StatusCode == http.StatusNotFound {
		fallbackURL := xiongdaSeedreamRootURL(info.ChannelBaseUrl) + xiongdaSeedreamFallbackQueryPath
		fallbackReq, fallbackErr := http.NewRequestWithContext(ctx, http.MethodPost, fallbackURL, bytes.NewReader(payload))
		if fallbackErr != nil {
			return result, fallbackErr
		}
		fallbackReq.Header.Set("Accept", "application/json")
		fallbackReq.Header.Set("Content-Type", "application/json")
		fallbackReq.Header.Set("Authorization", "Bearer "+info.ApiKey)
		fallbackResult, fallbackErr := doXiongdaSeedreamQuery(client, fallbackReq)
		if fallbackErr == nil || fallbackResult.Response != nil {
			return fallbackResult, fallbackErr
		}
	}
	return result, queryErr
}

func doXiongdaSeedreamQuery(client *http.Client, req *http.Request) (xiongdaSeedreamPollResult, error) {
	resp, err := client.Do(req)
	if err != nil {
		return xiongdaSeedreamPollResult{}, err
	}
	body, readErr := readXiongdaSeedreamResponseBody(resp.Body)
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	result := xiongdaSeedreamPollResult{Response: resp, Body: body}
	if readErr != nil {
		return result, readErr
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return result, fmt.Errorf("Xiongda Seedream query returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return result, nil
}

func readXiongdaSeedreamResponseBody(reader io.Reader) ([]byte, error) {
	if reader == nil {
		return nil, errors.New("empty response body")
	}
	body, err := io.ReadAll(io.LimitReader(reader, xiongdaSeedreamMaxResponseSize+1))
	if err != nil {
		return nil, err
	}
	if len(body) > xiongdaSeedreamMaxResponseSize {
		return nil, fmt.Errorf("Xiongda Seedream response exceeds %d bytes", xiongdaSeedreamMaxResponseSize)
	}
	return body, nil
}

func classifyXiongdaSeedreamResult(body []byte) (xiongdaSeedreamPollState, string, error) {
	var response xiongdaSeedreamQueryResponse
	if err := common.Unmarshal(body, &response); err != nil {
		return xiongdaSeedreamPollFailed, "", fmt.Errorf("invalid Xiongda Seedream query response: %w", err)
	}
	if countSuccessfulSeedreamImages(response.Data) > 0 {
		return xiongdaSeedreamPollSucceeded, "", nil
	}
	if response.Error != nil && (strings.TrimSpace(response.Error.Message) != "" || strings.TrimSpace(response.Error.Code) != "") {
		message := strings.TrimSpace(response.Error.Message)
		if message == "" {
			message = strings.TrimSpace(response.Error.Code)
		}
		return xiongdaSeedreamPollFailed, message, nil
	}

	status := strings.ToLower(firstNonEmptyString(
		jsonScalarString(response.Status),
		jsonScalarString(response.TaskStatus),
		jsonScalarString(response.State),
	))
	switch status {
	case "success", "succeeded", "completed", "done":
		return xiongdaSeedreamPollFailed, "Xiongda Seedream task completed without generated images", nil
	case "failed", "failure", "error", "canceled", "cancelled":
		message := firstNonEmptyString(response.Message, response.Detail, jsonScalarString(response.Code))
		return xiongdaSeedreamPollFailed, message, nil
	case "pending", "queued", "submitted", "running", "processing", "in_progress", "created", "":
		if len(response.Data) > 0 {
			return xiongdaSeedreamPollFailed, "Xiongda Seedream task returned no successful images", nil
		}
		return xiongdaSeedreamPollPending, "", nil
	default:
		return xiongdaSeedreamPollPending, "", nil
	}
}

func isRetryableXiongdaSeedreamPollError(resp *http.Response) bool {
	if resp == nil {
		return true
	}
	return resp.StatusCode == http.StatusNotFound ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= http.StatusInternalServerError
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func jsonScalarString(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	if trimmed[0] == '"' {
		var value string
		if err := common.Unmarshal(trimmed, &value); err == nil {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(string(trimmed))
}
