package model

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

type Log struct {
	Id                 int    `json:"id" gorm:"index:idx_created_at_id,priority:1;index:idx_user_id_id,priority:2"`
	UserId             int    `json:"user_id" gorm:"index;index:idx_user_id_id,priority:1"`
	CreatedAt          int64  `json:"created_at" gorm:"bigint;index:idx_created_at_id,priority:2;index:idx_created_at_type"`
	Type               int    `json:"type" gorm:"index:idx_created_at_type"`
	Content            string `json:"content"`
	Username           string `json:"username" gorm:"index;index:index_username_model_name,priority:2;default:''"`
	TokenName          string `json:"token_name" gorm:"index;default:''"`
	ModelName          string `json:"model_name" gorm:"index;index:index_username_model_name,priority:1;default:''"`
	Quota              int    `json:"quota" gorm:"default:0"`
	PromptTokens       int    `json:"prompt_tokens" gorm:"default:0"`
	CompletionTokens   int    `json:"completion_tokens" gorm:"default:0"`
	UseTime            int    `json:"use_time" gorm:"default:0"`
	IsStream           bool   `json:"is_stream"`
	ChannelId          int    `json:"channel" gorm:"index"`
	ChannelName        string `json:"channel_name" gorm:"->"`
	TokenId            int    `json:"token_id" gorm:"default:0;index"`
	Group              string `json:"group" gorm:"index"`
	Ip                 string `json:"ip" gorm:"index;default:''"`
	RequestId          string `json:"request_id,omitempty" gorm:"type:varchar(64);index:idx_logs_request_id;default:''"`
	UpstreamRequestId  string `json:"upstream_request_id,omitempty" gorm:"type:varchar(128);index:idx_logs_upstream_request_id;default:''"`
	BillingMode        string `json:"billing_mode,omitempty" gorm:"type:varchar(32);index;default:''"`
	BillingExprMatched bool   `json:"billing_expr_matched,omitempty" gorm:"not null;default:false;index"`
	BillingExprHash    string `json:"billing_expr_hash,omitempty" gorm:"type:char(64);index;default:''"`
	BillingTier        string `json:"billing_tier,omitempty" gorm:"type:varchar(128);index;default:''"`
	Other              string `json:"other"`
}

// don't use iota, avoid change log type value
const (
	LogTypeUnknown = 0
	LogTypeTopup   = 1
	LogTypeConsume = 2
	LogTypeManage  = 3
	LogTypeSystem  = 4
	LogTypeError   = 5
	LogTypeRefund  = 6
)

func formatUserLogs(logs []*Log, startIdx int) {
	for i := range logs {
		logs[i].ChannelName = ""
		var otherMap map[string]interface{}
		otherMap, _ = common.StrToMap(logs[i].Other)
		if otherMap != nil {
			// Remove admin-only debug fields.
			delete(otherMap, "admin_info")
			// delete(otherMap, "reject_reason")
			delete(otherMap, "stream_status")
		}
		logs[i].Other = common.MapToJsonStr(otherMap)
		logs[i].Id = startIdx + i + 1
	}
}

func GetLogByTokenId(tokenId int) (logs []*Log, err error) {
	err = LOG_DB.Model(&Log{}).Where("token_id = ?", tokenId).Order("id desc").Limit(common.MaxRecentItems).Find(&logs).Error
	formatUserLogs(logs, 0)
	return logs, err
}

func RecordLog(userId int, logType int, content string) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

// RecordLogWithAdminInfo 记录操作日志，并将管理员相关信息存入 Other.admin_info，
func RecordLogWithAdminInfo(userId int, logType int, content string, adminInfo map[string]interface{}) {
	if logType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	username, _ := GetUsernameById(userId, false)
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      logType,
		Content:   content,
	}
	if len(adminInfo) > 0 {
		other := map[string]interface{}{
			"admin_info": adminInfo,
		}
		log.Other = common.MapToJsonStr(other)
	}
	if err := LOG_DB.Create(log).Error; err != nil {
		common.SysLog("failed to record log: " + err.Error())
	}
}

func RecordTopupLog(userId int, content string, callerIp string, paymentMethod string, callbackPaymentMethod string) {
	username, _ := GetUsernameById(userId, false)
	adminInfo := map[string]interface{}{
		"server_ip":               common.GetIp(),
		"node_name":               common.NodeName,
		"caller_ip":               callerIp,
		"payment_method":          paymentMethod,
		"callback_payment_method": callbackPaymentMethod,
		"version":                 common.Version,
	}
	other := map[string]interface{}{
		"admin_info": adminInfo,
	}
	log := &Log{
		UserId:    userId,
		Username:  username,
		CreatedAt: common.GetTimestamp(),
		Type:      LogTypeTopup,
		Content:   content,
		Ip:        callerIp,
		Other:     common.MapToJsonStr(other),
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		common.SysLog("failed to record topup log: " + err.Error())
	}
}

func RecordErrorLog(c *gin.Context, userId int, channelId int, modelName string, tokenName string, content string, tokenId int, useTimeSeconds int,
	isStream bool, group string, other map[string]interface{}) {
	logger.LogInfo(c, fmt.Sprintf("record error log: userId=%d, channelId=%d, modelName=%s, tokenName=%s, content=%s", userId, channelId, modelName, tokenName, content))
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	if other == nil {
		other = make(map[string]interface{})
	}
	annotateBillingEnvironment(other)
	otherStr := common.MapToJsonStr(other)
	billingMode, billingExprMatched, billingExprHash, billingTier := billingTraceFromOther(other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeError,
		Content:          content,
		PromptTokens:     0,
		CompletionTokens: 0,
		TokenName:        tokenName,
		ModelName:        modelName,
		Quota:            0,
		ChannelId:        channelId,
		TokenId:          tokenId,
		UseTime:          useTimeSeconds,
		IsStream:         isStream,
		Group:            group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:          requestId,
		UpstreamRequestId:  upstreamRequestId,
		BillingMode:        billingMode,
		BillingExprMatched: billingExprMatched,
		BillingExprHash:    billingExprHash,
		BillingTier:        billingTier,
		Other:              otherStr,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
	if requestId != "" {
		if err := UpsertBillingLedger(BillingLedgerUpsertParams{
			BillingID:       requestId,
			BillingType:     BillingLedgerTypeRequest,
			SourceRequestID: requestId,
			UserID:          userId,
			Username:        username,
			TokenID:         tokenId,
			TokenName:       tokenName,
			UserGroup:       group,
			ChannelID:       channelId,
			ModelName:       modelName,
			Quota:           0,
			Currency:        common.GetServerBillingCurrency(),
			ServerRegion:    common.GetServerRegion(),
			Status:          BillingLedgerStatusFailed,
			IsStream:        isStream,
			CreatedAt:       log.CreatedAt,
			SettledAt:       log.CreatedAt,
			Other:           other,
		}); err != nil {
			logger.LogError(c, "failed to record failed billing ledger: "+err.Error())
		}
	}
}

type RecordConsumeLogParams struct {
	ChannelId        int                    `json:"channel_id"`
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
	ModelName        string                 `json:"model_name"`
	TokenName        string                 `json:"token_name"`
	Quota            int                    `json:"quota"`
	Content          string                 `json:"content"`
	TokenId          int                    `json:"token_id"`
	UseTimeSeconds   int                    `json:"use_time_seconds"`
	IsStream         bool                   `json:"is_stream"`
	Group            string                 `json:"group"`
	UsageSource      string                 `json:"usage_source"`
	Other            map[string]interface{} `json:"other"`
}

func RecordConsumeLog(c *gin.Context, userId int, params RecordConsumeLogParams) {
	if !common.LogConsumeEnabled {
		return
	}
	// 审计计费：把最终扣费 quota 写入 context，供审计 middleware 组装 billing。
	// 只读已结算的最终值，不参与任何计费计算，对扣费主流程零影响。
	common.SetContextKey(c, constant.ContextKeyFinalQuota, params.Quota)
	common.SetContextKey(c, constant.ContextKeyBillingReady, true)
	username := c.GetString("username")
	requestId := c.GetString(common.RequestIdKey)
	upstreamRequestId := c.GetString(common.UpstreamRequestIdKey)
	if params.Other == nil {
		params.Other = make(map[string]interface{})
	}
	billingID := common.GetContextKeyString(c, constant.ContextKeyBillingId)
	if billingID == "" {
		billingID = requestId
	}
	billingType := common.GetContextKeyString(c, constant.ContextKeyBillingType)
	if billingType == "" {
		billingType = "request"
	}
	params.Other["billing_id"] = billingID
	params.Other["billing_type"] = billingType
	params.UsageSource = normalizeLogUsageSource(billingType, params.UsageSource, params.Other, params.Quota, params.PromptTokens, params.CompletionTokens)
	params.Other["usage_source"] = params.UsageSource
	annotateBillingEnvironment(params.Other)
	if billingType == "task" {
		params.Other["task_id"] = billingID
		params.Other["create_request_id"] = requestId
	}
	logger.LogInfo(c, fmt.Sprintf("record consume log: userId=%d, params=%s", userId, common.GetJsonString(params)))
	otherStr := common.MapToJsonStr(params.Other)
	billingMode, billingExprMatched, billingExprHash, billingTier := billingTraceFromOther(params.Other)
	// 判断是否需要记录 IP
	needRecordIp := false
	if settingMap, err := GetUserSetting(userId, false); err == nil {
		if settingMap.RecordIpLog {
			needRecordIp = true
		}
	}
	log := &Log{
		UserId:           userId,
		Username:         username,
		CreatedAt:        common.GetTimestamp(),
		Type:             LogTypeConsume,
		Content:          params.Content,
		PromptTokens:     params.PromptTokens,
		CompletionTokens: params.CompletionTokens,
		TokenName:        params.TokenName,
		ModelName:        params.ModelName,
		Quota:            params.Quota,
		ChannelId:        params.ChannelId,
		TokenId:          params.TokenId,
		UseTime:          params.UseTimeSeconds,
		IsStream:         params.IsStream,
		Group:            params.Group,
		Ip: func() string {
			if needRecordIp {
				return c.ClientIP()
			}
			return ""
		}(),
		RequestId:          requestId,
		UpstreamRequestId:  upstreamRequestId,
		BillingMode:        billingMode,
		BillingExprMatched: billingExprMatched,
		BillingExprHash:    billingExprHash,
		BillingTier:        billingTier,
		Other:              otherStr,
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		logger.LogError(c, "failed to record log: "+err.Error())
	}
	ledgerStatus := BillingLedgerStatusSettled
	ledgerSettledAt := log.CreatedAt
	if billingType == BillingLedgerTypeTask {
		ledgerStatus = BillingLedgerStatusPending
		ledgerSettledAt = 0
	}
	if err := UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:         billingID,
		BillingType:       billingType,
		SourceRequestID:   requestId,
		SourceTaskID:      taskIDFromOther(params.Other),
		UserID:            userId,
		Username:          username,
		TokenID:           params.TokenId,
		TokenName:         params.TokenName,
		UserGroup:         params.Group,
		ChannelID:         params.ChannelId,
		ModelName:         params.ModelName,
		UpstreamModelName: stringFromOther(params.Other, "upstream_model_name"),
		Quota:             int64(params.Quota),
		Currency:          common.GetServerBillingCurrency(),
		ServerRegion:      common.GetServerRegion(),
		Status:            ledgerStatus,
		UsageSource:       params.UsageSource,
		IsStream:          params.IsStream,
		PromptTokens:      int64(params.PromptTokens),
		CompletionTokens:  int64(params.CompletionTokens),
		CreatedAt:         log.CreatedAt,
		SettledAt:         ledgerSettledAt,
		Other:             params.Other,
	}); err != nil {
		logger.LogError(c, "failed to record billing ledger: "+err.Error())
	}
	if common.DataExportEnabled {
		gopool.Go(func() {
			LogQuotaData(userId, username, params.ModelName, params.Quota, common.GetTimestamp(), params.PromptTokens+params.CompletionTokens)
		})
	}
}

type RecordTaskBillingLogParams struct {
	UserId           int
	LogType          int
	Content          string
	ChannelId        int
	ModelName        string
	Quota            int
	TokenId          int
	Group            string
	UsageSource      string
	PromptTokens     int
	CompletionTokens int
	Other            map[string]interface{}
}

func RecordTaskBillingLog(params RecordTaskBillingLogParams) {
	if params.LogType == LogTypeConsume && !common.LogConsumeEnabled {
		return
	}
	if params.Other == nil {
		params.Other = make(map[string]interface{})
	}
	params.UsageSource = normalizeTaskBillingUsageSource(params)
	params.Other["usage_source"] = params.UsageSource
	annotateBillingEnvironment(params.Other)
	username, _ := GetUsernameById(params.UserId, false)
	tokenName := ""
	if params.TokenId > 0 {
		if token, err := GetTokenById(params.TokenId); err == nil {
			tokenName = token.Name
		}
	}
	billingMode, billingExprMatched, billingExprHash, billingTier := billingTraceFromOther(params.Other)
	log := &Log{
		UserId:             params.UserId,
		Username:           username,
		CreatedAt:          common.GetTimestamp(),
		Type:               params.LogType,
		Content:            params.Content,
		PromptTokens:       params.PromptTokens,
		CompletionTokens:   params.CompletionTokens,
		TokenName:          tokenName,
		ModelName:          params.ModelName,
		Quota:              params.Quota,
		ChannelId:          params.ChannelId,
		TokenId:            params.TokenId,
		Group:              params.Group,
		BillingMode:        billingMode,
		BillingExprMatched: billingExprMatched,
		BillingExprHash:    billingExprHash,
		BillingTier:        billingTier,
		Other:              common.MapToJsonStr(params.Other),
	}
	err := LOG_DB.Create(log).Error
	if err != nil {
		common.SysLog("failed to record task billing log: " + err.Error())
	}
	finalQuota, status := taskLedgerFinalQuotaAndStatus(params)
	if err := UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:         stringFromOther(params.Other, "billing_id"),
		BillingType:       BillingLedgerTypeTask,
		SourceRequestID:   stringFromOther(params.Other, "create_request_id"),
		SourceTaskID:      taskIDFromOther(params.Other),
		UserID:            params.UserId,
		Username:          username,
		TokenID:           params.TokenId,
		TokenName:         tokenName,
		UserGroup:         params.Group,
		ChannelID:         params.ChannelId,
		ModelName:         params.ModelName,
		UpstreamModelName: stringFromOther(params.Other, "upstream_model_name"),
		Quota:             finalQuota,
		Currency:          common.GetServerBillingCurrency(),
		ServerRegion:      common.GetServerRegion(),
		Status:            status,
		UsageSource:       params.UsageSource,
		IsStream:          false,
		PromptTokens:      int64(params.PromptTokens),
		CompletionTokens:  int64(params.CompletionTokens),
		CreatedAt:         log.CreatedAt,
		SettledAt:         log.CreatedAt,
		Other:             params.Other,
	}); err != nil {
		common.SysLog("failed to record task billing ledger: " + err.Error())
	}
}

func annotateBillingEnvironment(other map[string]interface{}) {
	if other == nil {
		return
	}
	other["server_region"] = common.GetServerRegion()
	other["currency"] = common.GetServerBillingCurrency()
}

func normalizeLogUsageSource(billingType string, source string, other map[string]interface{}, quota int, promptTokens int, completionTokens int) string {
	source = strings.TrimSpace(source)
	if source == "" {
		source = stringFromOther(other, "usage_source")
	}
	switch source {
	case BillingUsageSourceUpstream, BillingUsageSourceEstimated, BillingUsageSourcePerCall, BillingUsageSourceTaskActual, BillingUsageSourceNone:
		return source
	}
	if quota == 0 {
		return BillingUsageSourceNone
	}
	if billingType == BillingLedgerTypeTask {
		return BillingUsageSourcePerCall
	}
	if promptTokens+completionTokens > 0 {
		return BillingUsageSourceUpstream
	}
	return BillingUsageSourcePerCall
}

func normalizeTaskBillingUsageSource(params RecordTaskBillingLogParams) string {
	source := strings.TrimSpace(params.UsageSource)
	if source == "" {
		source = stringFromOther(params.Other, "usage_source")
	}
	switch source {
	case BillingUsageSourceUpstream, BillingUsageSourceEstimated, BillingUsageSourcePerCall, BillingUsageSourceTaskActual, BillingUsageSourceNone:
		return source
	}
	if params.LogType == LogTypeRefund || params.Quota == 0 {
		return BillingUsageSourceNone
	}
	if int64FromOther(params.Other, "actual_quota") >= 0 {
		return BillingUsageSourceTaskActual
	}
	return BillingUsageSourceTaskActual
}

func stringFromOther(other map[string]interface{}, key string) string {
	if other == nil {
		return ""
	}
	if value, ok := other[key]; ok && value != nil {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return ""
}

func boolFromOther(other map[string]interface{}, key string) bool {
	if other == nil {
		return false
	}
	value, ok := other[key]
	if !ok || value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "y":
			return true
		}
	case int:
		return v != 0
	case int64:
		return v != 0
	case int32:
		return v != 0
	case float64:
		return v != 0
	case float32:
		return v != 0
	}
	return false
}

func billingTraceFromOther(other map[string]interface{}) (mode string, matched bool, exprHash string, tier string) {
	mode = stringFromOther(other, "billing_mode")
	matched = boolFromOther(other, "billing_expr_matched")
	exprHash = stringFromOther(other, "billing_expr_hash")
	if exprHash == "" {
		exprHash = stringFromOther(other, "expr_hash")
	}
	tier = stringFromOther(other, "billing_tier")
	if tier == "" {
		tier = stringFromOther(other, "matched_tier")
	}
	return
}

func taskIDFromOther(other map[string]interface{}) string {
	if taskID := stringFromOther(other, "task_id"); taskID != "" {
		return taskID
	}
	return stringFromOther(other, "billing_id")
}

func taskLedgerFinalQuotaAndStatus(params RecordTaskBillingLogParams) (int64, string) {
	if actual := int64FromOther(params.Other, "actual_quota"); actual >= 0 {
		return actual, BillingLedgerStatusSettled
	}
	if params.LogType == LogTypeRefund {
		return 0, BillingLedgerStatusRefunded
	}
	return int64(params.Quota), BillingLedgerStatusSettled
}

func int64FromOther(other map[string]interface{}, key string) int64 {
	if other == nil {
		return -1
	}
	value, ok := other[key]
	if !ok || value == nil {
		return -1
	}
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err == nil {
			return n
		}
	}
	return -1
}

func GetAllLogs(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, startIdx int, num int, channel int, group string, requestId string, upstreamRequestId string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB
	} else {
		tx = LOG_DB.Where("logs.type = ?", logType)
	}

	tx = applyLogContainsFilter(tx, "logs.model_name", modelName)
	tx = applyLogContainsFilter(tx, "logs.username", username)
	tx = applyLogContainsFilter(tx, "logs.token_name", tokenName)
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if upstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", upstreamRequestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if channel != 0 {
		tx = tx.Where("logs.channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}
	err = tx.Order("logs.id desc").Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		return nil, 0, err
	}

	channelIds := types.NewSet[int]()
	for _, log := range logs {
		if log.ChannelId != 0 {
			channelIds.Add(log.ChannelId)
		}
	}

	if channelIds.Len() > 0 {
		var channels []struct {
			Id   int    `gorm:"column:id"`
			Name string `gorm:"column:name"`
		}
		if common.MemoryCacheEnabled {
			// Cache get channel
			for _, channelId := range channelIds.Items() {
				if cacheChannel, err := CacheGetChannel(channelId); err == nil {
					channels = append(channels, struct {
						Id   int    `gorm:"column:id"`
						Name string `gorm:"column:name"`
					}{
						Id:   channelId,
						Name: cacheChannel.Name,
					})
				}
			}
		} else {
			// Bulk query channels from DB
			if err = DB.Table("channels").Select("id, name").Where("id IN ?", channelIds.Items()).Find(&channels).Error; err != nil {
				return logs, total, err
			}
		}
		channelMap := make(map[int]string, len(channels))
		for _, channel := range channels {
			channelMap[channel.Id] = channel.Name
		}
		for i := range logs {
			logs[i].ChannelName = channelMap[logs[i].ChannelId]
		}
	}

	return logs, total, err
}

const logSearchCountLimit = 10000

func GetUserLogs(userId int, logType int, startTimestamp int64, endTimestamp int64, modelName string, tokenName string, startIdx int, num int, group string, requestId string, upstreamRequestId string) (logs []*Log, total int64, err error) {
	var tx *gorm.DB
	if logType == LogTypeUnknown {
		tx = LOG_DB.Where("logs.user_id = ?", userId)
	} else {
		tx = LOG_DB.Where("logs.user_id = ? and logs.type = ?", userId, logType)
	}

	tx = applyLogContainsFilter(tx, "logs.model_name", modelName)
	tx = applyLogContainsFilter(tx, "logs.token_name", tokenName)
	if requestId != "" {
		tx = tx.Where("logs.request_id = ?", requestId)
	}
	if upstreamRequestId != "" {
		tx = tx.Where("logs.upstream_request_id = ?", upstreamRequestId)
	}
	if startTimestamp != 0 {
		tx = tx.Where("logs.created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("logs.created_at <= ?", endTimestamp)
	}
	if group != "" {
		tx = tx.Where("logs."+logGroupCol+" = ?", group)
	}
	err = tx.Model(&Log{}).Limit(logSearchCountLimit).Count(&total).Error
	if err != nil {
		common.SysError("failed to count user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}
	err = tx.Order("logs.id desc").Limit(num).Offset(startIdx).Find(&logs).Error
	if err != nil {
		common.SysError("failed to search user logs: " + err.Error())
		return nil, 0, errors.New("查询日志失败")
	}

	formatUserLogs(logs, startIdx)
	return logs, total, err
}

type Stat struct {
	Quota int `json:"quota"`
	Rpm   int `json:"rpm"`
	Tpm   int `json:"tpm"`
}

func logContainsPattern(input string) (string, bool) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", false
	}

	replacer := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	return "%" + replacer.Replace(input) + "%", true
}

func applyLogContainsFilter(tx *gorm.DB, column string, value string) *gorm.DB {
	pattern, ok := logContainsPattern(value)
	if !ok {
		return tx
	}
	return tx.Where(column+" LIKE ? ESCAPE '!'", pattern)
}

func SumUsedQuota(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string, channel int, group string) (stat Stat, err error) {
	tx := LOG_DB.Table("logs").Select("sum(quota) quota")

	// 为rpm和tpm创建单独的查询
	rpmTpmQuery := LOG_DB.Table("logs").Select("count(*) rpm, sum(prompt_tokens) + sum(completion_tokens) tpm")

	tx = applyLogContainsFilter(tx, "username", username)
	rpmTpmQuery = applyLogContainsFilter(rpmTpmQuery, "username", username)
	tx = applyLogContainsFilter(tx, "token_name", tokenName)
	rpmTpmQuery = applyLogContainsFilter(rpmTpmQuery, "token_name", tokenName)
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	tx = applyLogContainsFilter(tx, "model_name", modelName)
	rpmTpmQuery = applyLogContainsFilter(rpmTpmQuery, "model_name", modelName)
	if channel != 0 {
		tx = tx.Where("channel_id = ?", channel)
		rpmTpmQuery = rpmTpmQuery.Where("channel_id = ?", channel)
	}
	if group != "" {
		tx = tx.Where(logGroupCol+" = ?", group)
		rpmTpmQuery = rpmTpmQuery.Where(logGroupCol+" = ?", group)
	}

	tx = tx.Where("type = ?", LogTypeConsume)
	rpmTpmQuery = rpmTpmQuery.Where("type = ?", LogTypeConsume)

	// 只统计最近60秒的rpm和tpm
	rpmTpmQuery = rpmTpmQuery.Where("created_at >= ?", time.Now().Add(-60*time.Second).Unix())

	// 执行查询
	if err := tx.Scan(&stat).Error; err != nil {
		common.SysError("failed to query log stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}
	if err := rpmTpmQuery.Scan(&stat).Error; err != nil {
		common.SysError("failed to query rpm/tpm stat: " + err.Error())
		return stat, errors.New("查询统计数据失败")
	}

	return stat, nil
}

func SumUsedToken(logType int, startTimestamp int64, endTimestamp int64, modelName string, username string, tokenName string) (token int) {
	tx := LOG_DB.Table("logs").Select("ifnull(sum(prompt_tokens),0) + ifnull(sum(completion_tokens),0)")
	if username != "" {
		tx = tx.Where("username = ?", username)
	}
	if tokenName != "" {
		tx = tx.Where("token_name = ?", tokenName)
	}
	if startTimestamp != 0 {
		tx = tx.Where("created_at >= ?", startTimestamp)
	}
	if endTimestamp != 0 {
		tx = tx.Where("created_at <= ?", endTimestamp)
	}
	if modelName != "" {
		tx = tx.Where("model_name = ?", modelName)
	}
	tx.Where("type = ?", LogTypeConsume).Scan(&token)
	return token
}

func DeleteOldLog(ctx context.Context, targetTimestamp int64, limit int) (int64, error) {
	var total int64 = 0

	for {
		if nil != ctx.Err() {
			return total, ctx.Err()
		}

		result := LOG_DB.Where("created_at < ?", targetTimestamp).Limit(limit).Delete(&Log{})
		if nil != result.Error {
			return total, result.Error
		}

		total += result.RowsAffected

		if result.RowsAffected < int64(limit) {
			break
		}
	}

	return total, nil
}
