package volcengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

const (
	wangsuImageGenerationsPath = "/images/generations"
	wangsuImageEditsPath       = "/images/edits"
)

type wangsuImageGenerationRequest struct {
	Model          string `json:"model,omitempty"`
	Prompt         string `json:"prompt"`
	N              uint   `json:"n"`
	ResponseFormat string `json:"response_format,omitempty"`
	Size           string `json:"size,omitempty"`
}

type wangsuImageEditRequest struct {
	Model          string `json:"model,omitempty"`
	Prompt         string `json:"prompt"`
	Image          any    `json:"image"`
	Mask           string `json:"mask,omitempty"`
	N              uint   `json:"n"`
	ResponseFormat string `json:"response_format,omitempty"`
	Size           string `json:"size,omitempty"`
	OutputFormat   string `json:"output_format,omitempty"`
	Watermark      *bool  `json:"watermark,omitempty"`
}

var uploadWangsuImageInputBytes = service.UploadImageInputBytes

func isWangsuSeedream5Relay(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelMeta == nil {
		return false
	}
	if seedream5SpecForRelay(info) == nil {
		request, ok := info.Request.(*dto.ImageRequest)
		if !ok || request == nil || seedream5SpecForModel(request.Model) == nil {
			return false
		}
	}
	baseURL := strings.ToLower(strings.TrimSpace(info.ChannelBaseUrl))
	return strings.Contains(baseURL, "edgecloudapp.com")
}

func buildWangsuImageURL(baseURL, endpoint string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	for _, suffix := range []string{wangsuImageGenerationsPath, wangsuImageEditsPath} {
		if strings.HasSuffix(strings.ToLower(base), suffix) {
			base = base[:len(base)-len(suffix)]
			break
		}
	}
	return strings.TrimRight(base, "/") + endpoint
}

func wangsuSeedreamRequestUsesEditEndpoint(request dto.ImageRequest) bool {
	return hasJSONValue(request.Image) || hasJSONValue(request.Images) || hasJSONValue(request.Mask)
}

func wangsuSeedreamUsesEditEndpoint(info *relaycommon.RelayInfo) bool {
	if !isWangsuSeedream5Relay(info) {
		return false
	}
	if info.RelayMode == relayconstant.RelayModeImagesEdits {
		return true
	}
	if info.RelayMode != relayconstant.RelayModeImagesGenerations {
		return false
	}
	request, ok := info.Request.(*dto.ImageRequest)
	return ok && request != nil && wangsuSeedreamRequestUsesEditEndpoint(*request)
}

func convertWangsuSeedream5ImageRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest, spec *seedream5ModelSpec) (any, error) {
	switch info.RelayMode {
	case relayconstant.RelayModeImagesGenerations:
		if wangsuSeedreamRequestUsesEditEndpoint(request) {
			return convertWangsuSeedream5EditRequest(c, info, request, spec)
		}
		return convertWangsuSeedream5GenerationRequest(request, spec)
	case relayconstant.RelayModeImagesEdits:
		return convertWangsuSeedream5EditRequest(c, info, request, spec)
	default:
		return nil, seedream5BadRequest("Wangsu Seedream 5.0 models must use /v1/images/generations or /v1/images/edits")
	}
}

func convertWangsuSeedream5GenerationRequest(request dto.ImageRequest, spec *seedream5ModelSpec) (*wangsuImageGenerationRequest, error) {
	if spec == nil {
		return nil, seedream5BadRequest("unsupported Seedream 5.0 model")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, seedream5BadRequest("prompt is required")
	}
	if err := validateWangsuResponseFormat(request.ResponseFormat); err != nil {
		return nil, err
	}
	if err := validateWangsuGenerationOutputOptions(request); err != nil {
		return nil, err
	}
	if err := rejectWangsuUnsupportedImageFields(request, false); err != nil {
		return nil, err
	}

	allowedExtra := map[string]struct{}{
		"optimize_prompt_options":             {},
		"stream":                              {},
		"sequential_image_generation":         {},
		"sequential_image_generation_options": {},
	}
	if err := rejectUnknownWangsuSeedreamExtraFields(request.Extra, allowedExtra, spec.ModelID); err != nil {
		return nil, err
	}
	if raw, ok := request.Extra["stream"]; ok && hasJSONValue(raw) {
		if err := rejectSeedream5Stream(raw); err != nil {
			return nil, err
		}
	}
	if err := validateWangsuOptimizePromptOptions(request); err != nil {
		return nil, err
	}

	n, err := resolveWangsuSeedreamImageCount(request)
	if err != nil {
		return nil, err
	}

	return &wangsuImageGenerationRequest{
		Model:          request.Model,
		Prompt:         request.Prompt,
		N:              n,
		ResponseFormat: request.ResponseFormat,
		Size:           request.Size,
	}, nil
}

func convertWangsuSeedream5EditRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.ImageRequest, spec *seedream5ModelSpec) (any, error) {
	if spec == nil {
		return nil, seedream5BadRequest("unsupported Seedream 5.0 model")
	}
	if strings.TrimSpace(request.Prompt) == "" {
		return nil, seedream5BadRequest("prompt is required")
	}
	if err := validateWangsuResponseFormat(request.ResponseFormat); err != nil {
		return nil, err
	}
	if err := rejectWangsuUnsupportedImageFields(request, true); err != nil {
		return nil, err
	}
	allowedExtra := map[string]struct{}{
		"optimize_prompt_options":             {},
		"stream":                              {},
		"sequential_image_generation":         {},
		"sequential_image_generation_options": {},
	}
	if err := rejectUnknownWangsuSeedreamExtraFields(request.Extra, allowedExtra, spec.ModelID); err != nil {
		return nil, err
	}
	if raw, ok := request.Extra["stream"]; ok && hasJSONValue(raw) {
		if err := rejectSeedream5Stream(raw); err != nil {
			return nil, err
		}
	}
	if err := validateWangsuOptimizePromptOptions(request); err != nil {
		return nil, err
	}
	if err := validateSeedream5Size(request.Size, spec); err != nil {
		return nil, err
	}
	requiresMultipart := wangsuSeedreamEditRequiresMultipart(info)
	var outputFormat string
	if hasJSONValue(request.OutputFormat) {
		if err := decodeSeedreamJSONField("output_format", request.OutputFormat, &outputFormat); err != nil {
			return nil, err
		}
		if requiresMultipart && outputFormat != "jpeg" {
			return nil, seedream5BadRequest("output_format must be jpeg for Wangsu multipart image edits")
		}
		if !requiresMultipart && outputFormat != "png" && outputFormat != "jpeg" {
			return nil, seedream5BadRequest("output_format must be one of png or jpeg")
		}
	}
	if requiresMultipart && request.Watermark != nil && *request.Watermark {
		return nil, seedream5BadRequest("watermark=true is not supported by the Wangsu multipart image edits API")
	}
	n, err := resolveWangsuSeedreamImageCount(request)
	if err != nil {
		return nil, err
	}

	imageValues, err := wangsuImageValuesFromRequest(request)
	if err != nil {
		return nil, err
	}
	imageFiles, maskFile, err := wangsuMultipartInputs(c, &imageValues)
	if err != nil {
		return nil, err
	}
	imageValues = uniqueWangsuImageInputs(imageValues)
	if len(imageValues)+len(imageFiles) > spec.MaxReferenceImages {
		return nil, seedream5BadRequest(fmt.Sprintf("image supports at most %d reference images for %s", spec.MaxReferenceImages, spec.ModelID))
	}
	imageParts := make([]wangsuMultipartImagePart, 0, len(imageValues)+len(imageFiles))
	for index, value := range imageValues {
		normalized, normalizeErr := normalizeWangsuImageInput(c, value, fmt.Sprintf("image.%d", index))
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		if requiresMultipart {
			part, downloadErr := downloadWangsuImagePart(normalized, index)
			if downloadErr != nil {
				return nil, downloadErr
			}
			imageParts = append(imageParts, part)
		} else {
			imageParts = append(imageParts, wangsuMultipartImagePart{SourceURL: normalized})
		}
	}
	for index, fileHeader := range imageFiles {
		if requiresMultipart {
			part, fileErr := readWangsuMultipartImagePart(fileHeader, index)
			if fileErr != nil {
				return nil, fileErr
			}
			imageParts = append(imageParts, part)
		} else {
			value, fileErr := uploadWangsuMultipartFile(c, fileHeader, fmt.Sprintf("image file %d", index+1), fmt.Sprintf("image.%d", len(imageValues)))
			if fileErr != nil {
				return nil, fileErr
			}
			imageParts = append(imageParts, wangsuMultipartImagePart{SourceURL: value})
		}
	}
	if len(imageParts) == 0 {
		return nil, seedream5BadRequest("image is required")
	}

	var maskPart *wangsuMultipartImagePart
	if hasJSONValue(request.Mask) {
		maskValues, maskErr := decodeWangsuImageValues(request.Mask)
		if maskErr != nil {
			return nil, seedream5BadRequest("mask must be a non-empty URL or Base64 data URI")
		}
		if len(maskValues) != 1 {
			return nil, seedream5BadRequest("mask supports exactly one image")
		}
		normalized, normalizeErr := normalizeWangsuImageInput(c, maskValues[0], "mask")
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		if requiresMultipart {
			part, downloadErr := downloadWangsuImagePart(normalized, 0)
			if downloadErr != nil {
				return nil, downloadErr
			}
			maskPart = &part
		} else {
			maskPart = &wangsuMultipartImagePart{SourceURL: normalized}
		}
	}
	if maskFile != nil {
		if maskPart != nil {
			return nil, seedream5BadRequest("mask must be provided only once")
		}
		if requiresMultipart {
			part, fileErr := readWangsuMultipartImagePart(maskFile, 0)
			if fileErr != nil {
				return nil, fileErr
			}
			maskPart = &part
		} else {
			value, fileErr := uploadWangsuMultipartFile(c, maskFile, "mask file", "mask")
			if fileErr != nil {
				return nil, fileErr
			}
			maskPart = &wangsuMultipartImagePart{SourceURL: value}
		}
	}

	if wangsuSeedreamEditRequiresMultipart(info) {
		return buildWangsuSeedreamEditMultipartRequest(c, request, imageParts, maskPart, n)
	}
	if c != nil && c.Request != nil {
		c.Request.Header.Set("Content-Type", gin.MIMEJSON)
	}

	var image any = imageParts[0].SourceURL
	if len(imageParts) > 1 {
		images := make([]string, 0, len(imageParts))
		for _, part := range imageParts {
			images = append(images, part.SourceURL)
		}
		image = images
	}
	mask := ""
	if maskPart != nil {
		mask = maskPart.SourceURL
	}
	return &wangsuImageEditRequest{
		Model:          request.Model,
		Prompt:         request.Prompt,
		Image:          image,
		Mask:           mask,
		N:              n,
		ResponseFormat: request.ResponseFormat,
		Size:           request.Size,
		OutputFormat:   outputFormat,
		Watermark:      request.Watermark,
	}, nil
}

type wangsuMultipartImagePart struct {
	Filename    string
	ContentType string
	Data        []byte
	SourceURL   string
}

func wangsuSeedreamEditRequiresMultipart(info *relaycommon.RelayInfo) bool {
	if info == nil || info.ChannelBaseUrl == "" {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(info.ChannelBaseUrl))
	if err != nil {
		return false
	}
	basePath := strings.TrimRight(strings.ToLower(parsed.Path), "/")
	basePath = strings.TrimSuffix(basePath, wangsuImageGenerationsPath)
	basePath = strings.TrimSuffix(basePath, wangsuImageEditsPath)
	return strings.HasSuffix(strings.TrimRight(basePath, "/"), "/lishuaikunpicedit")
}

func buildWangsuSeedreamEditMultipartRequest(c *gin.Context, request dto.ImageRequest, imageParts []wangsuMultipartImagePart, maskPart *wangsuMultipartImagePart, n uint) (*bytes.Buffer, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := map[string]string{
		"model":  request.Model,
		"prompt": request.Prompt,
		"n":      strconv.FormatUint(uint64(n), 10),
	}
	if request.Size != "" {
		fields["size"] = request.Size
	}
	if request.ResponseFormat != "" {
		fields["response_format"] = request.ResponseFormat
	}
	for _, key := range []string{"model", "prompt", "n", "size", "response_format"} {
		if value := fields[key]; value != "" {
			if err := writer.WriteField(key, value); err != nil {
				return nil, fmt.Errorf("write Wangsu multipart field %s: %w", key, err)
			}
		}
	}
	imageField := "image"
	if len(imageParts) > 1 {
		imageField = "image[]"
	}
	for _, part := range imageParts {
		if err := writeWangsuMultipartImagePart(writer, imageField, part); err != nil {
			return nil, err
		}
	}
	if maskPart != nil {
		if err := writeWangsuMultipartImagePart(writer, "mask", *maskPart); err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close Wangsu multipart request: %w", err)
	}
	if c != nil && c.Request != nil {
		c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	}
	return &body, nil
}

func writeWangsuMultipartImagePart(writer *multipart.Writer, fieldName string, part wangsuMultipartImagePart) error {
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="%s"; filename="%s"`, fieldName, part.Filename))
	header.Set("Content-Type", part.ContentType)
	formPart, err := writer.CreatePart(header)
	if err != nil {
		return fmt.Errorf("create Wangsu %s image part: %w", fieldName, err)
	}
	if _, err = formPart.Write(part.Data); err != nil {
		return fmt.Errorf("write Wangsu %s image part: %w", fieldName, err)
	}
	return nil
}

func downloadWangsuImagePart(imageURL string, index int) (wangsuMultipartImagePart, error) {
	resp, err := service.DoDownloadRequest(imageURL, "wangsu_seedream_edit_reference")
	if err != nil {
		return wangsuMultipartImagePart{}, fmt.Errorf("download image url %d failed: %w", index+1, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return wangsuMultipartImagePart{}, fmt.Errorf("download image url %d failed: HTTP %d", index+1, resp.StatusCode)
	}
	maxBytes := wangsuMultipartMaxBytes()
	if resp.ContentLength > maxBytes {
		return wangsuMultipartImagePart{}, fmt.Errorf("download image url %d exceeds maximum size: %d > %d", index+1, resp.ContentLength, maxBytes)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return wangsuMultipartImagePart{}, fmt.Errorf("read image url %d failed: %w", index+1, err)
	}
	if int64(len(data)) > maxBytes {
		return wangsuMultipartImagePart{}, fmt.Errorf("download image url %d exceeds maximum size: %d > %d", index+1, len(data), maxBytes)
	}
	contentType := normalizeWangsuImageContentType(resp.Header.Get("Content-Type"), data)
	if !strings.HasPrefix(contentType, "image/") {
		return wangsuMultipartImagePart{}, fmt.Errorf("download image url %d has invalid content type: %s", index+1, contentType)
	}
	return wangsuMultipartImagePart{
		Filename:    wangsuMultipartFilename(imageURL, index, contentType),
		ContentType: contentType,
		Data:        data,
		SourceURL:   imageURL,
	}, nil
}

func readWangsuMultipartImagePart(fileHeader *multipart.FileHeader, index int) (wangsuMultipartImagePart, error) {
	if fileHeader == nil {
		return wangsuMultipartImagePart{}, fmt.Errorf("image file %d is nil", index+1)
	}
	maxBytes := wangsuMultipartMaxBytes()
	if fileHeader.Size > maxBytes {
		return wangsuMultipartImagePart{}, fmt.Errorf("image file %d exceeds maximum size: %d > %d", index+1, fileHeader.Size, maxBytes)
	}
	file, err := fileHeader.Open()
	if err != nil {
		return wangsuMultipartImagePart{}, fmt.Errorf("open image file %d: %w", index+1, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return wangsuMultipartImagePart{}, fmt.Errorf("read image file %d: %w", index+1, err)
	}
	if int64(len(data)) > maxBytes {
		return wangsuMultipartImagePart{}, fmt.Errorf("image file %d exceeds maximum size: %d > %d", index+1, len(data), maxBytes)
	}
	contentType, _, _ := mime.ParseMediaType(fileHeader.Header.Get("Content-Type"))
	contentType = normalizeWangsuImageContentType(contentType, data)
	if !strings.HasPrefix(contentType, "image/") {
		contentType = detectImageMimeType(fileHeader.Filename)
	}
	if !strings.HasPrefix(contentType, "image/") {
		return wangsuMultipartImagePart{}, fmt.Errorf("image file %d has invalid content type: %s", index+1, contentType)
	}
	return wangsuMultipartImagePart{
		Filename:    wangsuMultipartFilename(fileHeader.Filename, index, contentType),
		ContentType: contentType,
		Data:        data,
	}, nil
}

func wangsuMultipartMaxBytes() int64 {
	megabytes := appconstant.MaxFileDownloadMB
	if megabytes <= 0 {
		megabytes = 64
	}
	return int64(megabytes) * 1024 * 1024
}

func normalizeWangsuImageContentType(raw string, data []byte) string {
	contentType := strings.TrimSpace(raw)
	if parsed, _, err := mime.ParseMediaType(contentType); err == nil {
		contentType = parsed
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = strings.ToLower(http.DetectContentType(data))
	}
	return contentType
}

func wangsuMultipartFilename(raw string, index int, contentType string) string {
	filename := ""
	if parsed, err := url.Parse(raw); err == nil {
		filename = path.Base(parsed.Path)
	}
	if filename == "." || filename == "/" || filename == "" {
		filename = fmt.Sprintf("image-%d", index+1)
	}
	filename = sanitizeWangsuMultipartFilename(filename)
	if filepath.Ext(filename) == "" {
		filename += wangsuImageExtension(contentType)
	}
	return filename
}

func sanitizeWangsuMultipartFilename(filename string) string {
	filename = strings.TrimSpace(filename)
	filename = strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, filename)
	filename = strings.Trim(filename, ".-")
	if filename == "" {
		return "image"
	}
	return filename
}

func wangsuImageExtension(contentType string) string {
	if contentType == "image/jpeg" {
		return ".jpg"
	}
	if extensions, err := mime.ExtensionsByType(contentType); err == nil && len(extensions) > 0 {
		return extensions[0]
	}
	return ".png"
}

func validateWangsuOptimizePromptOptions(request dto.ImageRequest) error {
	raw, ok := request.Extra["optimize_prompt_options"]
	if !ok || !hasJSONValue(raw) {
		return nil
	}
	var options seedreamOptimizePromptOptions
	if err := decodeSeedreamJSONField("optimize_prompt_options", raw, &options); err != nil {
		return err
	}
	if options.Mode != "" && options.Mode != "standard" {
		return seedream5BadRequest("optimize_prompt_options.mode must be standard on Wangsu")
	}
	return nil
}

func resolveWangsuSeedreamImageCount(request dto.ImageRequest) (uint, error) {
	n := uint(1)
	if request.N != nil {
		if *request.N == 0 {
			return 0, seedream5BadRequest("n must be greater than 0")
		}
		n = *request.N
	}

	var sequentialMode string
	if raw, ok := request.Extra["sequential_image_generation"]; ok && hasJSONValue(raw) {
		if err := decodeSeedreamJSONField("sequential_image_generation", raw, &sequentialMode); err != nil {
			return 0, err
		}
		if sequentialMode != "auto" && sequentialMode != "disabled" {
			return 0, seedream5BadRequest("sequential_image_generation must be one of auto or disabled")
		}
	}

	var maxImages *int
	if raw, ok := request.Extra["sequential_image_generation_options"]; ok && hasJSONValue(raw) {
		var options seedreamSequentialImageGenerationOptions
		if err := decodeSeedreamJSONField("sequential_image_generation_options", raw, &options); err != nil {
			return 0, err
		}
		if options.MaxImages == nil || *options.MaxImages < 1 {
			return 0, seedream5BadRequest("sequential_image_generation_options.max_images must be greater than 0")
		}
		maxImages = options.MaxImages
	}

	switch sequentialMode {
	case "auto":
		if maxImages == nil {
			return 0, seedream5BadRequest("Wangsu sequential_image_generation=auto requires sequential_image_generation_options.max_images")
		}
		if request.N != nil && int(*request.N) != *maxImages {
			return 0, seedream5BadRequest("n conflicts with sequential_image_generation_options.max_images")
		}
		n = uint(*maxImages)
	case "disabled":
		if maxImages != nil {
			return 0, seedream5BadRequest("sequential_image_generation_options is only valid when sequential_image_generation is auto")
		}
		if n > 1 {
			return 0, seedream5BadRequest("n must be 1 when sequential_image_generation is disabled")
		}
	default:
		if maxImages != nil {
			return 0, seedream5BadRequest("sequential_image_generation_options requires sequential_image_generation=auto")
		}
	}

	return n, nil
}

func validateWangsuResponseFormat(responseFormat string) error {
	if responseFormat != "" && responseFormat != "url" && responseFormat != "b64_json" {
		return seedream5BadRequest("response_format must be one of url or b64_json")
	}
	return nil
}

func validateWangsuGenerationOutputOptions(request dto.ImageRequest) error {
	if hasJSONValue(request.OutputFormat) {
		var outputFormat string
		if err := decodeSeedreamJSONField("output_format", request.OutputFormat, &outputFormat); err != nil {
			return err
		}
		if outputFormat != "jpeg" {
			return seedream5BadRequest("output_format must be jpeg for Wangsu text-to-image requests")
		}
	}
	if request.Watermark != nil && *request.Watermark {
		return seedream5BadRequest("watermark=true is not supported by the Wangsu text-to-image API")
	}
	return nil
}

func rejectWangsuUnsupportedImageFields(request dto.ImageRequest, edits bool) error {
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
		{name: "input_fidelity", present: hasJSONValue(request.InputFidelity)},
		{name: "watermark_enabled", present: hasJSONValue(request.WatermarkEnabled)},
		{name: "user_id", present: hasJSONValue(request.UserId)},
	}
	if !edits {
		unsupported = append(unsupported,
			struct {
				name    string
				present bool
			}{name: "image", present: hasJSONValue(request.Image)},
			struct {
				name    string
				present bool
			}{name: "images", present: hasJSONValue(request.Images)},
			struct {
				name    string
				present bool
			}{name: "mask", present: hasJSONValue(request.Mask)},
		)
	}
	for _, field := range unsupported {
		if field.present {
			return seedream5BadRequest(fmt.Sprintf("%s is not supported by the Wangsu image API", field.name))
		}
	}
	return nil
}

func rejectUnknownWangsuSeedreamExtraFields(extra map[string]json.RawMessage, allowed map[string]struct{}, model string) error {
	for key := range extra {
		if _, ok := allowed[key]; !ok {
			return seedream5BadRequest(fmt.Sprintf("unsupported parameter %q for %s on Wangsu", key, model))
		}
	}
	return nil
}

func wangsuImageValuesFromRequest(request dto.ImageRequest) ([]string, error) {
	values, err := decodeWangsuImageValues(request.Image)
	if err != nil {
		return nil, err
	}
	more, err := decodeWangsuImageValues(request.Images)
	if err != nil {
		return nil, err
	}
	return append(values, more...), nil
}

func decodeWangsuImageValues(raw json.RawMessage) ([]string, error) {
	if !hasJSONValue(raw) {
		return nil, nil
	}
	trimmed := bytes.TrimSpace(raw)
	switch trimmed[0] {
	case '"':
		var value string
		if err := common.Unmarshal(trimmed, &value); err != nil {
			return nil, seedream5BadRequest("image must be a string or an array of strings")
		}
		value = cleanWangsuImageInput(value)
		if value == "" {
			return nil, seedream5BadRequest("image must not be empty")
		}
		return []string{value}, nil
	case '[':
		var values []string
		if err := common.Unmarshal(trimmed, &values); err != nil {
			return nil, seedream5BadRequest("image must be a string or an array of strings")
		}
		for index := range values {
			values[index] = cleanWangsuImageInput(values[index])
			if values[index] == "" {
				return nil, seedream5BadRequest("image entries must not be empty")
			}
		}
		return values, nil
	default:
		return nil, seedream5BadRequest("image must be a string or an array of strings")
	}
}

func wangsuMultipartInputs(c *gin.Context, imageValues *[]string) ([]*multipart.FileHeader, *multipart.FileHeader, error) {
	if c == nil || c.Request == nil || !strings.Contains(strings.ToLower(c.Request.Header.Get("Content-Type")), "multipart/form-data") {
		return nil, nil, nil
	}
	if c.Request.MultipartForm == nil {
		if _, err := c.MultipartForm(); err != nil {
			return nil, nil, seedream5BadRequest(fmt.Sprintf("failed to parse multipart image edit request: %v", err))
		}
	}
	form := c.Request.MultipartForm
	if form == nil {
		return nil, nil, seedream5BadRequest("multipart form is empty")
	}

	for key, values := range form.Value {
		if isWangsuImageField(key) {
			for _, value := range values {
				if cleaned := cleanWangsuImageInput(value); cleaned != "" {
					*imageValues = append(*imageValues, cleaned)
				}
			}
			continue
		}
		switch key {
		case "model", "prompt", "n", "size", "response_format", "output_format", "watermark":
		default:
			return nil, nil, seedream5BadRequest(fmt.Sprintf("unsupported multipart field %q for the Wangsu image edits API", key))
		}
	}

	var imageFiles []*multipart.FileHeader
	var maskFile *multipart.FileHeader
	keys := make([]string, 0, len(form.File))
	for key := range form.File {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		files := form.File[key]
		switch {
		case isWangsuImageField(key):
			imageFiles = append(imageFiles, files...)
		case key == "mask":
			if len(files) > 1 || maskFile != nil {
				return nil, nil, seedream5BadRequest("mask supports exactly one file")
			}
			if len(files) == 1 {
				maskFile = files[0]
			}
		default:
			return nil, nil, seedream5BadRequest(fmt.Sprintf("unsupported multipart file field %q for the Wangsu image edits API", key))
		}
	}
	return imageFiles, maskFile, nil
}

func isWangsuImageField(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return key == "image" || key == "images" || key == "image[]" || key == "images[]" ||
		strings.HasPrefix(key, "image[") || strings.HasPrefix(key, "images[")
}

func cleanWangsuImageInput(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "[") {
		if middle := strings.Index(value, "]("); middle > 0 && strings.HasSuffix(value, ")") {
			value = value[middle+2 : len(value)-1]
		}
	}
	if strings.HasPrefix(value, "<") && strings.HasSuffix(value, ">") {
		value = value[1 : len(value)-1]
	}
	return strings.TrimSpace(value)
}

func uniqueWangsuImageInputs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = cleanWangsuImageInput(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func uploadWangsuMultipartFile(c *gin.Context, fileHeader *multipart.FileHeader, fieldName, fieldPath string) (string, error) {
	if fileHeader == nil {
		return "", fmt.Errorf("%s is nil", fieldName)
	}
	file, err := fileHeader.Open()
	if err != nil {
		return "", fmt.Errorf("open %s: %w", fieldName, err)
	}
	defer file.Close()

	body, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", fieldName, err)
	}
	if len(body) == 0 {
		return "", seedream5BadRequest(fmt.Sprintf("%s must not be empty", fieldName))
	}

	contentType, _, _ := mime.ParseMediaType(fileHeader.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "image/") {
		contentType = detectImageMimeType(fileHeader.Filename)
	}
	return uploadWangsuImageBytes(c, body, contentType, fieldPath)
}

func normalizeWangsuImageInput(c *gin.Context, value, fieldPath string) (string, error) {
	value = cleanWangsuImageInput(value)
	if !strings.HasPrefix(strings.ToLower(value), "data:") {
		return value, nil
	}
	return "", seedream5BadRequest(fmt.Sprintf("%s Base64 image input is not supported; use a public URL or multipart file", fieldPath))
}

func uploadWangsuImageBytes(c *gin.Context, body []byte, contentType, fieldPath string) (string, error) {
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	url, err := uploadWangsuImageInputBytes(ctx, body, contentType)
	if err != nil {
		return "", fmt.Errorf("upload Wangsu Seedream %s to OSS: %w", fieldPath, err)
	}
	if strings.TrimSpace(url) == "" {
		return "", fmt.Errorf("upload Wangsu Seedream %s to OSS returned an empty URL", fieldPath)
	}
	return strings.TrimSpace(url), nil
}
