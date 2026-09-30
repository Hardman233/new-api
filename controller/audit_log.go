package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	billingservice "github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/oss"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

// GetAuditLogs 管理员分页查询审计列表（不含正文）。
func GetAuditLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	userId, _ := strconv.Atoi(c.Query("user_id"))
	tokenId, _ := strconv.Atoi(c.Query("token_id"))
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	statusCode, _ := strconv.Atoi(c.Query("status_code"))

	q := model.AuditLogQuery{
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		UserId:         userId,
		TokenId:        tokenId,
		ModelName:      c.Query("model_name"),
		ChannelId:      channelId,
		RequestId:      c.Query("request_id"),
		StatusCode:     statusCode,
	}
	logs, total, err := model.GetAuditLogs(q, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}

// GetAuditLogDetail 管理员查看单条审计详情（含解密正文）。
// GetAuditQuery provides a service-to-service read-only audit lookup by request_id.
func GetAuditQuery(c *gin.Context) {
	requestId := strings.TrimSpace(c.Query("request_id"))
	if requestId == "" {
		common.ApiErrorMsg(c, "request_id is required")
		return
	}
	logRow, err := model.GetApiAuditLogByRequestId(requestId)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorMsg(c, "audit log not found")
			return
		}
		common.ApiError(c, err)
		return
	}

	billing := buildAuditQueryBilling(logRow.Other)
	tokens := billingTokensFromOther(logRow.Other)
	attachBillingTokenSummary(billing, tokens)
	data := gin.H{
		"billing_id":        requestId,
		"billing_type":      "request",
		"request_id":        logRow.RequestId,
		"created_at":        logRow.CreatedAt,
		"model_name":        logRow.ModelName,
		"channel_id":        logRow.ChannelId,
		"channel_name":      logRow.ChannelName,
		"token_id":          logRow.TokenId,
		"token_name":        logRow.TokenName,
		"user_group":        logRow.UserGroup,
		"status_code":       logRow.StatusCode,
		"is_stream":         logRow.IsStream,
		"use_time_ms":       logRow.UseTimeMs,
		"prompt_tokens":     tokens.PromptTokens,
		"completion_tokens": tokens.CompletionTokens,
		"total_tokens":      tokens.TotalTokens,
		"usage":             tokens.toUsageMap(),
		"billing":           billing,
	}
	attachUpstreamResponseLog(c, data, requestId, logRow.RequestId, "")
	common.ApiSuccess(c, data)
}

func GetBillingQuery(c *gin.Context) {
	billingID := strings.TrimSpace(c.Query("billing_id"))
	if billingID == "" {
		billingID = strings.TrimSpace(c.Query("request_id"))
	}
	if billingID == "" {
		common.ApiErrorMsg(c, "billing_id is required")
		return
	}
	if ledger, err := model.GetBillingLedgerByBillingID(billingID); err == nil {
		getLedgerBillingQuery(c, ledger)
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		common.ApiError(c, err)
		return
	}
	if strings.HasPrefix(billingID, "task_") {
		getTaskBillingQuery(c, billingID)
		return
	}
	getRequestBillingQuery(c, billingID)
}

// GetTokenBillingQuery returns a persisted bill owned by the authenticated user.
// Unlike the audit endpoint, it never reads or returns upstream response logs.
func GetTokenBillingQuery(c *gin.Context) {
	billingID := strings.TrimSpace(c.Query("billing_id"))
	if billingID == "" {
		billingID = strings.TrimSpace(c.Query("request_id"))
	}
	if billingID == "" {
		common.ApiErrorMsg(c, "billing_id is required")
		return
	}

	ledger, err := model.GetBillingLedgerByBillingID(billingID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorMsg(c, "billing not found")
			return
		}
		common.ApiError(c, err)
		return
	}
	userID := c.GetInt("id")
	if userID <= 0 || ledger.UserID != userID {
		common.ApiErrorMsg(c, "billing not found")
		return
	}

	common.ApiSuccess(c, buildLedgerBillingQueryData(ledger, true))
}

func GetSupplierStats(c *gin.Context) {
	keyword := strings.TrimSpace(c.Query("supplier_keyword"))
	if keyword == "" {
		common.ApiErrorMsg(c, "supplier_keyword is required")
		return
	}
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	q := model.SupplierStatsQuery{
		SupplierKeyword: keyword,
		StartTimestamp:  startTimestamp,
		EndTimestamp:    endTimestamp,
		ModelName:       strings.TrimSpace(c.Query("model_name")),
		UserGroup:       strings.TrimSpace(c.Query("user_group")),
		TokenName:       strings.TrimSpace(c.Query("token_name")),
		ServerRegion:    strings.TrimSpace(c.Query("server_region")),
		Currency:        strings.ToUpper(strings.TrimSpace(c.Query("currency"))),
		Source:          strings.TrimSpace(c.Query("source")),
	}
	result, err := model.GetSupplierStats(q)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if len(result.MatchedChannels) == 0 &&
		len(result.SummaryByCurrency) == 0 &&
		len(result.BySupplier) == 0 &&
		len(result.ByChannel) == 0 &&
		len(result.ByModel) == 0 {
		common.ApiErrorMsg(c, "supplier stats not found")
		return
	}
	common.ApiSuccess(c, result)
}

func getLedgerBillingQuery(c *gin.Context, ledger *model.BillingLedger) {
	data := buildLedgerBillingQueryData(ledger, false)
	attachUpstreamResponseLog(c, data, ledger.BillingID, ledger.SourceRequestID, ledger.SourceTaskID)
	common.ApiSuccess(c, data)
}

func buildLedgerBillingQueryData(ledger *model.BillingLedger, persistedOnly bool) gin.H {
	amount, _ := ledger.Amount.Float64()
	quota := ledger.Quota
	amountText := ledger.AmountText
	reconciliation := taskLedgerReconciliation{}
	if !persistedOnly {
		reconciliation = reconcileTaskLedgerBilling(ledger, quota, amount, amountText)
		if reconciliation.Reconciled {
			quota = reconciliation.Quota
			amount = reconciliation.Amount
			amountText = reconciliation.AmountText
		}
	}
	tokens := billingTokensFromLedger(ledger)
	billing := gin.H{
		"billing_id":          ledger.BillingID,
		"billing_type":        ledger.BillingType,
		"quota":               quota,
		"display_amount":      amount,
		"display_amount_text": amountText,
		"display_currency":    ledger.Currency,
		"quota_per_unit":      common.QuotaPerUnit,
		"model":               ledger.ModelName,
		"channel_id":          ledger.ChannelID,
		"token_id":            ledger.TokenID,
		"group":               ledger.UserGroup,
		"is_stream":           ledger.IsStream,
		"server_region":       ledger.ServerRegion,
		"supplier_name":       ledger.SupplierName,
		"status":              ledger.Status,
		"billing_source":      "ledger",
	}
	attachLedgerBillingEvidence(billing, ledger)
	attachTaskLedgerReconciliation(billing, reconciliation)
	attachBillingTokenSummary(billing, tokens)
	if !attachLedgerBillingCNYReference(billing, ledger, !persistedOnly) && !persistedOnly {
		attachBillingCNYReference(billing)
	}
	data := gin.H{
		"billing_id":        ledger.BillingID,
		"billing_type":      ledger.BillingType,
		"billing_source":    "ledger",
		"request_id":        ledger.SourceRequestID,
		"create_request_id": ledger.SourceRequestID,
		"task_id":           ledger.SourceTaskID,
		"created_at":        ledger.CreatedAt,
		"completed_at":      ledger.SettledAt,
		"model_name":        ledger.ModelName,
		"upstream_model":    ledger.UpstreamModelName,
		"channel_id":        ledger.ChannelID,
		"channel_name":      ledger.ChannelName,
		"supplier_name":     ledger.SupplierName,
		"server_region":     ledger.ServerRegion,
		"token_id":          ledger.TokenID,
		"token_name":        ledger.TokenName,
		"user_group":        ledger.UserGroup,
		"status":            ledger.Status,
		"is_stream":         ledger.IsStream,
		"billing":           billing,
	}
	attachLedgerBillingEvidence(data, ledger)
	attachTaskLedgerReconciliation(data, reconciliation)
	attachBillingSummary(data, billing)
	return data
}

type taskLedgerReconciliation struct {
	Reconciled       bool
	Reason           string
	LedgerQuota      int64
	LedgerAmount     float64
	LedgerAmountText string
	TaskQuota        int64
	Quota            int64
	Amount           float64
	AmountText       string
}

func reconcileTaskLedgerBilling(ledger *model.BillingLedger, fallbackQuota int64, fallbackAmount float64, fallbackAmountText string) taskLedgerReconciliation {
	result := taskLedgerReconciliation{
		Quota:      fallbackQuota,
		Amount:     fallbackAmount,
		AmountText: fallbackAmountText,
	}
	if ledger == nil || ledger.BillingType != model.BillingLedgerTypeTask || ledger.Status != model.BillingLedgerStatusSettled {
		return result
	}
	taskID := strings.TrimSpace(ledger.SourceTaskID)
	if taskID == "" {
		taskID = strings.TrimSpace(ledger.BillingID)
	}
	if taskID == "" {
		return result
	}
	task, exist, err := model.GetByOnlyTaskId(taskID)
	if err != nil || !exist || task == nil || task.Status != model.TaskStatusSuccess || task.Quota <= 0 {
		return result
	}
	taskQuota := int64(task.Quota)
	if taskQuota == ledger.Quota {
		return result
	}
	amount := common.BillingDisplayAmountFromQuota(taskQuota)
	amountText := common.FormatBillingAmountText(amount, ledger.Currency)
	return taskLedgerReconciliation{
		Reconciled:       true,
		Reason:           "task_quota_mismatch",
		LedgerQuota:      ledger.Quota,
		LedgerAmount:     fallbackAmount,
		LedgerAmountText: fallbackAmountText,
		TaskQuota:        taskQuota,
		Quota:            taskQuota,
		Amount:           amount,
		AmountText:       amountText,
	}
}

func attachTaskLedgerReconciliation(target gin.H, reconciliation taskLedgerReconciliation) {
	if target == nil || !reconciliation.Reconciled {
		return
	}
	target["billing_reconciled"] = true
	target["billing_reconcile_reason"] = reconciliation.Reason
	target["ledger_quota"] = reconciliation.LedgerQuota
	target["ledger_display_amount"] = reconciliation.LedgerAmount
	target["ledger_display_amount_text"] = reconciliation.LedgerAmountText
	target["task_quota"] = reconciliation.TaskQuota
}

func getRequestBillingQuery(c *gin.Context, requestID string) {
	logRow, err := model.GetApiAuditLogByRequestId(requestID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorMsg(c, "billing log not found")
			return
		}
		common.ApiError(c, err)
		return
	}
	billing := buildAuditQueryBilling(logRow.Other)
	tokens := billingTokensFromOther(logRow.Other)
	attachBillingTokenSummary(billing, tokens)
	attachBillingCNYReference(billing)
	data := gin.H{
		"billing_id":   requestID,
		"billing_type": "request",
		"request_id":   logRow.RequestId,
		"created_at":   logRow.CreatedAt,
		"model_name":   logRow.ModelName,
		"channel_id":   logRow.ChannelId,
		"channel_name": logRow.ChannelName,
		"token_id":     logRow.TokenId,
		"token_name":   logRow.TokenName,
		"user_group":   logRow.UserGroup,
		"status_code":  logRow.StatusCode,
		"is_stream":    logRow.IsStream,
		"use_time_ms":  logRow.UseTimeMs,
		"billing":      billing,
	}
	attachBillingTokenSummary(data, tokens)
	attachBillingSummary(data, billing)
	attachUpstreamResponseLog(c, data, requestID, logRow.RequestId, "")
	common.ApiSuccess(c, data)
}

func getTaskBillingQuery(c *gin.Context, taskID string) {
	task, exist, err := model.GetByOnlyTaskId(taskID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !exist {
		common.ApiErrorMsg(c, "billing task not found")
		return
	}

	channelName := ""
	if ch, chErr := model.GetChannelById(task.ChannelId, false); chErr == nil && ch != nil {
		channelName = ch.Name
	}
	tokenName := ""
	if task.PrivateData.TokenId > 0 {
		if token, tokenErr := model.GetTokenById(task.PrivateData.TokenId); tokenErr == nil && token != nil {
			tokenName = token.Name
		}
	}
	modelName := task.Properties.OriginModelName
	if modelName == "" {
		modelName = task.Properties.UpstreamModelName
	}
	useTimeMs := 0
	if task.FinishTime > 0 && task.SubmitTime > 0 && task.FinishTime >= task.SubmitTime {
		useTimeMs = int((task.FinishTime - task.SubmitTime) * 1000)
	}

	billing := gin.H{
		"billing_id":          task.TaskID,
		"billing_type":        "task",
		"quota":               task.Quota,
		"display_amount":      quotaDisplayAmount(task.Quota),
		"display_amount_text": common.FormatBillingAmountText(quotaDisplayAmount(task.Quota), common.GetServerBillingCurrency()),
		"display_currency":    common.GetServerBillingCurrency(),
		"quota_per_unit":      common.QuotaPerUnit,
		"model":               modelName,
		"channel_id":          task.ChannelId,
		"token_id":            task.PrivateData.TokenId,
		"group":               task.Group,
		"is_stream":           false,
		"server_region":       common.GetServerRegion(),
		"billing_source":      "legacy",
	}
	tokens := billingTokensFromJSON(task.Data)
	attachBillingTokenSummary(billing, tokens)
	attachBillingCNYReference(billing)
	data := gin.H{
		"billing_id":         task.TaskID,
		"billing_type":       "task",
		"billing_source":     "legacy",
		"request_id":         task.Properties.CreateRequestID,
		"create_request_id":  task.Properties.CreateRequestID,
		"task_id":            task.TaskID,
		"created_at":         task.SubmitTime,
		"completed_at":       task.FinishTime,
		"model_name":         modelName,
		"channel_id":         task.ChannelId,
		"channel_name":       channelName,
		"token_id":           task.PrivateData.TokenId,
		"token_name":         tokenName,
		"user_group":         task.Group,
		"status":             task.Status.ToVideoStatus(),
		"submit_retry_count": task.PrivateData.SubmitRetryCount,
		"is_stream":          false,
		"use_time_ms":        useTimeMs,
		"billing":            billing,
	}
	attachBillingTokenSummary(data, tokens)
	attachBillingSummary(data, billing)
	attachUpstreamResponseLog(c, data, task.TaskID, task.Properties.CreateRequestID, task.TaskID)
	common.ApiSuccess(c, data)
}

func attachUpstreamResponseLog(c *gin.Context, data gin.H, billingID, requestID, taskID string) {
	logRow, err := model.GetLatestUpstreamResponseLog(billingID, requestID, taskID)
	if err != nil || logRow == nil {
		return
	}
	item := gin.H{
		"id":                  logRow.Id,
		"created_at":          logRow.CreatedAt,
		"request_id":          logRow.RequestId,
		"billing_id":          logRow.BillingID,
		"task_id":             logRow.TaskID,
		"model_name":          logRow.ModelName,
		"upstream_model_name": logRow.UpstreamModelName,
		"channel_id":          logRow.ChannelID,
		"channel_name":        logRow.ChannelName,
		"channel_type":        logRow.ChannelType,
		"relay_mode":          logRow.RelayMode,
		"retry_count":         logRow.RetryCount,
		"status_code":         logRow.StatusCode,
		"content_type":        logRow.ContentType,
		"body_size":           logRow.BodySize,
		"sanitized_body_size": logRow.SanitizedBodySize,
		"body_sha256":         logRow.BodySha256,
		"body_truncated":      logRow.BodyTruncated,
		"media_replaced":      logRow.MediaReplaced,
		"prompt_tokens":       logRow.PromptTokens,
		"completion_tokens":   logRow.CompletionTokens,
		"total_tokens":        logRow.TotalTokens,
		"usage":               parseAuditJSONValue(logRow.UsageJSON),
		"error":               logRow.Error,
	}
	if shouldIncludeUpstreamResponse(c) {
		item["body"] = parseAuditJSONValue(logRow.Body)
	}
	data["upstream_response_log"] = item
}

func shouldIncludeUpstreamResponse(c *gin.Context) bool {
	if c == nil {
		return false
	}
	value := strings.ToLower(strings.TrimSpace(c.Query("include_upstream_response")))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(c.Query("include_upstream_body")))
	}
	return value == "1" || value == "true" || value == "yes"
}

func parseAuditJSONValue(raw string) interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var value interface{}
	if err := common.Unmarshal([]byte(raw), &value); err != nil {
		return raw
	}
	return value
}

func attachBillingSummary(data gin.H, billing gin.H) {
	if billing == nil {
		return
	}
	for _, key := range []string{
		"quota", "display_amount", "display_amount_text", "display_currency", "quota_per_unit",
		"amount_usd", "amount_cny", "amount_cny_text",
		"exchange_rate", "exchange_rate_base", "exchange_rate_target", "exchange_rate_updated_at",
		"exchange_rate_source", "exchange_rate_stale",
		"prompt_tokens", "completion_tokens", "total_tokens", "usage",
		"server_region", "supplier_name", "status", "billing_source",
		"billing_mode", "billing_expr_matched", "billing_expr_hash", "billing_tier",
		"billing_match_info", "billing_calc_detail",
	} {
		if value, ok := billing[key]; ok {
			data[key] = value
		}
	}
}

func attachLedgerBillingEvidence(target gin.H, ledger *model.BillingLedger) {
	if target == nil || ledger == nil {
		return
	}
	target["billing_mode"] = ledger.BillingMode
	target["billing_expr_matched"] = ledger.BillingExprMatched
	target["billing_expr_hash"] = ledger.BillingExprHash
	target["billing_tier"] = ledger.BillingTier
	if strings.TrimSpace(ledger.BillingMatchInfo) != "" {
		target["billing_match_info"] = parseAuditJSONValue(ledger.BillingMatchInfo)
	}
	if strings.TrimSpace(ledger.BillingCalcDetail) != "" {
		target["billing_calc_detail"] = parseAuditJSONValue(ledger.BillingCalcDetail)
	}
}

type billingTokenSummary struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Usage            map[string]interface{}
}

func (t billingTokenSummary) hasTokens() bool {
	return t.PromptTokens > 0 || t.CompletionTokens > 0 || t.TotalTokens > 0
}

func billingTokensFromLedger(ledger *model.BillingLedger) billingTokenSummary {
	if ledger == nil {
		return billingTokenSummary{}
	}
	tokens := billingTokenSummary{
		PromptTokens:     ledger.PromptTokens,
		CompletionTokens: ledger.CompletionTokens,
	}
	tokens.merge(billingTokensFromOther(ledger.Other))
	tokens.normalize()
	if !tokens.hasTokens() && ledger.BillingType == model.BillingLedgerTypeTask {
		taskID := strings.TrimSpace(ledger.SourceTaskID)
		if taskID == "" {
			taskID = strings.TrimSpace(ledger.BillingID)
		}
		if taskID != "" {
			if task, exist, err := model.GetByOnlyTaskId(taskID); err == nil && exist && task != nil {
				tokens.merge(billingTokensFromJSON(task.Data))
				tokens.normalize()
			}
		}
	}
	return tokens
}

func billingTokensFromOther(other string) billingTokenSummary {
	if strings.TrimSpace(other) == "" {
		return billingTokenSummary{}
	}
	otherMap, err := common.StrToMap(other)
	if err != nil {
		return billingTokenSummary{}
	}
	tokens := billingTokensFromMap(otherMap, 0)
	tokens.normalize()
	return tokens
}

func billingTokensFromJSON(raw []byte) billingTokenSummary {
	if len(raw) == 0 {
		return billingTokenSummary{}
	}
	var payload map[string]interface{}
	if err := common.Unmarshal(raw, &payload); err != nil {
		return billingTokenSummary{}
	}
	tokens := billingTokensFromMap(payload, 0)
	tokens.normalize()
	return tokens
}

func billingTokensFromMap(payload map[string]interface{}, depth int) billingTokenSummary {
	if payload == nil || depth > 4 {
		return billingTokenSummary{}
	}
	tokens := billingTokensFromUsageMap(payload)
	for _, key := range []string{"usage", "billing", "metadata", "data", "result", "response", "content"} {
		child, ok := billingMapValue(payload[key])
		if !ok {
			continue
		}
		if key == "usage" {
			tokens.merge(billingTokensFromUsageMap(child))
		}
		tokens.merge(billingTokensFromMap(child, depth+1))
	}
	tokens.normalize()
	return tokens
}

func billingTokensFromUsageMap(payload map[string]interface{}) billingTokenSummary {
	if payload == nil {
		return billingTokenSummary{}
	}
	promptTokens := firstBillingTokenInt64(payload, "prompt_tokens", "input_tokens")
	completionTokens := firstBillingTokenInt64(payload, "completion_tokens", "output_tokens")
	totalTokens := firstBillingTokenInt64(payload, "total_tokens")
	if promptTokens == 0 && completionTokens == 0 && totalTokens == 0 {
		return billingTokenSummary{}
	}
	usage := map[string]interface{}{}
	if promptTokens > 0 {
		usage["prompt_tokens"] = promptTokens
	}
	if completionTokens > 0 {
		usage["completion_tokens"] = completionTokens
	}
	if totalTokens > 0 {
		usage["total_tokens"] = totalTokens
	}
	if toolUsage, ok := payload["tool_usage"]; ok {
		usage["tool_usage"] = toolUsage
	}
	return billingTokenSummary{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		Usage:            usage,
	}
}

func (t *billingTokenSummary) merge(next billingTokenSummary) {
	if next.PromptTokens > t.PromptTokens {
		t.PromptTokens = next.PromptTokens
	}
	if next.CompletionTokens > t.CompletionTokens {
		t.CompletionTokens = next.CompletionTokens
	}
	if next.TotalTokens > t.TotalTokens {
		t.TotalTokens = next.TotalTokens
	}
	if next.Usage != nil {
		if t.Usage == nil {
			t.Usage = make(map[string]interface{}, len(next.Usage))
		}
		for k, v := range next.Usage {
			t.Usage[k] = v
		}
	}
}

func (t *billingTokenSummary) normalize() {
	if t.TotalTokens == 0 && (t.PromptTokens > 0 || t.CompletionTokens > 0) {
		t.TotalTokens = t.PromptTokens + t.CompletionTokens
	}
	if t.TotalTokens > 0 {
		if t.PromptTokens == 0 && t.CompletionTokens == 0 {
			t.CompletionTokens = t.TotalTokens
		} else if t.CompletionTokens == 0 && t.TotalTokens >= t.PromptTokens {
			t.CompletionTokens = t.TotalTokens - t.PromptTokens
		}
	}
	if t.TotalTokens > t.PromptTokens+t.CompletionTokens {
		t.PromptTokens += t.TotalTokens - t.PromptTokens - t.CompletionTokens
	}
	if t.Usage == nil {
		t.Usage = map[string]interface{}{}
	}
	t.Usage["prompt_tokens"] = t.PromptTokens
	t.Usage["completion_tokens"] = t.CompletionTokens
	t.Usage["total_tokens"] = t.TotalTokens
}

func (t billingTokenSummary) toUsageMap() gin.H {
	t.normalize()
	usage := gin.H{
		"prompt_tokens":     t.PromptTokens,
		"completion_tokens": t.CompletionTokens,
		"total_tokens":      t.TotalTokens,
	}
	for k, v := range t.Usage {
		if k == "prompt_tokens" || k == "completion_tokens" || k == "total_tokens" {
			continue
		}
		usage[k] = v
	}
	return usage
}

func attachBillingTokenSummary(target gin.H, tokens billingTokenSummary) {
	if target == nil {
		return
	}
	tokens.normalize()
	target["prompt_tokens"] = tokens.PromptTokens
	target["completion_tokens"] = tokens.CompletionTokens
	target["total_tokens"] = tokens.TotalTokens
	target["usage"] = tokens.toUsageMap()
}

func firstBillingTokenInt64(payload map[string]interface{}, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			if n := numberToInt64(value); n > 0 {
				return n
			}
		}
	}
	return 0
}

func numberToInt64(value interface{}) int64 {
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case int16:
		return int64(v)
	case int8:
		return int64(v)
	case uint:
		return int64(v)
	case uint64:
		return int64(v)
	case uint32:
		return int64(v)
	case uint16:
		return int64(v)
	case uint8:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return int64(f)
		}
	}
	return 0
}

func billingMapValue(value interface{}) (map[string]interface{}, bool) {
	m, ok := value.(map[string]interface{})
	return m, ok && m != nil
}

func attachBillingCNYReference(billing gin.H) {
	if billing == nil {
		return
	}
	displayCurrency := strings.TrimSpace(fmt.Sprint(billing["display_currency"]))
	amountUSD := numberToFloat64(billing["display_amount"])
	if amountUSD == 0 {
		quota := numberToFloat64(billing["quota"])
		quotaPerUnit := numberToFloat64(billing["quota_per_unit"])
		if quotaPerUnit > 0 {
			amountUSD = quota / quotaPerUnit
		}
	}
	for key, value := range billingservice.BuildBillingCNYReferenceFields(displayCurrency, amountUSD) {
		billing[key] = value
	}
}

func attachLedgerBillingCNYReference(billing gin.H, ledger *model.BillingLedger, allowDerivedAmount bool) bool {
	if billing == nil || ledger == nil ||
		!strings.EqualFold(strings.TrimSpace(ledger.Currency), common.BillingCurrencyUSD) ||
		!ledger.ExchangeRate.GreaterThan(decimal.Zero) {
		return false
	}

	storedAmountUSD, _ := ledger.Amount.Float64()
	amountUSD := storedAmountUSD
	if allowDerivedAmount {
		displayAmount, ok := billing["display_amount"]
		if ok {
			amountUSD = numberToFloat64(displayAmount)
		}
	}
	exchangeRate, _ := ledger.ExchangeRate.Float64()
	amountCNY, _ := ledger.AmountCNYReference.Float64()
	if allowDerivedAmount && (ledger.AmountCNYReference.IsZero() || amountUSD != storedAmountUSD) {
		amountCNY, _ = decimal.NewFromFloat(amountUSD).Mul(ledger.ExchangeRate).Round(8).Float64()
	}
	billing["amount_usd"] = amountUSD
	billing["amount_cny"] = amountCNY
	billing["amount_cny_text"] = billingservice.FormatBillingCNYAmountText(amountCNY)
	billing["exchange_rate"] = exchangeRate
	billing["exchange_rate_base"] = "USD"
	billing["exchange_rate_target"] = "CNY"
	billing["exchange_rate_source"] = "ledger_snapshot"
	billing["exchange_rate_stale"] = false
	billing["amount_cny_reference_stored"] = true

	if other, err := common.StrToMap(ledger.Other); err == nil {
		if reference, ok := other["cny_reference"].(map[string]interface{}); ok && reference != nil {
			if value, exists := reference["base"]; exists && strings.TrimSpace(fmt.Sprint(value)) != "" {
				billing["exchange_rate_base"] = value
			}
			if value, exists := reference["target"]; exists && strings.TrimSpace(fmt.Sprint(value)) != "" {
				billing["exchange_rate_target"] = value
			}
			if value, exists := reference["source"]; exists && strings.TrimSpace(fmt.Sprint(value)) != "" {
				billing["exchange_rate_source"] = value
			}
			if value, exists := reference["rate_updated_at"]; exists {
				billing["exchange_rate_updated_at"] = value
			}
			if value, exists := reference["stale"]; exists {
				billing["exchange_rate_stale"] = value
			}
			if value, exists := reference["backfilled"]; exists {
				billing["amount_cny_reference_backfilled"] = value
			}
			if value, exists := reference["policy"]; exists {
				billing["amount_cny_reference_policy"] = value
			}
		}
	}
	return true
}

func buildAuditQueryBilling(other string) gin.H {
	if other == "" {
		return nil
	}
	otherMap, err := common.StrToMap(other)
	if err != nil {
		return nil
	}
	rawBilling, ok := otherMap["billing"].(map[string]interface{})
	if !ok || rawBilling == nil {
		return nil
	}

	quota := numberToFloat64(rawBilling["quota"])
	quotaPerUnit := numberToFloat64(rawBilling["quota_per_unit"])
	displayAmountText := ""
	if rawBilling["display_amount"] != nil {
		displayAmountText = fmt.Sprint(rawBilling["display_amount"])
	}
	displayAmount := 0.0
	if quotaPerUnit > 0 {
		displayAmount = quota / quotaPerUnit
	}

	return gin.H{
		"billing_id":          rawBilling["billing_id"],
		"billing_type":        rawBilling["billing_type"],
		"quota":               rawBilling["quota"],
		"display_amount":      displayAmount,
		"display_amount_text": displayAmountText,
		"display_currency":    rawBilling["display_currency"],
		"quota_per_unit":      rawBilling["quota_per_unit"],
		"model":               rawBilling["model"],
		"channel_id":          rawBilling["channel_id"],
		"token_id":            rawBilling["token_id"],
		"group":               rawBilling["group"],
		"is_stream":           rawBilling["is_stream"],
		"server_region":       rawBilling["server_region"],
		"billing_source":      "legacy",
	}
}

func quotaDisplayAmount(quota int) float64 {
	if common.QuotaPerUnit <= 0 {
		return 0
	}
	return float64(quota) / common.QuotaPerUnit
}

func numberToFloat64(value interface{}) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case int32:
		return float64(v)
	case uint:
		return float64(v)
	case uint64:
		return float64(v)
	case uint32:
		return float64(v)
	case string:
		n, _ := strconv.ParseFloat(v, 64)
		return n
	default:
		return 0
	}
}

func GetAuditLogDetail(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	logRow, err := model.GetApiAuditLogById(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 记录管理员查看审计正文的操作（谁、何时、查看了哪条）。
	adminId := c.GetInt("id")
	adminName := c.GetString("username")
	common.SysLog("audit detail viewed: admin_id=" + strconv.Itoa(adminId) + " admin=" + adminName + " audit_log_id=" + strconv.FormatUint(id, 10))

	reqBody := resolveBody(c, logRow.BodyStorage, logRow.RequestBody, logRow.RequestLocalPath, logRow.RequestOssKey)
	respBody := resolveBody(c, logRow.BodyStorage, logRow.ResponseBody, logRow.ResponseLocalPath, logRow.ResponseOssKey)

	common.ApiSuccess(c, gin.H{
		"meta":          logRow,
		"request_body":  reqBody,
		"response_body": respBody,
	})
}

// resolveBody 按 body_storage 取正文：inline 直接解；二进制优先本地、回退 OSS。
func resolveBody(c *gin.Context, storage, inlineText, localPath, ossKey string) string {
	switch storage {
	case model.AuditBodyOmittedMeta, model.AuditBodyAbandoned:
		return "" // 无正文，仅元信息
	case model.AuditBodyInline:
		return decodeStoredText(inlineText)
	default:
		// local / local+oss：优先本地、回退 OSS。
		if localPath != "" {
			if data, err := common.ReadAuditSpoolFile(localPath); err == nil {
				return decodeBinaryStored(data)
			}
		}
		if ossKey != "" {
			if uploader, err := oss.GetUploader(); err == nil {
				if data, gerr := uploader.Get(context.Background(), ossKey); gerr == nil {
					return decodeBinaryStored(data)
				}
			}
		}
		return ""
	}
}

// decodeStoredText 还原 inline 文本：可能是加密 base64，也可能是明文。
func decodeStoredText(stored string) string {
	if stored == "" {
		return ""
	}
	raw, encrypted, err := common.AuditDecodeStored(stored)
	if err != nil {
		return ""
	}
	if !encrypted {
		return string(raw)
	}
	plain, derr := common.AuditDecrypt(raw)
	if derr != nil {
		return ""
	}
	return string(plain)
}

// decodeBinaryStored 还原落盘/OSS 的二进制正文：AES-GCM 解密 -> gzip 解压。
func decodeBinaryStored(data []byte) string {
	plain, err := common.AuditDecrypt(data)
	if err != nil {
		// 未加密时直接尝试解压
		plain = data
	}
	if un, derr := common.GzipDecompress(plain); derr == nil {
		return string(un)
	}
	return string(plain)
}

// DeleteAuditLog 删除单条审计日志。
func DeleteAuditLog(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		common.ApiErrorMsg(c, "invalid id")
		return
	}
	if err = model.DeleteAuditLogById(id); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// CleanupAuditLogs 手动触发过期清理。
func CleanupAuditLogs(c *gin.Context) {
	total := int64(0)
	for {
		n, err := model.CleanupExpiredAuditLogs(1000)
		if err != nil {
			common.ApiError(c, err)
			return
		}
		total += n
		if n < 1000 {
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"deleted": total,
	})
}
