package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

const (
	defaultGeneratedMediaSyncUploadTimeoutSec = 120

	generatedMediaUploadURLModeBase64Only          = "base64_only"
	generatedMediaUploadURLModeDownloadAndReupload = "download_and_reupload"
	generatedMediaSourceResponseURLCompat          = "response_url"
)

type GeneratedMediaUploadParams struct {
	RequestID             string
	BillingID             string
	TaskID                string
	UserID                int
	Username              string
	TokenID               int
	TokenName             string
	UserGroup             string
	ChannelID             int
	ChannelName           string
	ModelName             string
	ServerRegion          string
	Path                  string
	ClientOutputUploadURL string
}

func GeneratedMediaReturnOSSURL() bool {
	return common.GetEnvOrDefaultBool("GENERATED_MEDIA_RETURN_OSS_URL", false)
}

func GeneratedMediaRequestContext(c *gin.Context) context.Context {
	// Media uploads must finish before we can return the rewritten OSS URL.
	// Do not inherit the client request context here: a browser/proxy timeout or
	// disconnect would otherwise cancel PutObject and turn a successful upstream
	// generation into a 502 response.
	return context.Background()
}

func GeneratedMediaUploadParamsFromRelay(c *gin.Context, info *relaycommon.RelayInfo) GeneratedMediaUploadParams {
	params := GeneratedMediaUploadParams{
		ServerRegion: common.GetServerRegion(),
	}
	if info != nil {
		params.RequestID = strings.TrimSpace(info.RequestId)
		params.UserID = info.UserId
		params.TokenID = info.TokenId
		params.UserGroup = strings.TrimSpace(info.UserGroup)
		params.ModelName = strings.TrimSpace(info.OriginModelName)
		params.Path = strings.TrimSpace(info.RequestURLPath)
		if info.ChannelMeta != nil {
			params.ChannelID = info.ChannelId
			if params.ModelName == "" {
				params.ModelName = strings.TrimSpace(info.UpstreamModelName)
			}
		}
	}
	if c != nil {
		if params.RequestID == "" {
			params.RequestID = strings.TrimSpace(common.GetContextKeyString(c, common.RequestIdKey))
		}
		params.BillingID = strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyBillingId))
		if params.BillingID == "" {
			params.BillingID = strings.TrimSpace(c.Writer.Header().Get(common.BillingIdKey))
		}
		if params.Username == "" {
			params.Username = strings.TrimSpace(c.GetString("username"))
		}
		if params.TokenName == "" {
			params.TokenName = strings.TrimSpace(c.GetString("token_name"))
		}
		if params.ChannelName == "" {
			params.ChannelName = strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyChannelName))
		}
		if params.Path == "" && c.Request != nil && c.Request.URL != nil {
			params.Path = c.Request.URL.String()
		}
	}
	if params.ChannelName == "" && params.ChannelID > 0 {
		if ch, err := model.CacheGetChannel(params.ChannelID); err == nil && ch != nil {
			params.ChannelName = strings.TrimSpace(ch.Name)
		}
	}
	if params.BillingID == "" {
		params.BillingID = params.RequestID
	}
	return params
}

func GeneratedMediaUploadParamsFromTask(task *model.Task) GeneratedMediaUploadParams {
	params := GeneratedMediaUploadParams{
		ServerRegion: common.GetServerRegion(),
	}
	if task == nil {
		return params
	}
	params.RequestID = strings.TrimSpace(task.Properties.CreateRequestID)
	params.BillingID = strings.TrimSpace(task.TaskID)
	params.TaskID = strings.TrimSpace(task.TaskID)
	params.UserID = task.UserId
	params.UserGroup = strings.TrimSpace(task.Group)
	params.ChannelID = task.ChannelId
	params.TokenID = task.PrivateData.TokenId
	if task.PrivateData.XAI != nil {
		params.ClientOutputUploadURL = strings.TrimSpace(task.PrivateData.XAI.OutputUploadURL)
	}
	params.ModelName = strings.TrimSpace(task.Properties.OriginModelName)
	if params.ModelName == "" && task.PrivateData.BillingContext != nil {
		params.ModelName = strings.TrimSpace(task.PrivateData.BillingContext.OriginModelName)
	}
	if params.ModelName == "" {
		params.ModelName = strings.TrimSpace(task.Properties.UpstreamModelName)
	}
	if params.UserID > 0 {
		params.Username, _ = model.GetUsernameById(params.UserID, false)
	}
	if params.TokenID > 0 {
		if token, err := model.GetTokenById(params.TokenID); err == nil && token != nil {
			params.TokenName = strings.TrimSpace(token.Name)
		}
	}
	if params.ChannelID > 0 {
		if ch, err := model.CacheGetChannel(params.ChannelID); err == nil && ch != nil {
			params.ChannelName = strings.TrimSpace(ch.Name)
		}
	}
	return params
}

func EnsureTaskVideoOSSURL(ctx context.Context, task *model.Task, taskResult *relaycommon.TaskInfo) (string, error) {
	ossURL, _, err := EnsureTaskVideoOSSResult(ctx, task, taskResult)
	return ossURL, err
}

func EnsureTaskVideoOSSResult(ctx context.Context, task *model.Task, taskResult *relaycommon.TaskInfo) (string, int64, error) {
	if task == nil {
		return "", 0, nil
	}
	if !GeneratedMediaReturnOSSURL() {
		return strings.TrimSpace(task.GetResultURL()), task.PrivateData.OssResultSizeBytes, nil
	}
	if task.Status != model.TaskStatusSuccess {
		return strings.TrimSpace(task.GetResultURL()), task.PrivateData.OssResultSizeBytes, nil
	}
	if ossURL := strings.TrimSpace(task.PrivateData.OssResultURL); ossURL != "" {
		sizeBytes := task.PrivateData.OssResultSizeBytes
		if sizeBytes <= 0 {
			sizeBytes = lookupGeneratedMediaSizeBytes(model.GeneratedMediaKindVideo, GeneratedMediaUploadParamsFromTask(task), task.PrivateData.OssResultKey, ossURL)
			if sizeBytes > 0 {
				task.PrivateData.OssResultSizeBytes = sizeBytes
				_ = model.UpdateTaskGeneratedMediaResult(task.TaskID, task.PrivateData.OssResultKey, ossURL, sizeBytes)
			}
		}
		return ossURL, sizeBytes, nil
	}
	if !GeneratedMediaEnabled() {
		return "", 0, errors.New("generated media OSS return is enabled but OSS is not configured")
	}
	rawURL := chooseTaskGeneratedMediaURL(task, taskResult)
	if rawURL == "" {
		return strings.TrimSpace(task.GetResultURL()), task.PrivateData.OssResultSizeBytes, nil
	}
	params := GeneratedMediaUploadParamsFromTask(task)
	if isGeneratedMediaReturnedOSSURL(rawURL) {
		sizeBytes := lookupGeneratedMediaSizeBytes(model.GeneratedMediaKindVideo, params, task.PrivateData.OssResultKey, rawURL)
		task.PrivateData.OssResultURL = rawURL
		if sizeBytes > 0 {
			task.PrivateData.OssResultSizeBytes = sizeBytes
		}
		_ = model.UpdateTaskGeneratedMediaResult(task.TaskID, task.PrivateData.OssResultKey, rawURL, sizeBytes)
		return rawURL, sizeBytes, nil
	}
	uploadCtx, cancel := context.WithTimeout(ctx, generatedMediaSyncUploadTimeout())
	defer cancel()

	ossURL, row, err := UploadGeneratedMediaURL(uploadCtx, params, rawURL, model.GeneratedMediaKindVideo, "$.task.result_url")
	if err != nil {
		return "", 0, err
	}
	ossURL = strings.TrimSpace(ossURL)
	if ossURL == "" {
		return "", 0, errors.New("generated media video OSS URL is empty")
	}
	ossKey := ""
	sizeBytes := int64(0)
	if row != nil {
		ossKey = strings.TrimSpace(row.OssKey)
		sizeBytes = row.SizeBytes
	}
	if sizeBytes <= 0 {
		sizeBytes = lookupGeneratedMediaSizeBytes(model.GeneratedMediaKindVideo, params, ossKey, ossURL)
	}
	task.PrivateData.OssResultURL = ossURL
	if ossKey != "" {
		task.PrivateData.OssResultKey = ossKey
	}
	if sizeBytes > 0 {
		task.PrivateData.OssResultSizeBytes = sizeBytes
	}
	_ = model.UpdateTaskGeneratedMediaResult(task.TaskID, ossKey, ossURL, sizeBytes)
	return ossURL, sizeBytes, nil
}

func RewriteImageResponseToOSS(ctx context.Context, params GeneratedMediaUploadParams, imageResponse *dto.ImageResponse) (bool, error) {
	if imageResponse == nil {
		return false, nil
	}
	body, err := common.Marshal(imageResponse)
	if err != nil {
		return false, err
	}
	body, changed, err := RewriteImageResponseBodyToOSS(ctx, params, body)
	if err != nil || !changed {
		return changed, err
	}
	if err = common.Unmarshal(body, imageResponse); err != nil {
		return false, err
	}
	return true, nil
}

func RewriteImageResponseBodyToOSS(ctx context.Context, params GeneratedMediaUploadParams, body []byte) ([]byte, bool, error) {
	if !GeneratedMediaReturnOSSURL() {
		return body, false, nil
	}
	if !GeneratedMediaEnabled() {
		return body, false, errors.New("generated media OSS return is enabled but OSS is not configured")
	}
	if len(body) == 0 || !json.Valid(body) {
		return body, false, nil
	}

	var root map[string]any
	if err := common.Unmarshal(body, &root); err != nil {
		return body, false, err
	}
	data, ok := root["data"]
	if !ok {
		return body, false, nil
	}

	uploadCtx, cancel := context.WithTimeout(ctx, generatedMediaSyncUploadTimeout())
	defer cancel()

	changed, err := rewriteImageDataNodeToOSS(uploadCtx, params, data, "$.data")
	if err != nil {
		return body, false, err
	}
	if !changed {
		return body, false, nil
	}
	rewritten, err := common.Marshal(root)
	if err != nil {
		return body, false, err
	}
	return rewritten, true, nil
}

func RewriteVideoResponseBodyToOSS(ctx context.Context, params GeneratedMediaUploadParams, body []byte) ([]byte, bool, error) {
	if !GeneratedMediaReturnOSSURL() {
		return body, false, nil
	}
	if !GeneratedMediaEnabled() {
		return body, false, errors.New("generated media OSS return is enabled but OSS is not configured")
	}
	if len(body) == 0 || !json.Valid(body) {
		return body, false, nil
	}

	var root any
	if err := common.Unmarshal(body, &root); err != nil {
		return body, false, err
	}

	uploadCtx, cancel := context.WithTimeout(ctx, generatedMediaSyncUploadTimeout())
	defer cancel()

	changed, err := rewriteVideoURLsNodeToOSS(uploadCtx, params, &root, "$")
	if err != nil {
		return body, false, err
	}
	if !changed {
		return body, false, nil
	}
	rewritten, err := common.Marshal(root)
	if err != nil {
		return body, false, err
	}
	return rewritten, true, nil
}

func RewriteGeneratedMediaMarkdownDataURLsToOSS(ctx context.Context, params GeneratedMediaUploadParams, body []byte) ([]byte, bool, error) {
	return RewriteGeneratedMediaDataURLsToOSS(ctx, params, body)
}

func RewriteGeneratedMediaDataURLsToOSS(ctx context.Context, params GeneratedMediaUploadParams, body []byte) ([]byte, bool, error) {
	return rewriteGeneratedMediaDataURLsToOSS(ctx, params, body, false)
}

func RewriteGeneratedMediaDataURLsToOSSRequired(ctx context.Context, params GeneratedMediaUploadParams, body []byte) ([]byte, bool, error) {
	return rewriteGeneratedMediaDataURLsToOSS(ctx, params, body, true)
}

func rewriteGeneratedMediaDataURLsToOSS(ctx context.Context, params GeneratedMediaUploadParams, body []byte, required bool) ([]byte, bool, error) {
	if !GeneratedMediaReturnOSSURL() {
		if !required {
			return body, false, nil
		}
	}
	if !required && !GeneratedMediaEnabled() {
		return body, false, errors.New("generated media OSS return is enabled but OSS is not configured")
	}
	if len(body) == 0 || !json.Valid(body) {
		return body, false, nil
	}

	var root any
	if err := common.Unmarshal(body, &root); err != nil {
		return body, false, err
	}

	uploadCtx, cancel := context.WithTimeout(ctx, generatedMediaSyncUploadTimeout())
	defer cancel()

	changed, err := rewriteGeneratedMediaDataURLsNodeToOSS(uploadCtx, params, &root, "$")
	if err != nil {
		return body, false, err
	}
	if !changed {
		return body, false, nil
	}
	rewritten, err := common.Marshal(root)
	if err != nil {
		return body, false, err
	}
	return rewritten, true, nil
}

func UploadGeneratedMediaBytes(ctx context.Context, params GeneratedMediaUploadParams, data []byte, contentType, mediaKind, fieldPath string) (string, *model.GeneratedMediaObject, error) {
	if len(data) == 0 {
		return "", nil, errors.New("generated media bytes is empty")
	}
	detectedContentType := normalizeContentType(contentType)
	if detectedContentType == "" || detectedContentType == "application/octet-stream" {
		detectedContentType = normalizeContentType(http.DetectContentType(data))
	}
	detectedKind := mediaKindFromContentType(detectedContentType, mediaKind)
	if detectedContentType == "application/octet-stream" && detectedKind == model.GeneratedMediaKindImage {
		detectedContentType = "image/png"
	}
	candidate := buildGeneratedMediaCandidate(data, detectedContentType, detectedKind, fieldPath)
	if candidate.MediaKind == "" {
		return "", nil, fmt.Errorf("unsupported generated media content type: %s", contentType)
	}
	localPath, err := writeGeneratedMediaSpool(candidate.MediaKind, candidate.ContentType, candidate.Sha256, candidate.Data)
	if err != nil {
		return "", nil, err
	}
	key := buildGeneratedMediaOSSKey(candidate.MediaKind, candidate.ContentType, candidate.Sha256)
	row, created, err := model.EnsureGeneratedMediaObjectPending(params.generatedMediaObjectParams(
		model.GeneratedMediaSourceResponseBase64,
		candidate.MediaKind,
		candidate.Sha256,
		"",
		localPath,
		fieldPath,
		candidate.ContentType,
		int64(len(candidate.Data)),
		candidate.Sha256,
		key,
	))
	if err != nil {
		return "", nil, err
	}
	if row == nil {
		return "", nil, errors.New("generated media row is empty")
	}
	if row.Status == model.GeneratedMediaStatusUploaded && strings.TrimSpace(row.OssURL) != "" {
		return strings.TrimSpace(row.OssURL), row, nil
	}
	if !created {
		_ = model.UpdateGeneratedMediaLocal(row.Id, localPath, candidate.ContentType, candidate.Sha256, int64(len(candidate.Data)))
	}
	row.LocalPath = localPath
	row.ContentType = candidate.ContentType
	row.Sha256 = candidate.Sha256
	row.SizeBytes = int64(len(candidate.Data))
	row.OssKey = key
	if err = uploadGeneratedMediaRow(ctx, row); err != nil {
		if isGeneratedMediaMissingLocalFileErr(err) {
			localPath, err = writeGeneratedMediaSpool(candidate.MediaKind, candidate.ContentType, candidate.Sha256, candidate.Data)
			if err == nil {
				row.LocalPath = localPath
				_ = model.UpdateGeneratedMediaLocal(row.Id, localPath, candidate.ContentType, candidate.Sha256, int64(len(candidate.Data)))
				err = uploadGeneratedMediaRow(ctx, row)
			}
		}
	}
	if err != nil {
		_ = model.MarkGeneratedMediaFailed(row.Id, truncateErr(err), row.RetryCount+1, generatedMediaBackoff(row.RetryCount+1))
		return "", row, err
	}
	return generatedMediaPublicURL(key), row, nil
}

func UploadGeneratedMediaURL(ctx context.Context, params GeneratedMediaUploadParams, rawURL, mediaKind, fieldPath string) (string, *model.GeneratedMediaObject, error) {
	rawURL = strings.TrimSpace(rawURL)
	if !isHTTPURL(rawURL) {
		return "", nil, fmt.Errorf("invalid generated media url")
	}
	if isGeneratedMediaReturnedOSSURL(rawURL) {
		return rawURL, nil, nil
	}
	sourceHash := common.Sha256Hex([]byte(rawURL))
	row, created, err := model.EnsureGeneratedMediaObjectPending(params.generatedMediaObjectParams(
		generatedMediaSourceResponseURLCompat,
		mediaKind,
		sourceHash,
		rawURL,
		"",
		fieldPath,
		"",
		0,
		"",
		"",
	))
	if err != nil {
		return "", nil, err
	}
	if row == nil {
		return "", nil, errors.New("generated media row is empty")
	}
	if row.Status == model.GeneratedMediaStatusUploaded && strings.TrimSpace(row.OssURL) != "" {
		return strings.TrimSpace(row.OssURL), row, nil
	}

	if created || strings.TrimSpace(row.LocalPath) == "" || !fileExists(row.LocalPath) {
		localPath, contentType, shaHex, sizeBytes, err := downloadGeneratedMediaToSpool(rawURL, mediaKind)
		if err != nil {
			_ = model.MarkGeneratedMediaFailed(row.Id, truncateErr(err), row.RetryCount+1, generatedMediaBackoff(row.RetryCount+1))
			return "", row, err
		}
		row.LocalPath = localPath
		row.ContentType = contentType
		row.Sha256 = shaHex
		row.SizeBytes = sizeBytes
		row.OssKey = buildGeneratedMediaOSSKey(mediaKind, contentType, shaHex)
		if err = model.UpdateGeneratedMediaLocal(row.Id, localPath, contentType, shaHex, sizeBytes); err != nil {
			return "", row, err
		}
	}

	if row.OssKey == "" {
		row.OssKey = buildGeneratedMediaOSSKey(mediaKind, row.ContentType, row.Sha256)
	}
	if err = uploadGeneratedMediaRow(ctx, row); err != nil {
		if isGeneratedMediaMissingLocalFileErr(err) {
			localPath, contentType, shaHex, sizeBytes, downloadErr := downloadGeneratedMediaToSpool(rawURL, mediaKind)
			if downloadErr == nil {
				row.LocalPath = localPath
				row.ContentType = contentType
				row.Sha256 = shaHex
				row.SizeBytes = sizeBytes
				row.OssKey = buildGeneratedMediaOSSKey(mediaKind, contentType, shaHex)
				if updateErr := model.UpdateGeneratedMediaLocal(row.Id, localPath, contentType, shaHex, sizeBytes); updateErr != nil {
					err = updateErr
				} else {
					err = uploadGeneratedMediaRow(ctx, row)
				}
			} else {
				err = downloadErr
			}
		}
	}
	if err != nil {
		_ = model.MarkGeneratedMediaFailed(row.Id, truncateErr(err), row.RetryCount+1, generatedMediaBackoff(row.RetryCount+1))
		return "", row, err
	}
	return generatedMediaPublicURL(row.OssKey), row, nil
}

func EnsureGeneratedMediaOSSURL(ctx context.Context, params GeneratedMediaUploadParams, candidate GeneratedMediaCandidate) (string, *model.GeneratedMediaObject, error) {
	return UploadGeneratedMediaBytes(ctx, params, candidate.Data, candidate.ContentType, candidate.MediaKind, candidate.FieldPath)
}

func rewriteVideoURLsNodeToOSS(ctx context.Context, params GeneratedMediaUploadParams, node *any, path string) (bool, error) {
	switch value := (*node).(type) {
	case map[string]any:
		changed := false
		for key, child := range value {
			childPath := path + "." + key
			if isGeneratedVideoURLField(key) {
				if raw, ok := child.(string); ok {
					rewritten, sizeBytes, err := rewriteVideoURLStringToOSS(ctx, params, raw, childPath)
					if err != nil {
						return false, err
					}
					itemChanged := false
					if rewritten != strings.TrimSpace(raw) {
						value[key] = rewritten
						itemChanged = true
					}
					if setGeneratedMediaSizeBytes(value, sizeBytes) {
						itemChanged = true
					}
					if itemChanged {
						changed = true
						continue
					}
				}
			}
			childAny := child
			childChanged, err := rewriteVideoURLsNodeToOSS(ctx, params, &childAny, childPath)
			if err != nil {
				return false, err
			}
			if childChanged {
				value[key] = childAny
				changed = true
			}
		}
		return changed, nil
	case []any:
		changed := false
		for i, child := range value {
			childAny := child
			childChanged, err := rewriteVideoURLsNodeToOSS(ctx, params, &childAny, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return false, err
			}
			if childChanged {
				value[i] = childAny
				changed = true
			}
		}
		return changed, nil
	default:
		return false, nil
	}
}

func rewriteVideoURLStringToOSS(ctx context.Context, params GeneratedMediaUploadParams, rawURL, path string) (string, int64, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return rawURL, 0, nil
	}
	if params.ClientOutputUploadURL != "" && rawURL == params.ClientOutputUploadURL {
		return rawURL, 0, nil
	}
	if isGeneratedMediaReturnedOSSURL(rawURL) {
		return rawURL, lookupGeneratedMediaSizeBytes(model.GeneratedMediaKindVideo, params, "", rawURL), nil
	}
	if strings.HasPrefix(strings.ToLower(rawURL), "data:") {
		candidate, ok := decodeGeneratedMediaString("url", path, rawURL)
		if !ok || candidate.MediaKind != model.GeneratedMediaKindVideo {
			return rawURL, 0, nil
		}
		ossURL, row, err := UploadGeneratedMediaBytes(ctx, params, candidate.Data, candidate.ContentType, candidate.MediaKind, candidate.FieldPath)
		if err != nil {
			return rawURL, 0, err
		}
		return ossURL, generatedMediaSizeBytes(row, params, model.GeneratedMediaKindVideo, "", ossURL, int64(len(candidate.Data))), nil
	}
	if !isHTTPURL(rawURL) || isTaskProxyVideoContentURL(rawURL) {
		return rawURL, 0, nil
	}
	ossURL, row, err := UploadGeneratedMediaURL(ctx, params, rawURL, model.GeneratedMediaKindVideo, path)
	if err != nil {
		return rawURL, 0, err
	}
	return ossURL, generatedMediaSizeBytes(row, params, model.GeneratedMediaKindVideo, "", ossURL, 0), nil
}

func isGeneratedVideoURLField(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "url", "result_url", "video_url", "file_url", "download_url", "remote_url":
		return true
	default:
		return false
	}
}

func isTaskProxyVideoContentURL(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	return strings.Contains(rawURL, "/v1/videos/") && strings.Contains(rawURL, "/content")
}

func setGeneratedMediaSizeBytes(item map[string]any, sizeBytes int64) bool {
	if item == nil || sizeBytes <= 0 {
		return false
	}
	if existing, ok := item["size_bytes"]; ok {
		switch v := existing.(type) {
		case int64:
			if v == sizeBytes {
				return false
			}
		case int:
			if int64(v) == sizeBytes {
				return false
			}
		case float64:
			if int64(v) == sizeBytes {
				return false
			}
		}
	}
	item["size_bytes"] = sizeBytes
	return true
}

func generatedMediaSizeBytes(row *model.GeneratedMediaObject, params GeneratedMediaUploadParams, mediaKind, ossKey, ossURL string, fallback int64) int64 {
	if row != nil {
		if row.SizeBytes > 0 {
			return row.SizeBytes
		}
		if ossKey == "" {
			ossKey = strings.TrimSpace(row.OssKey)
		}
		if ossURL == "" {
			ossURL = strings.TrimSpace(row.OssURL)
		}
	}
	if sizeBytes := lookupGeneratedMediaSizeBytes(mediaKind, params, ossKey, ossURL); sizeBytes > 0 {
		return sizeBytes
	}
	if fallback > 0 {
		return fallback
	}
	return 0
}

func lookupGeneratedMediaSizeBytes(mediaKind string, params GeneratedMediaUploadParams, ossKey, ossURL string) int64 {
	sizeBytes, err := model.GetGeneratedMediaSizeBytesByOSSRef(mediaKind, params.TaskID, ossKey, ossURL)
	if err != nil {
		return 0
	}
	return sizeBytes
}

func rewriteGeneratedMediaDataURLsNodeToOSS(ctx context.Context, params GeneratedMediaUploadParams, node *any, path string) (bool, error) {
	switch value := (*node).(type) {
	case map[string]any:
		changed := false
		imageURLChanged, err := rewriteGeneratedMediaImageURLMapToOSS(ctx, params, value, path)
		if err != nil {
			return false, err
		}
		changed = changed || imageURLChanged
		inlineChanged, err := rewriteGeneratedMediaInlineDataMapToOSS(ctx, params, value, path)
		if err != nil {
			return false, err
		}
		changed = changed || inlineChanged
		for key, child := range value {
			childAny := child
			childChanged, err := rewriteGeneratedMediaDataURLsNodeToOSS(ctx, params, &childAny, path+"."+key)
			if err != nil {
				return false, err
			}
			if childChanged {
				value[key] = childAny
				changed = true
			}
		}
		return changed, nil
	case []any:
		changed := false
		for i, child := range value {
			childAny := child
			childChanged, err := rewriteGeneratedMediaDataURLsNodeToOSS(ctx, params, &childAny, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return false, err
			}
			if childChanged {
				value[i] = childAny
				changed = true
			}
		}
		return changed, nil
	case string:
		rewritten, changed, err := rewriteGeneratedMediaDataURLsStringToOSS(ctx, params, value, path)
		if err != nil || !changed {
			return changed, err
		}
		*node = rewritten
		return true, nil
	default:
		return false, nil
	}
}

func rewriteGeneratedMediaDataURLsStringToOSS(ctx context.Context, params GeneratedMediaUploadParams, text, path string) (string, bool, error) {
	return rewriteGeneratedMediaDataURLsString(text, path, func(candidate GeneratedMediaCandidate) (string, error) {
		ossURL, _, err := UploadGeneratedMediaBytes(ctx, params, candidate.Data, candidate.ContentType, candidate.MediaKind, candidate.FieldPath)
		return ossURL, err
	})
}

func rewriteGeneratedMediaDataURLsString(text, path string, upload func(GeneratedMediaCandidate) (string, error)) (string, bool, error) {
	if indexGeneratedMediaDataURL(text, 0) < 0 {
		return text, false, nil
	}
	var out strings.Builder
	pos := 0
	changed := false
	dataURLIndex := 0
	for {
		idx := indexGeneratedMediaDataURL(text, pos)
		if idx < 0 {
			out.WriteString(text[pos:])
			break
		}
		end := generatedMediaDataURLEnd(text, idx)
		if end <= idx {
			out.WriteString(text[pos : idx+len("data:")])
			pos = idx + len("data:")
			continue
		}
		rawDataURL := text[idx:end]
		fieldPath := fmt.Sprintf("%s.data_url[%d]", path, dataURLIndex)
		dataURLIndex++
		candidate, ok := decodeGeneratedMediaString("url", fieldPath, rawDataURL)
		if !ok {
			out.WriteString(text[pos : idx+len("data:")])
			pos = idx + len("data:")
			continue
		}
		ossURL, err := upload(candidate)
		if err != nil {
			return text, false, err
		}
		out.WriteString(text[pos:idx])
		out.WriteString(ossURL)
		pos = end
		changed = true
	}
	if !changed {
		return text, false, nil
	}
	return out.String(), true, nil
}

func rewriteGeneratedMediaImageURLMapToOSS(ctx context.Context, params GeneratedMediaUploadParams, item map[string]any, path string) (bool, error) {
	for key, value := range item {
		if !strings.EqualFold(key, "image_url") {
			continue
		}
		switch imageURL := value.(type) {
		case map[string]any:
			changed, err := rewriteImageDataMapToOSS(ctx, params, imageURL, path+"."+key)
			if err != nil {
				return false, err
			}
			if changed {
				item[key] = imageURL
			}
			return changed, nil
		case string:
			rawURL := strings.TrimSpace(imageURL)
			if rawURL == "" {
				return false, nil
			}
			imageURLMap := map[string]any{
				"url": rawURL,
			}
			changed, err := rewriteImageDataMapToOSS(ctx, params, imageURLMap, path+"."+key)
			if err != nil {
				return false, err
			}
			if changed {
				item[key] = imageURLMap
			}
			return changed, nil
		default:
			return false, nil
		}
	}
	return false, nil
}

func indexGeneratedMediaDataURL(text string, offset int) int {
	if offset < 0 {
		offset = 0
	}
	for i := offset; i+len("data:") <= len(text); i++ {
		if text[i] != 'd' && text[i] != 'D' {
			continue
		}
		if strings.EqualFold(text[i:i+len("data:")], "data:") {
			return i
		}
	}
	return -1
}

func rewriteGeneratedMediaInlineDataMapToOSS(ctx context.Context, params GeneratedMediaUploadParams, item map[string]any, path string) (bool, error) {
	for _, inlineKey := range []string{"inlineData", "inline_data"} {
		rawInline, ok := item[inlineKey]
		if !ok {
			continue
		}
		inlineMap, ok := rawInline.(map[string]any)
		if !ok {
			continue
		}
		data, ok := stringValueFromMap(inlineMap, "data")
		if !ok || strings.TrimSpace(data) == "" {
			continue
		}
		contentType, ok := stringValueFromMap(inlineMap, "mimeType")
		if !ok || strings.TrimSpace(contentType) == "" {
			contentType, _ = stringValueFromMap(inlineMap, "mime_type")
		}
		contentType = normalizeContentType(contentType)
		mediaKind := mediaKindFromContentType(contentType, "")
		if mediaKind == "" {
			continue
		}
		decoded, ok := decodeBase64WithLimit(data)
		if !ok {
			return false, fmt.Errorf("invalid generated media inline data at %s.%s.data", path, inlineKey)
		}
		candidate := buildGeneratedMediaCandidate(decoded, contentType, mediaKind, path+"."+inlineKey+".data")
		ossURL, _, err := UploadGeneratedMediaBytes(ctx, params, candidate.Data, candidate.ContentType, candidate.MediaKind, candidate.FieldPath)
		if err != nil {
			return false, err
		}
		if inlineKey == "inline_data" {
			item["file_data"] = map[string]any{
				"mime_type": candidate.ContentType,
				"file_uri":  ossURL,
			}
		} else {
			item["fileData"] = map[string]any{
				"mimeType": candidate.ContentType,
				"fileUri":  ossURL,
			}
		}
		delete(item, inlineKey)
		return true, nil
	}
	return false, nil
}

func generatedMediaDataURLEnd(text string, start int) int {
	commaRel := strings.Index(text[start:], ",")
	if commaRel < 0 {
		return -1
	}
	comma := start + commaRel
	meta := text[start+len("data:") : comma]
	if !strings.Contains(strings.ToLower(meta), ";base64") {
		return -1
	}
	end := comma + 1
	for end < len(text) && isDataURLBase64Byte(text[end]) {
		end++
	}
	return end
}

func isDataURLBase64Byte(ch byte) bool {
	return ch >= 'a' && ch <= 'z' ||
		ch >= 'A' && ch <= 'Z' ||
		ch >= '0' && ch <= '9' ||
		ch == '+' || ch == '/' || ch == '=' ||
		ch == '-' || ch == '_'
}

func rewriteImageDataNodeToOSS(ctx context.Context, params GeneratedMediaUploadParams, node any, path string) (bool, error) {
	switch value := node.(type) {
	case []any:
		changed := false
		for i, item := range value {
			itemChanged, err := rewriteImageDataNodeToOSS(ctx, params, item, fmt.Sprintf("%s[%d]", path, i))
			if err != nil {
				return false, err
			}
			changed = changed || itemChanged
		}
		return changed, nil
	case map[string]any:
		return rewriteImageDataMapToOSS(ctx, params, value, path)
	default:
		return false, nil
	}
}

func rewriteImageDataMapToOSS(ctx context.Context, params GeneratedMediaUploadParams, item map[string]any, path string) (bool, error) {
	for key, value := range item {
		if !isGeneratedMediaBase64Field(key) {
			continue
		}
		raw, ok := value.(string)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		candidate, ok := decodeGeneratedMediaString(key, path+"."+key, raw)
		if !ok {
			return false, fmt.Errorf("invalid generated image base64 at %s.%s", path, key)
		}
		ossURL, row, err := UploadGeneratedMediaBytes(ctx, params, candidate.Data, candidate.ContentType, candidate.MediaKind, candidate.FieldPath)
		if err != nil {
			return false, err
		}
		item["url"] = ossURL
		setGeneratedMediaSizeBytes(item, generatedMediaSizeBytes(row, params, model.GeneratedMediaKindImage, "", ossURL, int64(len(candidate.Data))))
		deleteGeneratedMediaBase64Fields(item)
		return true, nil
	}

	rawURL, ok := stringValueFromMap(item, "url")
	if !ok || strings.TrimSpace(rawURL) == "" {
		return false, nil
	}
	lowerURL := strings.ToLower(strings.TrimSpace(rawURL))
	if strings.HasPrefix(lowerURL, "data:") {
		candidate, ok := decodeGeneratedMediaString("url", path+".url", rawURL)
		if !ok {
			return false, fmt.Errorf("invalid generated image data url at %s.url", path)
		}
		ossURL, row, err := UploadGeneratedMediaBytes(ctx, params, candidate.Data, candidate.ContentType, candidate.MediaKind, candidate.FieldPath)
		if err != nil {
			return false, err
		}
		item["url"] = ossURL
		setGeneratedMediaSizeBytes(item, generatedMediaSizeBytes(row, params, model.GeneratedMediaKindImage, "", ossURL, int64(len(candidate.Data))))
		return true, nil
	}
	if !generatedMediaShouldUploadResponseURL() || !isHTTPURL(rawURL) {
		return false, nil
	}
	ossURL, row, err := UploadGeneratedMediaURL(ctx, params, rawURL, model.GeneratedMediaKindImage, path+".url")
	if err != nil {
		return false, err
	}
	item["url"] = ossURL
	sizeChanged := setGeneratedMediaSizeBytes(item, generatedMediaSizeBytes(row, params, model.GeneratedMediaKindImage, "", ossURL, 0))
	return ossURL != rawURL || sizeChanged, nil
}

func (params GeneratedMediaUploadParams) generatedMediaObjectParams(sourceType, mediaKind, sourceHash, sourceURL, localPath, fieldPath, contentType string, sizeBytes int64, shaHex, ossKey string) model.GeneratedMediaObjectParams {
	other := map[string]any{
		"source": sourceType,
		"field":  fieldPath,
	}
	if params.Path != "" {
		other["path"] = params.Path
	}
	return model.GeneratedMediaObjectParams{
		SourceType:   sourceType,
		MediaKind:    mediaKind,
		SourceHash:   sourceHash,
		SourceURL:    sourceURL,
		LocalPath:    localPath,
		RequestId:    params.RequestID,
		BillingID:    params.BillingID,
		TaskID:       params.TaskID,
		UserID:       params.UserID,
		Username:     params.Username,
		TokenID:      params.TokenID,
		TokenName:    params.TokenName,
		UserGroup:    params.UserGroup,
		ChannelID:    params.ChannelID,
		ChannelName:  params.ChannelName,
		ModelName:    params.ModelName,
		ServerRegion: params.ServerRegion,
		ContentType:  contentType,
		SizeBytes:    sizeBytes,
		Sha256:       shaHex,
		OssKey:       ossKey,
		Other:        common.MapToJsonStr(other),
	}
}

func generatedMediaSyncUploadTimeout() time.Duration {
	sec := common.GetEnvOrDefault("GENERATED_MEDIA_SYNC_UPLOAD_TIMEOUT_SEC", defaultGeneratedMediaSyncUploadTimeoutSec)
	if sec <= 0 {
		sec = defaultGeneratedMediaSyncUploadTimeoutSec
	}
	return time.Duration(sec) * time.Second
}

func generatedMediaShouldUploadResponseURL() bool {
	mode := strings.ToLower(strings.TrimSpace(osEnvOrDefault("GENERATED_MEDIA_UPLOAD_URL_MODE", generatedMediaUploadURLModeDownloadAndReupload)))
	switch mode {
	case "", generatedMediaUploadURLModeDownloadAndReupload, "download", "reupload", "true", "1":
		return true
	case generatedMediaUploadURLModeBase64Only, "base64", "false", "0", "off", "passthrough", "pass_through":
		return false
	default:
		return true
	}
}

func stringValueFromMap(m map[string]any, key string) (string, bool) {
	for k, value := range m {
		if !strings.EqualFold(k, key) {
			continue
		}
		s, ok := value.(string)
		return s, ok
	}
	return "", false
}

func deleteGeneratedMediaBase64Fields(m map[string]any) {
	for key := range m {
		if isGeneratedMediaBase64Field(key) {
			delete(m, key)
		}
	}
}

func isGeneratedMediaReturnedOSSURL(rawURL string) bool {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return false
	}
	for _, base := range []string{
		strings.TrimSpace(osEnvOrDefault("GENERATED_MEDIA_PUBLIC_BASE_URL", "")),
		strings.TrimSpace(osEnvOrDefault("AUDIT_OSS_PUBLIC_BASE_URL", "")),
	} {
		if base == "" {
			continue
		}
		if strings.HasPrefix(rawURL, strings.TrimRight(base, "/")+"/") {
			return true
		}
	}
	return false
}

func osEnvOrDefault(key, fallback string) string {
	value := strings.TrimSpace(common.GetEnvOrDefaultString(key, fallback))
	if value == "" {
		return fallback
	}
	return value
}
