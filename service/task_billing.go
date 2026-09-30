package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// LogTaskConsumption 记录任务消费日志和统计信息（仅记录，不涉及实际扣费）。
// 实际扣费已由 BillingSession（PreConsumeBilling + SettleBilling）完成。
func LogTaskConsumption(c *gin.Context, info *relaycommon.RelayInfo) {
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("操作 %s", info.Action)
	// 支持任务仅按次计费
	if common.StringsContains(constant.TaskPricePatches, info.OriginModelName) {
		logContent = fmt.Sprintf("%s，按次计费", logContent)
	} else {
		if len(info.PriceData.OtherRatios) > 0 {
			var contents []string
			for key, ra := range info.PriceData.OtherRatios {
				if 1.0 != ra {
					contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
				}
			}
			if len(contents) > 0 {
				logContent = fmt.Sprintf("%s, 计算参数：%s", logContent, strings.Join(contents, ", "))
			}
		}
	}
	other := make(map[string]interface{})
	other["is_task"] = true
	other["request_path"] = c.Request.URL.Path
	other["usage_source"] = model.BillingUsageSourcePerCall
	other["model_price"] = info.PriceData.ModelPrice
	if info.PriceData.ModelRatio > 0 {
		other["model_ratio"] = info.PriceData.ModelRatio
	}
	other["group_ratio"] = info.PriceData.GroupRatioInfo.GroupRatio
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other["user_group_ratio"] = info.PriceData.GroupRatioInfo.GroupSpecialRatio
	}
	appendGroupFallbackInfo(c, info, other)
	if info.IsModelMapped {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = info.UpstreamModelName
	}
	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId:   info.ChannelId,
		ModelName:   info.OriginModelName,
		TokenName:   tokenName,
		Quota:       info.PriceData.Quota,
		Content:     logContent,
		TokenId:     info.TokenId,
		Group:       info.UsingGroup,
		UsageSource: model.BillingUsageSourcePerCall,
		Other:       other,
	})
	model.UpdateUserUsedQuotaAndRequestCount(info.UserId, info.PriceData.Quota)
	model.UpdateChannelUsedQuota(info.ChannelId, info.PriceData.Quota)
}

// ---------------------------------------------------------------------------
// 异步任务计费辅助函数
// ---------------------------------------------------------------------------

// resolveTokenKey 通过 TokenId 运行时获取令牌 Key（用于 Redis 缓存操作）。
// 如果令牌已被删除或查询失败，返回空字符串。
func resolveTokenKey(ctx context.Context, tokenId int, taskID string) string {
	token, err := model.GetTokenById(tokenId)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("获取令牌 key 失败 (tokenId=%d, task=%s): %s", tokenId, taskID, err.Error()))
		return ""
	}
	return token.Key
}

// taskIsSubscription 判断任务是否通过订阅计费。
func taskIsSubscription(task *model.Task) bool {
	return task.PrivateData.BillingSource == BillingSourceSubscription && task.PrivateData.SubscriptionId > 0
}

// taskAdjustFunding 调整任务的资金来源（钱包或订阅），delta > 0 表示扣费，delta < 0 表示退还。
func taskAdjustFunding(task *model.Task, delta int) error {
	if taskIsSubscription(task) {
		return model.PostConsumeUserSubscriptionDelta(task.PrivateData.SubscriptionId, int64(delta))
	}
	if delta > 0 {
		return model.DecreaseUserQuota(task.UserId, delta, false)
	}
	return model.IncreaseUserQuota(task.UserId, -delta, false)
}

// taskAdjustTokenQuota 调整任务的令牌额度，delta > 0 表示扣费，delta < 0 表示退还。
// 需要通过 resolveTokenKey 运行时获取 key（不从 PrivateData 中读取）。
func taskAdjustTokenQuota(ctx context.Context, task *model.Task, delta int) {
	if task.PrivateData.TokenId <= 0 || delta == 0 {
		return
	}
	tokenKey := resolveTokenKey(ctx, task.PrivateData.TokenId, task.TaskID)
	if tokenKey == "" {
		return
	}
	var err error
	if delta > 0 {
		err = model.DecreaseTokenQuota(task.PrivateData.TokenId, tokenKey, delta)
	} else {
		err = model.IncreaseTokenQuota(task.PrivateData.TokenId, tokenKey, -delta)
	}
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("调整令牌额度失败 (delta=%d, task=%s): %s", delta, task.TaskID, err.Error()))
	}
}

// taskBillingOther 从 task 的 BillingContext 构建日志 Other 字段。
func taskBillingOther(task *model.Task) map[string]interface{} {
	other := make(map[string]interface{})
	other["billing_id"] = task.TaskID
	other["billing_type"] = "task"
	other["task_id"] = task.TaskID
	if bc := task.PrivateData.BillingContext; bc != nil {
		if bc.BillingMode != "" {
			other["billing_mode"] = bc.BillingMode
		}
		if snap := bc.TieredBillingSnapshot; snap != nil {
			if snap.BillingMode != "" {
				other["billing_mode"] = snap.BillingMode
			}
			other["billing_expr_hash"] = snap.ExprHash
			other["estimated_tier"] = snap.EstimatedTier
			if snap.BillingMode == "tiered_expr" {
				other["billing_expr_matched"] = strings.TrimSpace(snap.EstimatedTier) != ""
				other["billing_tier"] = strings.TrimSpace(snap.EstimatedTier)
			}
		}
		other["model_price"] = bc.ModelPrice
		if bc.ModelRatio > 0 {
			other["model_ratio"] = bc.ModelRatio
		}
		other["group_ratio"] = bc.GroupRatio
		if len(bc.OtherRatios) > 0 {
			for k, v := range bc.OtherRatios {
				other[k] = v
			}
		}
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = props.UpstreamModelName
	}
	if props.CreateRequestID != "" {
		other["create_request_id"] = props.CreateRequestID
	}
	return other
}

// taskModelName 从 BillingContext 或 Properties 中获取模型名称。
func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		return bc.OriginModelName
	}
	return task.Properties.OriginModelName
}

func taskTokenName(task *model.Task) string {
	if task == nil || task.PrivateData.TokenId <= 0 {
		return ""
	}
	token, err := model.GetTokenById(task.PrivateData.TokenId)
	if err != nil || token == nil {
		return ""
	}
	return token.Name
}

type taskBillingUsage struct {
	PromptTokens     int64
	CompletionTokens int64
	TotalTokens      int64
	Usage            map[string]interface{}
}

func (u taskBillingUsage) hasTokens() bool {
	return u.PromptTokens > 0 || u.CompletionTokens > 0 || u.TotalTokens > 0
}

func (u *taskBillingUsage) merge(next taskBillingUsage) {
	if next.PromptTokens > u.PromptTokens {
		u.PromptTokens = next.PromptTokens
	}
	if next.CompletionTokens > u.CompletionTokens {
		u.CompletionTokens = next.CompletionTokens
	}
	if next.TotalTokens > u.TotalTokens {
		u.TotalTokens = next.TotalTokens
	}
	if next.Usage != nil {
		if u.Usage == nil {
			u.Usage = make(map[string]interface{}, len(next.Usage))
		}
		for k, v := range next.Usage {
			u.Usage[k] = v
		}
	}
}

func (u *taskBillingUsage) normalize() {
	if u.TotalTokens == 0 && (u.PromptTokens > 0 || u.CompletionTokens > 0) {
		u.TotalTokens = u.PromptTokens + u.CompletionTokens
	}
	if u.TotalTokens > 0 {
		if u.PromptTokens == 0 && u.CompletionTokens == 0 {
			u.CompletionTokens = u.TotalTokens
		} else if u.CompletionTokens == 0 && u.TotalTokens >= u.PromptTokens {
			u.CompletionTokens = u.TotalTokens - u.PromptTokens
		}
	}
	if u.TotalTokens > u.PromptTokens+u.CompletionTokens {
		u.PromptTokens += u.TotalTokens - u.PromptTokens - u.CompletionTokens
	}
	if !u.hasTokens() {
		return
	}
	if u.Usage == nil {
		u.Usage = map[string]interface{}{}
	}
	u.Usage["prompt_tokens"] = u.PromptTokens
	u.Usage["completion_tokens"] = u.CompletionTokens
	u.Usage["total_tokens"] = u.TotalTokens
}

func taskBillingUsageFromTask(task *model.Task) taskBillingUsage {
	if task == nil || len(task.Data) == 0 {
		return taskBillingUsage{}
	}
	var payload map[string]interface{}
	if err := common.Unmarshal(task.Data, &payload); err != nil {
		return taskBillingUsage{}
	}
	usage := taskBillingUsageFromMap(payload, 0)
	usage.normalize()
	return usage
}

func taskBillingUsageFromTaskInfo(taskResult *relaycommon.TaskInfo) taskBillingUsage {
	if taskResult == nil {
		return taskBillingUsage{}
	}
	usage := taskBillingUsage{
		PromptTokens:     int64(taskResult.PromptTokens),
		CompletionTokens: int64(taskResult.CompletionTokens),
		TotalTokens:      int64(taskResult.TotalTokens),
	}
	usage.normalize()
	return usage
}

func taskBillingUsageFromMap(payload map[string]interface{}, depth int) taskBillingUsage {
	if payload == nil || depth > 4 {
		return taskBillingUsage{}
	}
	usage := taskBillingUsage{}
	usage.merge(taskBillingUsageFromUsageMap(payload))
	for _, key := range []string{"usage", "metadata", "data", "result", "response", "content"} {
		child, ok := taskBillingMapValue(payload[key])
		if !ok {
			continue
		}
		if key == "usage" {
			usage.merge(taskBillingUsageFromUsageMap(child))
		}
		usage.merge(taskBillingUsageFromMap(child, depth+1))
	}
	return usage
}

func taskBillingUsageFromUsageMap(payload map[string]interface{}) taskBillingUsage {
	if payload == nil {
		return taskBillingUsage{}
	}
	promptTokens := firstTaskBillingInt64(payload, "prompt_tokens", "input_tokens")
	completionTokens := firstTaskBillingInt64(payload, "completion_tokens", "output_tokens")
	totalTokens := firstTaskBillingInt64(payload, "total_tokens")
	if promptTokens == 0 && completionTokens == 0 && totalTokens == 0 {
		return taskBillingUsage{}
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
	return taskBillingUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		Usage:            usage,
	}
}

func firstTaskBillingInt64(payload map[string]interface{}, keys ...string) int64 {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			if n := taskBillingInt64(value); n > 0 {
				return n
			}
		}
	}
	return 0
}

func taskBillingInt64(value interface{}) int64 {
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

func taskBillingMapValue(value interface{}) (map[string]interface{}, bool) {
	m, ok := value.(map[string]interface{})
	return m, ok && m != nil
}

func addTaskBillingUsageToOther(other map[string]interface{}, usage taskBillingUsage) taskBillingUsage {
	usage.normalize()
	if other == nil || !usage.hasTokens() {
		return usage
	}
	other["prompt_tokens"] = usage.PromptTokens
	other["completion_tokens"] = usage.CompletionTokens
	other["total_tokens"] = usage.TotalTokens
	if usage.Usage != nil {
		other["usage"] = usage.Usage
	}
	return usage
}

func attachTaskResultBillingUsage(task *model.Task, taskResult *relaycommon.TaskInfo) {
	if task == nil || taskResult == nil {
		return
	}
	usage := taskBillingUsageFromTask(task)
	usage.merge(taskBillingUsageFromTaskInfo(taskResult))
	usage.normalize()
	if !usage.hasTokens() {
		return
	}
	var payload map[string]interface{}
	if len(task.Data) > 0 {
		_ = common.Unmarshal(task.Data, &payload)
	}
	if payload == nil {
		payload = map[string]interface{}{}
	}
	usageMap, _ := taskBillingMapValue(payload["usage"])
	if usageMap == nil {
		usageMap = map[string]interface{}{}
	}
	for k, v := range usage.Usage {
		usageMap[k] = v
	}
	payload["usage"] = usageMap
	if b, err := common.Marshal(payload); err == nil {
		task.Data = b
	}
}

func setTaskBillingLedgerStatus(ctx context.Context, task *model.Task, finalQuota int, status string, reason string, extra map[string]interface{}) {
	if task == nil || strings.TrimSpace(task.TaskID) == "" {
		return
	}
	other := taskBillingOther(task)
	for k, v := range extra {
		other[k] = v
	}
	if reason != "" {
		other["reason"] = reason
	}
	usage := addTaskBillingUsageToOther(other, taskBillingUsageFromTask(task))
	usageSource := model.BillingUsageSourceTaskActual
	if status == model.BillingLedgerStatusPending {
		usageSource = model.BillingUsageSourcePerCall
	} else if status == model.BillingLedgerStatusRefunded || status == model.BillingLedgerStatusFailed || finalQuota == 0 {
		usageSource = model.BillingUsageSourceNone
	}
	if source, ok := other["usage_source"].(string); ok {
		switch source {
		case model.BillingUsageSourceUpstream, model.BillingUsageSourceEstimated, model.BillingUsageSourcePerCall, model.BillingUsageSourceTaskActual, model.BillingUsageSourceNone:
			usageSource = source
		}
	}
	other["usage_source"] = usageSource
	now := common.GetTimestamp()
	settledAt := int64(0)
	if model.IsBillingLedgerFinalStatus(status) {
		settledAt = now
	}
	username, _ := model.GetUsernameById(task.UserId, false)
	if err := model.UpsertBillingLedger(model.BillingLedgerUpsertParams{
		BillingID:         task.TaskID,
		BillingType:       model.BillingLedgerTypeTask,
		SourceRequestID:   task.Properties.CreateRequestID,
		SourceTaskID:      task.TaskID,
		UserID:            task.UserId,
		Username:          username,
		TokenID:           task.PrivateData.TokenId,
		TokenName:         taskTokenName(task),
		UserGroup:         task.Group,
		ChannelID:         task.ChannelId,
		ModelName:         taskModelName(task),
		UpstreamModelName: task.Properties.UpstreamModelName,
		Quota:             int64(finalQuota),
		Currency:          common.GetServerBillingCurrency(),
		ServerRegion:      common.GetServerRegion(),
		Status:            status,
		UsageSource:       usageSource,
		IsStream:          false,
		PromptTokens:      usage.PromptTokens,
		CompletionTokens:  usage.CompletionTokens,
		CreatedAt:         now,
		SettledAt:         settledAt,
		Other:             other,
	}); err != nil {
		logger.LogError(ctx, fmt.Sprintf("update task billing ledger failed task %s: %s", task.TaskID, err.Error()))
	}
}

func resetTaskBillingLedgerPending(ctx context.Context, task *model.Task, reason string) {
	setTaskBillingLedgerStatus(ctx, task, task.Quota, model.BillingLedgerStatusPending, reason, nil)
}

func SettleTaskQuotaExact(ctx context.Context, task *model.Task, reason string) {
	settleTaskQuotaExactWithExtra(ctx, task, reason, nil)
}

func settleTaskQuotaExactWithExtra(ctx context.Context, task *model.Task, reason string, extra map[string]interface{}) {
	if task == nil {
		return
	}
	if !startTaskBillingFinalization(ctx, task.TaskID, "settle_exact") {
		return
	}
	usageSource := model.BillingUsageSourceTaskActual
	if task.PrivateData.BillingContext != nil && task.PrivateData.BillingContext.PerCallBilling {
		usageSource = model.BillingUsageSourcePerCall
	}
	statusExtra := map[string]interface{}{
		"pre_consumed_quota": task.Quota,
		"actual_quota":       task.Quota,
		"usage_source":       usageSource,
	}
	for k, v := range extra {
		statusExtra[k] = v
	}
	setTaskBillingLedgerStatus(ctx, task, task.Quota, model.BillingLedgerStatusSettled, reason, statusExtra)
}

// RefundTaskQuota 统一的任务失败退款逻辑。
// 当异步任务失败时，将预扣的 quota 退还给用户（支持钱包和订阅），并退还令牌额度。
func RefundTaskQuota(ctx context.Context, task *model.Task, reason string) {
	if task == nil {
		return
	}
	quota := task.Quota
	if !startTaskBillingFinalization(ctx, task.TaskID, "refund") {
		return
	}
	refundExtra := youchuanTaskCostLedgerExtra(task)
	if refundExtra == nil {
		refundExtra = make(map[string]interface{})
	}
	refundExtra["pre_consumed_quota"] = quota
	refundExtra["refunded_quota"] = quota
	if quota == 0 {
		setTaskBillingLedgerStatus(ctx, task, 0, model.BillingLedgerStatusRefunded, reason, refundExtra)
		return
	}

	// 1. 退还资金来源（钱包或订阅）
	if err := taskAdjustFunding(task, -quota); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("退还资金来源失败 task %s: %s", task.TaskID, err.Error()))
		resetTaskBillingLedgerPending(ctx, task, reason)
		return
	}

	// 2. 退还令牌额度
	taskAdjustTokenQuota(ctx, task, -quota)

	// 3. 记录日志
	other := taskBillingOther(task)
	for key, value := range refundExtra {
		other[key] = value
	}
	other["task_id"] = task.TaskID
	other["reason"] = reason
	other["usage_source"] = model.BillingUsageSourceNone
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:      task.UserId,
		LogType:     model.LogTypeRefund,
		Content:     "",
		ChannelId:   task.ChannelId,
		ModelName:   taskModelName(task),
		Quota:       quota,
		TokenId:     task.PrivateData.TokenId,
		Group:       task.Group,
		UsageSource: model.BillingUsageSourceNone,
		Other:       other,
	})
}

// RecalculateTaskQuota 通用的异步差额结算。
// actualQuota 是任务完成后的实际应扣额度，与预扣额度 (task.Quota) 做差额结算。
// reason 用于日志记录（例如 "token重算" 或 "adaptor调整"）。
func RecalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason string) {
	recalculateTaskQuotaWithExtra(ctx, task, actualQuota, reason, nil)
}

func recalculateTaskQuotaWithExtra(ctx context.Context, task *model.Task, actualQuota int, reason string, extra map[string]interface{}) {
	if actualQuota <= 0 {
		return
	}
	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota

	if quotaDelta == 0 {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 预扣费准确（%s，%s）",
			task.TaskID, logger.LogQuota(actualQuota), reason))
		settleTaskQuotaExactWithExtra(ctx, task, reason, extra)
		return
	}
	if !startTaskBillingFinalization(ctx, task.TaskID, "settle") {
		return
	}

	logger.LogInfo(ctx, fmt.Sprintf("任务 %s 差额结算：delta=%s（实际：%s，预扣：%s，%s）",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))

	// 调整资金来源
	if err := taskAdjustFunding(task, quotaDelta); err != nil {
		logger.LogError(ctx, fmt.Sprintf("差额结算资金调整失败 task %s: %s", task.TaskID, err.Error()))
		resetTaskBillingLedgerPending(ctx, task, reason)
		return
	}

	// 调整令牌额度
	taskAdjustTokenQuota(ctx, task, quotaDelta)

	task.Quota = actualQuota

	var logType int
	var logQuota int
	if quotaDelta > 0 {
		logType = model.LogTypeConsume
		logQuota = quotaDelta
		model.UpdateUserUsedQuotaAndRequestCount(task.UserId, quotaDelta)
		model.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)
	} else {
		logType = model.LogTypeRefund
		logQuota = -quotaDelta
	}
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["pre_consumed_quota"] = preConsumedQuota
	other["actual_quota"] = actualQuota
	other["usage_source"] = model.BillingUsageSourceTaskActual
	for k, v := range extra {
		other[k] = v
	}
	usageSource := model.BillingUsageSourceTaskActual
	if source, ok := other["usage_source"].(string); ok {
		switch source {
		case model.BillingUsageSourceUpstream, model.BillingUsageSourceEstimated, model.BillingUsageSourcePerCall, model.BillingUsageSourceTaskActual, model.BillingUsageSourceNone:
			usageSource = source
		}
	}
	usage := addTaskBillingUsageToOther(other, taskBillingUsageFromTask(task))
	if err := task.UpdateQuota(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("差额结算任务额度落库失败 task %s: %s", task.TaskID, err.Error()))
		return
	}
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:           task.UserId,
		LogType:          logType,
		Content:          reason,
		ChannelId:        task.ChannelId,
		ModelName:        taskModelName(task),
		Quota:            logQuota,
		TokenId:          task.PrivateData.TokenId,
		Group:            task.Group,
		UsageSource:      usageSource,
		PromptTokens:     int(usage.PromptTokens),
		CompletionTokens: int(usage.CompletionTokens),
		Other:            other,
	})
	setTaskBillingLedgerStatus(ctx, task, actualQuota, model.BillingLedgerStatusSettled, reason, other)
}

func RecalculateTaskQuotaByTieredTokens(ctx context.Context, task *model.Task, completionTokens int) bool {
	if task == nil || completionTokens <= 0 {
		return false
	}
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.TieredBillingSnapshot == nil || bc.TieredBillingSnapshot.BillingMode != "tiered_expr" {
		return false
	}

	usage := taskBillingUsageFromTask(task)
	usage.normalize()
	params := billingexpr.TokenParams{
		P:   float64(usage.PromptTokens),
		C:   float64(usage.CompletionTokens),
		Len: float64(usage.PromptTokens),
	}
	if params.C <= 0 {
		params.C = float64(completionTokens)
	}

	requestInput := billingexpr.RequestInput{}
	if bc.BillingRequestInput != nil {
		requestInput = *bc.BillingRequestInput
	}

	tieredExtra := map[string]interface{}{}
	InjectTieredTokenParams(tieredExtra, params, nil)
	result, err := billingexpr.ComputeTieredQuotaWithRequest(bc.TieredBillingSnapshot, params, requestInput)
	if err != nil {
		actualQuota := bc.TieredBillingSnapshot.EstimatedQuotaAfterGroup
		tieredExtra["billing_expr_matched"] = false
		tieredExtra["tiered_error"] = err.Error()
		tieredExtra["tiered_quota_after_group"] = actualQuota
		reason := fmt.Sprintf("tiered_expr token重算失败，使用预估额度：tokens=%d, err=%s", completionTokens, err.Error())
		recalculateTaskQuotaWithExtra(ctx, task, actualQuota, reason, tieredExtra)
		return true
	}

	reason := fmt.Sprintf("tiered_expr token重算：tokens=%d, tier=%s", completionTokens, result.MatchedTier)
	tieredExtra["billing_expr_matched"] = true
	tieredExtra["matched_tier"] = result.MatchedTier
	tieredExtra["billing_tier"] = result.MatchedTier
	tieredExtra["tiered_quota_before_group"] = result.ActualQuotaBeforeGroup
	tieredExtra["tiered_quota_after_group"] = result.ActualQuotaAfterGroup
	recalculateTaskQuotaWithExtra(ctx, task, result.ActualQuotaAfterGroup, reason, tieredExtra)
	return true
}

// RecalculateTaskQuotaByTokens 根据实际 token 消耗重新计费（异步差额结算）。
// 当任务成功且返回了 totalTokens 时，根据模型倍率和分组倍率重新计算实际扣费额度，
// 与预扣费的差额进行补扣或退还。支持钱包和订阅计费来源。
func RecalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) {
	if totalTokens <= 0 {
		return
	}

	modelName := taskModelName(task)

	// 获取模型价格和倍率
	modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(modelName)
	// 只有配置了倍率(非固定价格)时才按 token 重新计费
	if !hasRatioSetting || modelRatio <= 0 {
		return
	}

	// 获取用户和组的倍率信息
	group := task.Group
	if group == "" {
		user, err := model.GetUserById(task.UserId, false)
		if err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return
	}

	groupRatio := ratio_setting.GetGroupRatio(group)
	userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group)

	var finalGroupRatio float64
	if hasUserGroupRatio {
		finalGroupRatio = userGroupRatio
	} else {
		finalGroupRatio = groupRatio
	}

	// 计算 OtherRatios 乘积（视频折扣、时长等）
	otherMultiplier := 1.0
	if bc := task.PrivateData.BillingContext; bc != nil {
		for _, r := range bc.OtherRatios {
			if r != 1.0 && r > 0 {
				otherMultiplier *= r
			}
		}
	}

	// 计算实际应扣费额度: totalTokens * modelRatio * groupRatio * otherMultiplier
	actualQuota := int(float64(totalTokens) * modelRatio * finalGroupRatio * otherMultiplier)

	reason := fmt.Sprintf("token重算：tokens=%d, modelRatio=%.2f, groupRatio=%.2f, otherMultiplier=%.4f", totalTokens, modelRatio, finalGroupRatio, otherMultiplier)
	RecalculateTaskQuota(ctx, task, actualQuota, reason)
}
