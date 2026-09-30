package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

type upstreamResponseLogSnapshot struct {
	row    model.UpstreamResponseLog
	params GeneratedMediaUploadParams
	body   []byte
}

type upstreamUsageSummary struct {
	UsageJSON        string
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
}

type UpstreamResponseRetryMetadata struct {
	RetryCount           int
	MaxRetries           int
	RetryScope           string
	RetryReason          string
	ResourceParam        string
	FailedResourceParams []string
	FinalAttempt         bool
}

type upstreamResponseCaptureReadCloser struct {
	io.Reader
	io.Closer
}

type UpstreamResponseBodyCapture struct {
	body bytes.Buffer
}

func StartUpstreamResponseBodyCapture(resp *http.Response) *UpstreamResponseBodyCapture {
	if !common.UpstreamResponseLogEnabled || resp == nil || resp.Body == nil {
		return nil
	}

	originalBody := resp.Body
	capture := &UpstreamResponseBodyCapture{}
	resp.Body = &upstreamResponseCaptureReadCloser{
		Reader: io.TeeReader(originalBody, &capture.body),
		Closer: originalBody,
	}
	return capture
}

func (capture *UpstreamResponseBodyCapture) Record(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) {
	if capture == nil {
		return
	}
	RecordUpstreamResponseLog(c, info, resp, capture.body.Bytes())
}

func RecordUpstreamResponseLog(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response, body []byte) {
	if !common.UpstreamResponseLogEnabled || len(body) == 0 {
		return
	}
	snapshot := buildUpstreamResponseLogSnapshot(c, info, resp, body)
	go writeUpstreamResponseLog(snapshot)
}

// RecordUpstreamResponseLogSync preserves ordering when one client request
// produces multiple upstream responses that must be queried by latest id.
func RecordUpstreamResponseLogSync(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response, body []byte) {
	if !common.UpstreamResponseLogEnabled || len(body) == 0 {
		return
	}
	snapshot := buildUpstreamResponseLogSnapshot(c, info, resp, body)
	writeUpstreamResponseLog(snapshot)
}

// RecordUpstreamResponseLogAttemptSync records ordered responses from one
// client request when a provider-specific retry policy performs multiple calls.
func RecordUpstreamResponseLogAttemptSync(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response, body []byte, metadata UpstreamResponseRetryMetadata) {
	if !common.UpstreamResponseLogEnabled || len(body) == 0 {
		return
	}
	if metadata.RetryCount < 0 {
		metadata.RetryCount = 0
	}
	snapshot := buildUpstreamResponseLogSnapshot(c, info, resp, body)
	applyUpstreamResponseRetryMetadata(&snapshot, metadata)
	writeUpstreamResponseLog(snapshot)
}

func applyUpstreamResponseRetryMetadata(snapshot *upstreamResponseLogSnapshot, metadata UpstreamResponseRetryMetadata) {
	if snapshot == nil {
		return
	}
	if metadata.RetryCount < 0 {
		metadata.RetryCount = 0
	}
	snapshot.row.RetryCount = metadata.RetryCount
	other := map[string]interface{}{}
	if snapshot.row.Other != "" {
		if existing, err := common.StrToMap(snapshot.row.Other); err == nil && existing != nil {
			other = existing
		}
	}
	other["attempt"] = metadata.RetryCount + 1
	other["retry_count"] = metadata.RetryCount
	other["max_retries"] = metadata.MaxRetries
	if retryScope := strings.TrimSpace(metadata.RetryScope); retryScope != "" {
		other["retry_scope"] = retryScope
	}
	other["retry_reason"] = strings.TrimSpace(metadata.RetryReason)
	other["final_attempt"] = metadata.FinalAttempt
	if resourceParam := strings.TrimSpace(metadata.ResourceParam); resourceParam != "" {
		other["resource_param"] = resourceParam
	}
	if len(metadata.FailedResourceParams) > 0 {
		failedParams := make([]string, 0, len(metadata.FailedResourceParams))
		for _, resourceParam := range metadata.FailedResourceParams {
			if resourceParam = strings.TrimSpace(resourceParam); resourceParam != "" {
				failedParams = append(failedParams, resourceParam)
			}
		}
		if len(failedParams) > 0 {
			other["failed_resource_params"] = failedParams
		}
	}
	snapshot.row.Other = common.MapToJsonStr(other)
}

func recordTaskUpstreamResponseLog(task *model.Task, ch *model.Channel, resp *http.Response, body []byte) {
	if !common.UpstreamResponseLogEnabled || task == nil || len(body) == 0 {
		return
	}
	snapshot := buildTaskUpstreamResponseLogSnapshot(task, ch, resp, body)
	go writeUpstreamResponseLog(snapshot)
}

func buildTaskUpstreamResponseLogSnapshot(task *model.Task, ch *model.Channel, resp *http.Response, body []byte) upstreamResponseLogSnapshot {
	params := GeneratedMediaUploadParamsFromTask(task)
	if params.Path == "" {
		params.Path = "/v1/videos/" + strings.TrimSpace(task.TaskID)
	}

	row := model.UpstreamResponseLog{
		CreatedAt:         common.GetTimestamp(),
		RequestId:         strings.TrimSpace(task.Properties.CreateRequestID),
		BillingID:         strings.TrimSpace(task.TaskID),
		TaskID:            strings.TrimSpace(task.TaskID),
		UserID:            task.UserId,
		Username:          params.Username,
		TokenID:           task.PrivateData.TokenId,
		TokenName:         params.TokenName,
		UserGroup:         strings.TrimSpace(task.Group),
		ModelName:         params.ModelName,
		UpstreamModelName: strings.TrimSpace(task.Properties.UpstreamModelName),
		ServerRegion:      common.GetServerRegion(),
		ChannelID:         task.ChannelId,
		ChannelName:       params.ChannelName,
		Method:            http.MethodGet,
		Path:              params.Path,
		BodySize:          int64(len(body)),
		BodySha256:        common.Sha256Hex(body),
	}
	if row.RequestId == "" {
		row.RequestId = row.BillingID
	}
	if row.ModelName == "" && task.PrivateData.BillingContext != nil {
		row.ModelName = strings.TrimSpace(task.PrivateData.BillingContext.OriginModelName)
	}
	if row.ModelName == "" {
		row.ModelName = row.UpstreamModelName
	}
	if ch != nil {
		row.ChannelType = ch.Type
		if row.ChannelID == 0 {
			row.ChannelID = ch.Id
		}
		if row.ChannelName == "" {
			row.ChannelName = strings.TrimSpace(ch.Name)
		}
	}
	if resp != nil {
		row.StatusCode = resp.StatusCode
		row.ContentType = resp.Header.Get("Content-Type")
	}

	snapshot := upstreamResponseLogSnapshot{
		row:    row,
		params: params,
		body:   append([]byte(nil), body...),
	}
	return snapshot
}

func buildUpstreamResponseLogSnapshot(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response, body []byte) upstreamResponseLogSnapshot {
	now := common.GetTimestamp()
	row := model.UpstreamResponseLog{
		CreatedAt:    now,
		BodySize:     int64(len(body)),
		BodySha256:   common.Sha256Hex(body),
		ServerRegion: common.GetServerRegion(),
	}

	params := GeneratedMediaUploadParamsFromRelay(c, info)
	row.RequestId = strings.TrimSpace(params.RequestID)
	row.BillingID = strings.TrimSpace(params.BillingID)
	row.TaskID = strings.TrimSpace(params.TaskID)
	row.UserID = params.UserID
	row.Username = params.Username
	row.TokenID = params.TokenID
	row.TokenName = params.TokenName
	row.UserGroup = params.UserGroup
	row.ChannelID = params.ChannelID
	row.ChannelName = params.ChannelName
	row.ModelName = params.ModelName

	if info != nil {
		row.UpstreamModelName = strings.TrimSpace(info.UpstreamModelName)
		row.RelayMode = info.RelayMode
		if info.ChannelMeta != nil {
			row.ChannelType = info.ChannelType
			if row.ChannelID == 0 {
				row.ChannelID = info.ChannelId
			}
			if row.UpstreamModelName == "" {
				row.UpstreamModelName = strings.TrimSpace(info.ChannelMeta.UpstreamModelName)
			}
		}
	}

	if c != nil {
		if row.RequestId == "" {
			row.RequestId = strings.TrimSpace(c.GetString(common.RequestIdKey))
		}
		if row.BillingID == "" {
			row.BillingID = strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyBillingId))
		}
		if row.BillingID == "" {
			row.BillingID = strings.TrimSpace(c.Writer.Header().Get(common.BillingIdKey))
		}
		if row.UserID == 0 {
			row.UserID = c.GetInt("id")
		}
		if row.Username == "" {
			row.Username = strings.TrimSpace(c.GetString("username"))
		}
		if row.TokenID == 0 {
			row.TokenID = c.GetInt("token_id")
		}
		if row.TokenName == "" {
			row.TokenName = strings.TrimSpace(c.GetString("token_name"))
		}
		if row.UserGroup == "" {
			row.UserGroup = strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyUserGroup))
		}
		if row.ModelName == "" {
			row.ModelName = strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyOriginalModel))
		}
		if row.ChannelID == 0 {
			row.ChannelID = common.GetContextKeyInt(c, constant.ContextKeyChannelId)
		}
		if row.ChannelName == "" {
			row.ChannelName = strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyChannelName))
		}
		if row.ChannelType == 0 {
			row.ChannelType = common.GetContextKeyInt(c, constant.ContextKeyChannelType)
		}
		if row.RelayMode == 0 {
			row.RelayMode = c.GetInt("relay_mode")
		}
		if c.Request != nil {
			row.Method = c.Request.Method
			if c.Request.URL != nil {
				row.Path = c.Request.URL.Path
			}
		}
	}

	if row.BillingID == "" {
		row.BillingID = row.RequestId
	}
	if row.TaskID == "" && strings.HasPrefix(row.BillingID, "task_") {
		row.TaskID = row.BillingID
	}
	if params.BillingID == "" {
		params.BillingID = row.BillingID
	}
	if params.TaskID == "" {
		params.TaskID = row.TaskID
	}
	if params.RequestID == "" {
		params.RequestID = row.RequestId
	}
	if params.ModelName == "" {
		params.ModelName = row.ModelName
	}
	if params.Path == "" {
		params.Path = row.Path
	}

	if resp != nil {
		row.StatusCode = resp.StatusCode
		row.ContentType = resp.Header.Get("Content-Type")
	}
	row.Other = appendGroupFallbackUpstreamOther(row.Other, c, info, row.ChannelID, row.ChannelName)

	snapshot := upstreamResponseLogSnapshot{
		row:    row,
		params: params,
		body:   append([]byte(nil), body...),
	}
	if info != nil && info.ChannelRetryActive {
		applySameChannelRetryLogMetadata(&snapshot, info)
	}
	return snapshot
}

func applySameChannelRetryLogMetadata(snapshot *upstreamResponseLogSnapshot, info *relaycommon.RelayInfo) {
	if snapshot == nil || info == nil || !info.ChannelRetryActive {
		return
	}
	retryCount := info.ChannelRetryIndex
	if retryCount < 0 {
		retryCount = 0
	}
	snapshot.row.RetryCount = retryCount
	other := map[string]interface{}{}
	if snapshot.row.Other != "" {
		if existing, err := common.StrToMap(snapshot.row.Other); err == nil && existing != nil {
			other = existing
		}
	}
	other["attempt"] = retryCount + 1
	other["retry_count"] = retryCount
	other["max_retries"] = info.ChannelRetryMax
	other["retry_scope"] = "same_channel"
	if snapshot.row.StatusCode >= http.StatusMultipleChoices {
		other["retry_reason"] = fmt.Sprintf("http_status:%d", snapshot.row.StatusCode)
	}
	snapshot.row.Other = common.MapToJsonStr(other)
}

func appendGroupFallbackUpstreamOther(existing string, c *gin.Context, info *relaycommon.RelayInfo, channelId int, channelName string) string {
	metadata, ok := GetGroupFallbackResponseMetadata(c)
	if !ok && info != nil && info.GroupFallbackApplied {
		sourceGroup := strings.TrimSpace(info.TokenGroup)
		if sourceGroup != "" {
			metadata = GroupFallbackResponseMetadata{
				Enabled:     true,
				SourceGroup: sourceGroup,
				TargetGroup: strings.TrimSpace(info.UsingGroup),
			}
			ok = true
		}
	}
	if !ok {
		return existing
	}
	if metadata.ChannelId == 0 {
		metadata.ChannelId = channelId
	}
	if metadata.ChannelName == "" {
		metadata.ChannelName = strings.TrimSpace(channelName)
	}

	other := map[string]interface{}{}
	if existing != "" {
		if current, err := common.StrToMap(existing); err == nil && current != nil {
			other = current
		}
	}
	other["group_fallback"] = metadata
	return common.MapToJsonStr(other)
}

func writeUpstreamResponseLog(snapshot upstreamResponseLogSnapshot) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError("panic in upstream response log writer (ignored)")
		}
	}()

	body, mediaReplaced, rewriteErr := sanitizeUpstreamResponseBody(context.Background(), snapshot.params, snapshot.body)
	usage := extractUpstreamUsage(snapshot.body)
	if usage.UsageJSON == "" {
		usage = extractUpstreamUsage(body)
	}

	maxBytes := common.UpstreamResponseLogMaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = 2 * 1024 * 1024
	}
	bodyTruncated := false
	if len(body) > maxBytes {
		body = body[:maxBytes]
		bodyTruncated = true
	}

	row := snapshot.row
	row.Body = string(body)
	row.SanitizedBodySize = int64(len(body))
	row.BodyTruncated = bodyTruncated
	row.MediaReplaced = mediaReplaced
	row.UsageJSON = usage.UsageJSON
	row.PromptTokens = usage.PromptTokens
	row.CompletionTokens = usage.CompletionTokens
	row.TotalTokens = usage.TotalTokens
	if rewriteErr != "" {
		row.Error = rewriteErr
	}

	if err := row.Insert(); err != nil {
		common.SysError("failed to insert upstream response log: " + err.Error())
	}
}

func sanitizeUpstreamResponseBody(ctx context.Context, params GeneratedMediaUploadParams, body []byte) ([]byte, bool, string) {
	working := append([]byte(nil), body...)
	mediaReplaced := false
	rewriteErr := ""

	if common.UpstreamResponseLogMediaToOSS && GeneratedMediaEnabled() && json.Valid(working) {
		rewritten, changed, err := rewriteUpstreamResponseMediaToOSS(ctx, params, working)
		if err != nil {
			rewriteErr = err.Error()
		} else if changed {
			working = rewritten
			mediaReplaced = true
		}
	}

	stripped, strippedChanged := common.StripInlineBase64(working)
	if strippedChanged {
		working = stripped
		mediaReplaced = true
	}
	working = common.RedactAuditText(working)
	return working, mediaReplaced, rewriteErr
}

func rewriteUpstreamResponseMediaToOSS(ctx context.Context, params GeneratedMediaUploadParams, body []byte) ([]byte, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var root any
	if err := common.Unmarshal(body, &root); err != nil {
		return body, false, err
	}

	uploadCtx, cancel := context.WithTimeout(ctx, generatedMediaSyncUploadTimeout())
	defer cancel()

	changed := false
	if itemChanged, err := rewriteGeneratedMediaDataURLsNodeToOSS(uploadCtx, params, &root, "$"); err != nil {
		return body, false, err
	} else {
		changed = changed || itemChanged
	}
	if itemChanged, err := rewriteVideoURLsNodeToOSS(uploadCtx, params, &root, "$"); err != nil {
		return body, false, err
	} else {
		changed = changed || itemChanged
	}
	if rootMap, ok := root.(map[string]any); ok {
		if data, ok := rootMap["data"]; ok {
			if itemChanged, err := rewriteImageDataNodeToOSS(uploadCtx, params, data, "$.data"); err != nil {
				return body, false, err
			} else {
				rootMap["data"] = data
				changed = changed || itemChanged
			}
		}
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

func extractUpstreamUsage(body []byte) upstreamUsageSummary {
	if len(body) == 0 {
		return upstreamUsageSummary{}
	}
	if usage := extractUpstreamUsageJSON(body); usage.UsageJSON != "" {
		return usage
	}
	return extractUpstreamUsageSSE(body)
}

func extractUpstreamUsageJSON(body []byte) upstreamUsageSummary {
	if !json.Valid(body) {
		return upstreamUsageSummary{}
	}
	var root any
	if err := common.Unmarshal(body, &root); err != nil {
		return upstreamUsageSummary{}
	}
	usageNode := findUpstreamUsageNode(root)
	if usageNode == nil {
		return upstreamUsageSummary{}
	}
	usageBody, err := common.Marshal(usageNode)
	if err != nil {
		return upstreamUsageSummary{}
	}
	summary := upstreamUsageSummary{UsageJSON: string(usageBody)}
	usageMap, _ := usageNode.(map[string]any)
	summary.PromptTokens = firstInt64FromMap(usageMap, "prompt_tokens", "input_tokens", "promptTokenCount", "inputTokenCount")
	summary.CompletionTokens = firstInt64FromMap(usageMap, "completion_tokens", "output_tokens", "candidatesTokenCount", "outputTokenCount")
	if thoughts := firstInt64FromMap(usageMap, "thoughtsTokenCount", "reasoning_tokens"); thoughts > 0 {
		summary.CompletionTokens += thoughts
	}
	summary.TotalTokens = firstInt64FromMap(usageMap, "total_tokens", "totalTokens", "totalTokenCount")
	if summary.TotalTokens == 0 && (summary.PromptTokens > 0 || summary.CompletionTokens > 0) {
		summary.TotalTokens = summary.PromptTokens + summary.CompletionTokens
	}
	return summary
}

func extractUpstreamUsageSSE(body []byte) upstreamUsageSummary {
	var latest upstreamUsageSummary
	var eventData [][]byte

	flushEvent := func() {
		if len(eventData) == 0 {
			return
		}
		payload := bytes.TrimSpace(bytes.Join(eventData, []byte("\n")))
		eventData = eventData[:0]
		if bytes.Equal(payload, []byte("[DONE]")) {
			return
		}
		if usage := extractUpstreamUsageJSON(payload); usage.UsageJSON != "" {
			latest = usage
		}
	}

	for _, rawLine := range bytes.Split(body, []byte("\n")) {
		line := bytes.TrimSuffix(rawLine, []byte("\r"))
		if len(line) == 0 {
			flushEvent()
			continue
		}
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		data := line[len("data:"):]
		if len(data) > 0 && data[0] == ' ' {
			data = data[1:]
		}
		eventData = append(eventData, data)
	}
	flushEvent()
	return latest
}

func findUpstreamUsageNode(node any) any {
	switch value := node.(type) {
	case map[string]any:
		for _, key := range []string{"usage", "usage_metadata", "usageMetadata"} {
			if usage, ok := value[key]; ok {
				if _, isMap := usage.(map[string]any); isMap {
					return usage
				}
			}
		}
		for _, child := range value {
			if found := findUpstreamUsageNode(child); found != nil {
				return found
			}
		}
	case []any:
		for _, child := range value {
			if found := findUpstreamUsageNode(child); found != nil {
				return found
			}
		}
	}
	return nil
}

func firstInt64FromMap(values map[string]any, keys ...string) int64 {
	if values == nil {
		return 0
	}
	for _, key := range keys {
		if n, ok := anyToInt64(values[key]); ok {
			return n
		}
	}
	return 0
}

func anyToInt64(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case int32:
		return int64(v), true
	case uint:
		return int64(v), true
	case uint64:
		return int64(v), true
	case uint32:
		return int64(v), true
	case float64:
		return int64(v), true
	case float32:
		return int64(v), true
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	case string:
		var n int64
		_, err := fmt.Sscan(strings.TrimSpace(v), &n)
		return n, err == nil
	default:
		return 0, false
	}
}
