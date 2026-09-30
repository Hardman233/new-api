package volcengine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

const (
	seedream5LiteMinPixels = int64(2560 * 1440)
	seedream5LiteMaxPixels = int64(4096 * 4096)
	seedream5ProMinPixels  = int64(1280 * 720)
	seedream5ProMaxPixels  = int64(2048 * 2048)
)

type seedream5ModelSpec struct {
	ModelID             string
	ResolutionTiers     []string
	ExactSizes          []string
	MinPixels           int64
	MaxPixels           int64
	MaxReferenceImages  int
	SupportsGroup       bool
	SupportsDisabled    bool
	SupportsTools       bool
	OptimizePromptModes []string
}

var (
	seedream5LiteSpec = seedream5ModelSpec{
		ModelID:             ModelSeedream5Lite,
		ResolutionTiers:     []string{"2K", "3K", "4K"},
		MinPixels:           seedream5LiteMinPixels,
		MaxPixels:           seedream5LiteMaxPixels,
		MaxReferenceImages:  14,
		SupportsGroup:       true,
		SupportsTools:       true,
		OptimizePromptModes: []string{"standard", "fast"},
	}
	seedream5ProSpec = seedream5ModelSpec{
		ModelID:             ModelSeedream5Pro,
		ResolutionTiers:     []string{"1K", "2K"},
		MinPixels:           seedream5ProMinPixels,
		MaxPixels:           seedream5ProMaxPixels,
		MaxReferenceImages:  10,
		SupportsDisabled:    true,
		OptimizePromptModes: []string{"standard", "fast"},
	}
)

var seedream5CommonExtraFields = map[string]struct{}{
	"optimize_prompt_options": {},
	"stream":                  {},
}

type seedreamOptimizePromptOptions struct {
	Mode string `json:"mode,omitempty"`
}

type seedreamSequentialImageGenerationOptions struct {
	MaxImages *int `json:"max_images,omitempty"`
}

type seedreamTool struct {
	Type string `json:"type"`
}

type seedream5ImageRequest struct {
	Model                            string                                    `json:"model"`
	Prompt                           string                                    `json:"prompt"`
	Image                            json.RawMessage                           `json:"image,omitempty"`
	Size                             string                                    `json:"size,omitempty"`
	OptimizePromptOptions            *seedreamOptimizePromptOptions            `json:"optimize_prompt_options,omitempty"`
	OutputFormat                     string                                    `json:"output_format,omitempty"`
	ResponseFormat                   string                                    `json:"response_format,omitempty"`
	SequentialImageGeneration        string                                    `json:"sequential_image_generation,omitempty"`
	SequentialImageGenerationOptions *seedreamSequentialImageGenerationOptions `json:"sequential_image_generation_options,omitempty"`
	Tools                            []seedreamTool                            `json:"tools,omitempty"`
	Watermark                        *bool                                     `json:"watermark,omitempty"`
}

type seedreamImageUsage struct {
	dto.Usage
	GeneratedImages int            `json:"generated_images"`
	InputImages     int            `json:"input_images"`
	ToolUsage       map[string]int `json:"tool_usage,omitempty"`
}

type seedreamImageData struct {
	URL     string          `json:"url"`
	B64JSON string          `json:"b64_json"`
	Error   json.RawMessage `json:"error"`
}

type seedreamImageError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type seedreamImageResponse struct {
	Data  []seedreamImageData `json:"data"`
	Error *seedreamImageError `json:"error"`
	Usage *seedreamImageUsage `json:"usage"`
}

func seedream5SpecForModel(model string) *seedream5ModelSpec {
	switch {
	case strings.EqualFold(strings.TrimSpace(model), ModelSeedream5Lite):
		return &seedream5LiteSpec
	case strings.EqualFold(strings.TrimSpace(model), ModelSeedream5Pro):
		return &seedream5ProSpec
	default:
		return nil
	}
}

func seedream5SpecForRelay(info *relaycommon.RelayInfo) *seedream5ModelSpec {
	if info == nil {
		return nil
	}
	if spec := seedream5SpecForModel(info.OriginModelName); spec != nil {
		return spec
	}
	if info.ChannelMeta != nil {
		return seedream5SpecForModel(info.ChannelMeta.UpstreamModelName)
	}
	return nil
}

func seedream5SpecForRequest(info *relaycommon.RelayInfo, requestModel string) *seedream5ModelSpec {
	spec := seedream5SpecForRelay(info)
	if spec == nil {
		spec = seedream5SpecForModel(requestModel)
	}
	if spec == nil || !isXiongdaSeedream5Relay(info) {
		return spec
	}

	providerSpec := *spec
	providerSpec.OptimizePromptModes = []string{"standard"}
	// The supplier currently hangs on the documented nested tools object and
	// never returns a task id. Reject it until their wrapper accepts the schema.
	providerSpec.SupportsTools = false
	if providerSpec.ModelID == ModelSeedream5Lite {
		providerSpec.ResolutionTiers = []string{"2K", "3K"}
		providerSpec.MaxPixels = 10404496
	} else if providerSpec.ModelID == ModelSeedream5Pro {
		providerSpec.ExactSizes = []string{
			"1024x1024", "1152x864", "864x1152", "1312x736", "736x1312", "1248x832", "832x1248", "1568x672",
			"2048x2048", "2304x1728", "1728x2304", "2848x1600", "1600x2848", "2496x1664", "1664x2496", "3136x1344",
		}
		providerSpec.MaxPixels = 2048 * 2048
	}
	return &providerSpec
}

func convertSeedream5ImageRequest(info *relaycommon.RelayInfo, request dto.ImageRequest, spec *seedream5ModelSpec) (*seedream5ImageRequest, error) {
	if spec == nil {
		return nil, seedream5BadRequest("unsupported Seedream 5.0 model")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, seedream5BadRequest("prompt is required")
	}
	if err := validateSeedream5CommonFields(request, spec); err != nil {
		return nil, err
	}
	if err := validateSeedream5ExtraFields(request.Extra, spec); err != nil {
		return nil, err
	}
	if err := validateSeedream5Size(request.Size, spec); err != nil {
		return nil, err
	}
	referenceImages, err := validateSeedream5Images(request.Image, spec)
	if err != nil {
		return nil, err
	}
	isXiongda := isXiongdaSeedream5Relay(info)

	payload := &seedream5ImageRequest{
		Model:          request.Model,
		Prompt:         request.Prompt,
		Image:          cloneRawMessage(request.Image),
		Size:           request.Size,
		ResponseFormat: request.ResponseFormat,
		Watermark:      request.Watermark,
	}
	if payload.Model == "" && info != nil && info.ChannelMeta != nil {
		payload.Model = info.ChannelMeta.UpstreamModelName
	}
	if payload.Model == "" {
		return nil, seedream5BadRequest("model is required")
	}

	if request.ResponseFormat != "" && request.ResponseFormat != "url" && request.ResponseFormat != "b64_json" {
		return nil, seedream5BadRequest("response_format must be one of url or b64_json")
	}

	if hasJSONValue(request.OutputFormat) {
		if err := decodeSeedreamJSONField("output_format", request.OutputFormat, &payload.OutputFormat); err != nil {
			return nil, err
		}
		if payload.OutputFormat != "png" && payload.OutputFormat != "jpeg" {
			return nil, seedream5BadRequest("output_format must be one of png or jpeg")
		}
	}

	if raw, ok := request.Extra["optimize_prompt_options"]; ok && hasJSONValue(raw) {
		var options seedreamOptimizePromptOptions
		if err := decodeSeedreamJSONField("optimize_prompt_options", raw, &options); err != nil {
			return nil, err
		}
		if options.Mode != "" && !containsString(spec.OptimizePromptModes, options.Mode) {
			return nil, seedream5BadRequest(fmt.Sprintf(
				"optimize_prompt_options.mode must be one of %s",
				strings.Join(spec.OptimizePromptModes, " or "),
			))
		}
		// Xiongda's async wrapper currently crashes when this nested object is present.
		// Its only accepted mode, standard, is also the upstream default.
		if !isXiongda {
			payload.OptimizePromptOptions = &options
		}
	}

	if raw, ok := request.Extra["sequential_image_generation"]; ok && hasJSONValue(raw) {
		if err := decodeSeedreamJSONField("sequential_image_generation", raw, &payload.SequentialImageGeneration); err != nil {
			return nil, err
		}
		if payload.SequentialImageGeneration != "auto" && payload.SequentialImageGeneration != "disabled" {
			return nil, seedream5BadRequest("sequential_image_generation must be one of auto or disabled")
		}
		if !spec.SupportsGroup && payload.SequentialImageGeneration != "disabled" {
			return nil, seedream5BadRequest(fmt.Sprintf("sequential_image_generation=%s is not supported by %s", payload.SequentialImageGeneration, spec.ModelID))
		}
		// Xiongda documents disabled for Pro, but its current upstream rejects the
		// explicit field. Pro is single-image only, so omission preserves the mode.
		if isXiongda && spec.ModelID == ModelSeedream5Pro && payload.SequentialImageGeneration == "disabled" {
			payload.SequentialImageGeneration = ""
		}
	}

	if raw, ok := request.Extra["sequential_image_generation_options"]; ok && hasJSONValue(raw) {
		var options seedreamSequentialImageGenerationOptions
		if err := decodeSeedreamJSONField("sequential_image_generation_options", raw, &options); err != nil {
			return nil, err
		}
		if options.MaxImages != nil && (*options.MaxImages < 1 || *options.MaxImages > 15) {
			return nil, seedream5BadRequest("sequential_image_generation_options.max_images must be between 1 and 15")
		}
		if options.MaxImages != nil && spec.SupportsGroup && referenceImages+*options.MaxImages > 15 {
			return nil, seedream5BadRequest("reference image count plus max_images must not exceed 15")
		}
		if isXiongda {
			if options.MaxImages != nil && payload.SequentialImageGeneration == "auto" {
				// Preserve max_images as a model instruction while the supplier wrapper
				// cannot serialize sequential_image_generation_options.
				payload.Prompt = appendXiongdaSeedreamImageLimit(payload.Prompt, *options.MaxImages)
			}
		} else {
			payload.SequentialImageGenerationOptions = &options
		}
	}

	if raw, ok := request.Extra["tools"]; ok && hasJSONValue(raw) {
		if err := decodeSeedreamJSONField("tools", raw, &payload.Tools); err != nil {
			return nil, err
		}
		if len(payload.Tools) == 0 {
			return nil, seedream5BadRequest("tools must contain at least one tool")
		}
		for _, tool := range payload.Tools {
			if tool.Type != "web_search" {
				return nil, seedream5BadRequest("tools.type must be web_search")
			}
		}
	}

	if raw, ok := request.Extra["stream"]; ok && hasJSONValue(raw) {
		if err := rejectSeedream5Stream(raw); err != nil {
			return nil, err
		}
	}

	return payload, nil
}

func appendXiongdaSeedreamImageLimit(prompt string, maxImages int) string {
	return fmt.Sprintf("%s\n\nImportant: generate no more than %d images.", strings.TrimSpace(prompt), maxImages)
}

func validateSeedream5CommonFields(request dto.ImageRequest, spec *seedream5ModelSpec) error {
	if request.N != nil && *request.N == 0 {
		return seedream5BadRequest("n must be greater than 0")
	}
	if request.N != nil && *request.N > 1 {
		return seedream5BadRequest(fmt.Sprintf("n is not supported by %s", spec.ModelID))
	}
	unsupported := []struct {
		name    string
		present bool
	}{
		{name: "quality", present: request.Quality != ""},
		{name: "style", present: hasJSONValue(request.Style)},
		{name: "user", present: hasJSONValue(request.User)},
		{name: "extra_fields", present: hasJSONValue(request.ExtraFields)},
		{name: "background", present: hasJSONValue(request.Background)},
		{name: "moderation", present: hasJSONValue(request.Moderation)},
		{name: "output_compression", present: hasJSONValue(request.OutputCompression)},
		{name: "partial_images", present: hasJSONValue(request.PartialImages)},
		{name: "images", present: hasJSONValue(request.Images)},
		{name: "mask", present: hasJSONValue(request.Mask)},
		{name: "input_fidelity", present: hasJSONValue(request.InputFidelity)},
		{name: "watermark_enabled", present: hasJSONValue(request.WatermarkEnabled)},
		{name: "user_id", present: hasJSONValue(request.UserId)},
	}
	for _, field := range unsupported {
		if field.present {
			return seedream5BadRequest(fmt.Sprintf("%s is not supported by %s", field.name, spec.ModelID))
		}
	}
	return nil
}

func validateSeedream5ExtraFields(extra map[string]json.RawMessage, spec *seedream5ModelSpec) error {
	for key := range extra {
		if _, ok := seedream5CommonExtraFields[key]; ok {
			continue
		}
		if spec.SupportsGroup && (key == "sequential_image_generation" || key == "sequential_image_generation_options") {
			continue
		}
		if spec.SupportsDisabled && key == "sequential_image_generation" {
			continue
		}
		if spec.SupportsTools && key == "tools" {
			continue
		}
		return seedream5BadRequest(fmt.Sprintf("unsupported parameter %q for %s", key, spec.ModelID))
	}
	return nil
}

func validateSeedream5Size(size string, spec *seedream5ModelSpec) error {
	if size == "" || containsString(spec.ResolutionTiers, size) || containsString(spec.ExactSizes, size) {
		return nil
	}
	parts := strings.Split(size, "x")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return seedream5BadRequest(fmt.Sprintf("size must be one of %s, or <width>x<height>", strings.Join(spec.ResolutionTiers, ", ")))
	}
	width, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || width <= 0 {
		return seedream5BadRequest("size width must be a positive integer")
	}
	height, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || height <= 0 {
		return seedream5BadRequest("size height must be a positive integer")
	}
	if width > spec.MaxPixels || height > spec.MaxPixels {
		return seedream5BadRequest(fmt.Sprintf("size total pixels must be between %d and %d", spec.MinPixels, spec.MaxPixels))
	}
	pixels := width * height
	if pixels < spec.MinPixels || pixels > spec.MaxPixels {
		return seedream5BadRequest(fmt.Sprintf("size total pixels must be between %d and %d", spec.MinPixels, spec.MaxPixels))
	}
	if width > height*16 || height > width*16 {
		return seedream5BadRequest("size aspect ratio must be between 1/16 and 16")
	}
	return nil
}

func validateSeedream5Images(raw json.RawMessage, spec *seedream5ModelSpec) (int, error) {
	if !hasJSONValue(raw) {
		return 0, nil
	}
	trimmed := bytes.TrimSpace(raw)
	switch trimmed[0] {
	case '"':
		var image string
		if err := common.Unmarshal(trimmed, &image); err != nil || strings.TrimSpace(image) == "" {
			return 0, seedream5BadRequest("image must be a non-empty URL or Base64 data URI")
		}
		return 1, nil
	case '[':
		var images []string
		if err := common.Unmarshal(trimmed, &images); err != nil {
			return 0, seedream5BadRequest("image must be a string or an array of strings")
		}
		if len(images) > spec.MaxReferenceImages {
			return 0, seedream5BadRequest(fmt.Sprintf("image supports at most %d reference images for %s", spec.MaxReferenceImages, spec.ModelID))
		}
		for _, image := range images {
			if strings.TrimSpace(image) == "" {
				return 0, seedream5BadRequest("image entries must be non-empty URLs or Base64 data URIs")
			}
		}
		return len(images), nil
	default:
		return 0, seedream5BadRequest("image must be a string or an array of strings")
	}
}

func validateSeedream5RequestBody(requestBody io.Reader) (io.Reader, error) {
	if requestBody == nil {
		return nil, seedream5BadRequest("request body is required")
	}
	body, err := io.ReadAll(requestBody)
	if err != nil {
		return nil, types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	var envelope map[string]json.RawMessage
	if err := common.Unmarshal(body, &envelope); err != nil {
		return nil, seedream5BadRequest("request body must be valid JSON")
	}
	if raw, ok := envelope["stream"]; ok && hasJSONValue(raw) {
		if err := rejectSeedream5Stream(raw); err != nil {
			return nil, err
		}
	}
	return bytes.NewReader(body), nil
}

func rejectSeedream5Stream(raw json.RawMessage) error {
	var stream bool
	if err := decodeSeedreamJSONField("stream", raw, &stream); err != nil {
		return err
	}
	if stream {
		return seedream5BadRequest("stream=true is not supported; use a non-streaming image request")
	}
	return nil
}

func decodeSeedreamJSONField(name string, raw json.RawMessage, target any) error {
	if err := common.DecodeJsonStrict(bytes.NewReader(raw), target); err != nil {
		return seedream5BadRequest(fmt.Sprintf("invalid %s: %v", name, err))
	}
	return nil
}

func handleSeedream5ImageResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)
	if resp == nil || resp.Body == nil {
		return nil, types.NewOpenAIError(errors.New("empty Seedream response"), types.ErrorCodeEmptyResponse, http.StatusBadGateway)
	}
	if strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/event-stream") {
		return nil, types.NewOpenAIError(errors.New("unexpected streaming Seedream response"), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	return handleSeedream5ImageResponseBody(c, resp, info, responseBody)
}

func handleSeedream5ImageResponseBody(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, responseBody []byte) (*dto.Usage, *types.NewAPIError) {
	return handleSeedream5ImageResponseBodyWithUpstreamBody(c, resp, info, responseBody, responseBody)
}

func handleSeedream5ImageResponseBodyWithUpstreamBody(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo, responseBody, upstreamBody []byte) (*dto.Usage, *types.NewAPIError) {
	if len(upstreamBody) == 0 {
		upstreamBody = responseBody
	}
	upstreamResponseBody := append([]byte(nil), upstreamBody...)
	// Let the synchronous image rewrite claim the media row before the
	// asynchronous upstream logger scans the same URL.
	defer service.RecordUpstreamResponseLog(c, info, resp, upstreamResponseBody)

	var seedreamResponse seedreamImageResponse
	if err := common.Unmarshal(responseBody, &seedreamResponse); err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway)
	}

	generatedImages := countSuccessfulSeedreamImages(seedreamResponse.Data)
	if seedreamResponse.Usage != nil && seedreamResponse.Usage.GeneratedImages > 0 {
		generatedImages = seedreamResponse.Usage.GeneratedImages
	}
	if generatedImages <= 0 {
		message := "Seedream returned no generated images"
		if seedreamResponse.Error != nil && strings.TrimSpace(seedreamResponse.Error.Message) != "" {
			message = seedreamResponse.Error.Message
		}
		return nil, types.NewOpenAIError(errors.New(message), types.ErrorCodeBadResponse, http.StatusBadGateway)
	}
	if info != nil && info.PriceData.UsePrice {
		info.PriceData.AddOtherRatio("n", float64(generatedImages))
	}

	usage := &dto.Usage{}
	if seedreamResponse.Usage != nil {
		*usage = seedreamResponse.Usage.Usage
	}
	usage.NormalizeTokenAliases()
	if usage.OutputTokens > 0 && usage.CompletionTokenDetails.ImageTokens == 0 &&
		usage.CompletionTokenDetails.TextTokens == 0 && usage.CompletionTokenDetails.AudioTokens == 0 &&
		usage.CompletionTokenDetails.ReasoningTokens == 0 {
		usage.CompletionTokenDetails.ImageTokens = usage.CompletionTokens
	}

	if rewrittenBody, changed, rewriteErr := service.RewriteImageResponseBodyToOSS(service.GeneratedMediaRequestContext(c), service.GeneratedMediaUploadParamsFromRelay(c, info), responseBody); rewriteErr != nil {
		return nil, types.NewOpenAIError(rewriteErr, types.ErrorCodeBadResponse, http.StatusBadGateway)
	} else if changed {
		responseBody = rewrittenBody
	}
	service.IOCopyBytesGracefully(c, resp, responseBody)
	return usage, nil
}

func countSuccessfulSeedreamImages(data []seedreamImageData) int {
	count := 0
	for _, image := range data {
		if hasJSONValue(image.Error) {
			continue
		}
		if strings.TrimSpace(image.URL) != "" || strings.TrimSpace(image.B64JSON) != "" {
			count++
		}
	}
	return count
}

func hasJSONValue(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

func cloneRawMessage(raw json.RawMessage) json.RawMessage {
	if !hasJSONValue(raw) {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func seedream5BadRequest(message string) error {
	return types.NewErrorWithStatusCode(errors.New(message), types.ErrorCodeBadRequestBody, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
}
