package middleware

import (
	"bytes"
	"io"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// auditResponseWriter 包装 gin.ResponseWriter，限量复制响应正文。
// 必须转发 Flush()，否则流式 SSE 会被缓冲，破坏逐 chunk 推送。
type auditResponseWriter struct {
	gin.ResponseWriter
	context        *gin.Context
	buf            bytes.Buffer
	maxBytes       int
	mediaBuf       bytes.Buffer
	mediaMaxBytes  int
	written        int64
	truncated      bool
	mediaTruncated bool
}

func (w *auditResponseWriter) Write(data []byte) (int, error) {
	updated := transformGroupFallbackJSONResponse(w.context, w.ResponseWriter, data)
	w.written += int64(len(updated))
	w.capture(updated)
	n, err := w.ResponseWriter.Write(updated)
	if err != nil {
		return 0, err
	}
	if n != len(updated) {
		return 0, io.ErrShortWrite
	}
	return len(data), nil
}

func (w *auditResponseWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

// Flush 转发到底层 writer，保证流式响应实时下发。
func (w *auditResponseWriter) Flush() {
	w.ResponseWriter.Flush()
}

func (w *auditResponseWriter) capture(data []byte) {
	if len(data) == 0 {
		return
	}
	captureLimited(&w.buf, w.maxBytes, &w.truncated, data)
	captureLimited(&w.mediaBuf, w.mediaMaxBytes, &w.mediaTruncated, data)
}

func captureLimited(buf *bytes.Buffer, maxBytes int, truncated *bool, data []byte) {
	if maxBytes <= 0 {
		return
	}
	remain := maxBytes - buf.Len()
	if remain <= 0 {
		*truncated = true
		return
	}
	if len(data) > remain {
		buf.Write(data[:remain])
		*truncated = true
		return
	}
	buf.Write(data)
}

// isTextualContentType 判断是否纯文本（走 MySQL 全量存）。
func isTextualContentType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "" {
		return true // 多数 relay JSON 默认按文本处理
	}
	switch {
	case strings.HasPrefix(ct, "application/json"),
		strings.HasPrefix(ct, "text/"),
		strings.HasPrefix(ct, "application/x-ndjson"),
		strings.Contains(ct, "event-stream"):
		return true
	}
	return false
}

// AuditLog 审计留存 middleware：捕获请求/响应正文，按类型分流后异步入队。
func AuditLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		captureGeneratedMedia := service.GeneratedMediaEnabled()
		if !common.AuditLogEnabled && !captureGeneratedMedia {
			c.Next()
			return
		}

		startTime := time.Now()

		// 包装响应 writer，限量复制响应正文。
		var arw *auditResponseWriter
		if common.AuditLogCaptureResponse || captureGeneratedMedia {
			arw = &auditResponseWriter{
				ResponseWriter: c.Writer,
				context:        c,
				maxBytes:       common.AuditLogMaxBodyBytes,
				mediaMaxBytes:  service.GeneratedMediaCaptureMaxBytes(),
			}
			c.Writer = arw
		}

		c.Next()

		// 主流程之外，捕获失败绝不影响请求。
		defer func() {
			if r := recover(); r != nil {
				common.SysError("panic in audit middleware (ignored)")
			}
		}()

		shouldWriteAudit := common.AuditLogEnabled && shouldAudit(c)
		if !shouldWriteAudit && !captureGeneratedMedia {
			return
		}

		buildAndEnqueue(c, arw, startTime, shouldWriteAudit, captureGeneratedMedia)
	}
}

// shouldAudit 分组/模型白名单 + 抽样过滤。
func shouldAudit(c *gin.Context) bool {
	group := common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	if !inAllowlist(common.AuditLogGroupAllowlist, group) {
		return false
	}
	modelName := common.GetContextKeyString(c, constant.ContextKeyOriginalModel)
	if !inAllowlist(common.AuditLogModelAllowlist, modelName) {
		return false
	}
	return true
}

func inAllowlist(allowlist, val string) bool {
	allowlist = strings.TrimSpace(allowlist)
	if allowlist == "" {
		return true // 空表示全部
	}
	for _, item := range strings.Split(allowlist, ",") {
		if strings.TrimSpace(item) == val {
			return true
		}
	}
	return false
}

func buildAndEnqueue(c *gin.Context, arw *auditResponseWriter, startTime time.Time, shouldWriteAudit bool, captureGeneratedMedia bool) {
	now := common.GetTimestamp()
	retentionDays := common.AuditLogRetentionDays
	if retentionDays <= 0 {
		retentionDays = 7
	}

	row := model.ApiAuditLog{
		CreatedAt:           now,
		ExpireAt:            now + int64(retentionDays)*86400,
		RequestId:           c.GetString(common.RequestIdKey),
		UpstreamRequestId:   c.Writer.Header().Get(common.UpstreamRequestIdKey),
		UserId:              c.GetInt("id"),
		Username:            common.GetContextKeyString(c, constant.ContextKeyUserName),
		TokenId:             c.GetInt("token_id"),
		TokenName:           c.GetString("token_name"),
		UserGroup:           common.GetContextKeyString(c, constant.ContextKeyUserGroup),
		ModelName:           common.GetContextKeyString(c, constant.ContextKeyOriginalModel),
		ChannelId:           common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		ChannelName:         common.GetContextKeyString(c, constant.ContextKeyChannelName),
		ChannelType:         common.GetContextKeyInt(c, constant.ContextKeyChannelType),
		RelayMode:           c.GetInt("relay_mode"),
		Method:              c.Request.Method,
		Path:                c.Request.URL.Path,
		StatusCode:          c.Writer.Status(),
		IsStream:            common.GetContextKeyBool(c, constant.ContextKeyIsStream),
		UseTimeMs:           int(time.Since(startTime).Milliseconds()),
		RequestContentType:  c.Request.Header.Get("Content-Type"),
		ResponseContentType: c.Writer.Header().Get("Content-Type"),
		BodyStorage:         model.AuditBodyInline,
	}
	if common.AuditLogRecordIp {
		row.ClientIp = c.ClientIP()
	}

	var binaries []service.AuditBinaryBody

	// 请求正文
	if shouldWriteAudit && common.AuditLogCaptureRequest {
		if reqBody, err := readRequestBody(c); err == nil && len(reqBody) > 0 {
			row.RequestBodySize = int64(len(reqBody))
			row.RequestBodySha256 = common.Sha256Hex(reqBody)
			if isTextualContentType(row.RequestContentType) {
				text, truncated := storeTextBody(reqBody)
				row.RequestBody = text
				row.RequestTruncated = truncated
			} else {
				row.BodyStorage = model.AuditBodyOmittedMeta
			}
		}
	}

	// 响应正文
	if shouldWriteAudit && arw != nil {
		respBody := arw.buf.Bytes()
		row.ResponseBodySize = arw.written
		row.ResponseTruncated = arw.truncated
		if len(respBody) > 0 {
			row.ResponseBodySha256 = common.Sha256Hex(respBody)
			if isTextualContentType(row.ResponseContentType) {
				text, truncated := storeTextBody(respBody)
				row.ResponseBody = text
				if truncated {
					row.ResponseTruncated = true
				}
			} else {
				row.BodyStorage = model.AuditBodyOmittedMeta
			}
		}
	}

	// 组装本次消费金额 billing，写入 Other。
	row.Other = appendGroupFallbackAuditOther(c, row.Other)
	row.Other = buildBillingOther(c, &row)

	if captureGeneratedMedia && arw != nil && !arw.mediaTruncated && arw.mediaBuf.Len() > 0 {
		service.EnqueueGeneratedMediaFromAuditResponse(row, arw.mediaBuf.Bytes())
	}

	if shouldWriteAudit {
		service.EnqueueAuditLog(service.AuditLogPayload{
			Log:      row,
			Binaries: binaries,
		})
	}
}

func appendGroupFallbackAuditOther(c *gin.Context, existing string) string {
	metadata, ok := service.GetGroupFallbackResponseMetadata(c)
	if !ok {
		return existing
	}
	other := map[string]any{}
	if existing != "" {
		if current, err := common.StrToMap(existing); err == nil && current != nil {
			other = current
		}
	}
	other["group_fallback"] = metadata
	return common.MapToJsonStr(other)
}

// buildBillingOther 读取最终扣费 quota，组装 billing 写入 Other JSON。
func buildBillingOther(c *gin.Context, row *model.ApiAuditLog) string {
	if !common.GetContextKeyBool(c, constant.ContextKeyBillingReady) {
		return row.Other // 未结算（鉴权失败等）不写 billing
	}
	quota := common.GetContextKeyInt(c, constant.ContextKeyFinalQuota)
	billingID := common.GetContextKeyString(c, constant.ContextKeyBillingId)
	if billingID == "" {
		billingID = row.RequestId
	}
	billingType := common.GetContextKeyString(c, constant.ContextKeyBillingType)
	if billingType == "" {
		billingType = "request"
	}

	billing := map[string]any{
		"billing_id":       billingID,
		"billing_type":     billingType,
		"quota":            quota,
		"display_amount":   common.FormatBillingAmountText(common.BillingDisplayAmountFromQuota(int64(quota)), common.GetServerBillingCurrency()),
		"display_currency": common.GetServerBillingCurrency(),
		"quota_per_unit":   common.QuotaPerUnit,
		"model":            row.ModelName,
		"channel_id":       row.ChannelId,
		"token_id":         row.TokenId,
		"group":            row.UserGroup,
		"is_stream":        row.IsStream,
		"server_region":    common.GetServerRegion(),
	}

	other := map[string]any{}
	if row.Other != "" {
		if existing, err := common.StrToMap(row.Other); err == nil && existing != nil {
			other = existing
		}
	}
	other["billing_id"] = billingID
	other["billing_type"] = billingType
	other["server_region"] = common.GetServerRegion()
	other["currency"] = common.GetServerBillingCurrency()
	if billingType == "task" {
		other["task_id"] = billingID
		other["create_request_id"] = row.RequestId
	}
	other["billing"] = billing
	return common.MapToJsonStr(other)
}

// readRequestBody 在 c.Next() 后从可复用的 BodyStorage 读取请求体。
func readRequestBody(c *gin.Context) ([]byte, error) {
	bs, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	return bs.Bytes()
}

// storeTextBody 脱敏 + 内联 base64 剥离 + 可选加密，返回入库字符串与是否截断。
// 纯文本超长策略：决策为「存数据库」，完整入库不截断。
func storeTextBody(body []byte) (string, bool) {
	// 先剥离 JSON 内的内联 base64（b64_json/data:image 等），避免大块二进制撑爆 MySQL。
	stripped, _ := common.StripInlineBase64(body)
	sanitized := common.RedactAuditText(stripped)

	// 配置了密钥则加密入库，避免明文对话落库；加密后 base64 存入 LONGTEXT。
	if common.AuditLogSecret != "" {
		if enc, err := common.AuditEncrypt(sanitized); err == nil {
			return common.AuditEncodeStored(enc), false
		}
		common.SysError("audit encrypt failed, storing plaintext")
	}
	return string(sanitized), false
}
