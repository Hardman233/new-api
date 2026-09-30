package service

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

func claimBillingLedger(c *gin.Context, relayInfo *relaycommon.RelayInfo, estimatedQuota int) *types.NewAPIError {
	if relayInfo == nil {
		return nil
	}
	params := billingLedgerParamsFromRelay(c, relayInfo, estimatedQuota, model.BillingLedgerStatusPending)
	if params.BillingID == "" {
		return nil
	}
	claimed, existing, err := model.TryCreatePendingBillingLedger(params)
	if err != nil {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("claim billing ledger failed: %w", err),
			types.ErrorCodeUpdateDataError,
			http.StatusInternalServerError,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if claimed {
		relayInfo.BillingLedgerClaimed = true
		return nil
	}
	relayInfo.BillingIdempotentRejected = true
	status := ""
	if existing != nil {
		status = existing.Status
	}
	return types.NewErrorWithStatusCode(
		fmt.Errorf("duplicate billing_id %s, current status: %s", params.BillingID, status),
		types.ErrorCodeInvalidRequest,
		http.StatusConflict,
		types.ErrOptionWithSkipRetry(),
		types.ErrOptionWithNoRecordErrorLog(),
	)
}

func markClaimedBillingLedgerFailed(c *gin.Context, relayInfo *relaycommon.RelayInfo, reason string) {
	if relayInfo == nil || !relayInfo.BillingLedgerClaimed {
		return
	}
	params := billingLedgerParamsFromRelay(c, relayInfo, 0, model.BillingLedgerStatusFailed)
	if params.BillingID == "" {
		return
	}
	params.SettledAt = common.GetTimestamp()
	if params.Other == nil {
		params.Other = map[string]interface{}{}
	}
	params.Other["failure_reason"] = reason
	if err := model.UpsertBillingLedger(params); err != nil {
		logger.LogError(c, "mark claimed billing ledger failed: "+err.Error())
	}
}

func MarkClaimedBillingLedgerFailed(c *gin.Context, relayInfo *relaycommon.RelayInfo, reason string) {
	markClaimedBillingLedgerFailed(c, relayInfo, reason)
}

func billingLedgerParamsFromRelay(c *gin.Context, relayInfo *relaycommon.RelayInfo, quota int, status string) model.BillingLedgerUpsertParams {
	billingID := strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyBillingId))
	if billingID == "" && relayInfo != nil {
		billingID = strings.TrimSpace(relayInfo.RequestId)
	}
	if billingID == "" {
		billingID = strings.TrimSpace(c.GetString(common.RequestIdKey))
	}
	billingType := strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyBillingType))
	if billingType == "" {
		billingType = model.BillingLedgerTypeRequest
	}

	requestID := strings.TrimSpace(c.GetString(common.RequestIdKey))
	if requestID == "" && relayInfo != nil {
		requestID = strings.TrimSpace(relayInfo.RequestId)
	}
	sourceTaskID := ""
	if billingType == model.BillingLedgerTypeTask {
		sourceTaskID = billingID
	}

	channelID := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
	channelName := common.GetContextKeyString(c, constant.ContextKeyChannelName)
	modelName := ""
	upstreamModelName := ""
	tokenID := common.GetContextKeyInt(c, constant.ContextKeyTokenId)
	userID := common.GetContextKeyInt(c, constant.ContextKeyUserId)
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	isStream := common.GetContextKeyBool(c, constant.ContextKeyIsStream)
	if relayInfo != nil {
		if channelMeta := relayInfo.ChannelMeta; channelMeta != nil {
			if channelMeta.ChannelId > 0 {
				channelID = channelMeta.ChannelId
			}
			if channelMeta.ChannelBaseUrl != "" && channelName == "" {
				channelName = common.GetContextKeyString(c, constant.ContextKeyChannelName)
			}
			upstreamModelName = channelMeta.UpstreamModelName
		}
		modelName = relayInfo.OriginModelName
		tokenID = relayInfo.TokenId
		userID = relayInfo.UserId
		group = relayInfo.UsingGroup
		if group == "" {
			group = relayInfo.UserGroup
		}
		isStream = relayInfo.IsStream
	}

	other := map[string]interface{}{
		"billing_id":   billingID,
		"billing_type": billingType,
	}
	usageSource := model.BillingUsageSourceEstimated
	if billingType == model.BillingLedgerTypeTask {
		other["task_id"] = billingID
		other["create_request_id"] = requestID
		usageSource = model.BillingUsageSourcePerCall
	}
	if relayInfo != nil && relayInfo.PriceData.UsePrice {
		usageSource = model.BillingUsageSourcePerCall
	}
	if quota == 0 {
		usageSource = model.BillingUsageSourceNone
	}
	other["usage_source"] = usageSource

	return model.BillingLedgerUpsertParams{
		BillingID:         billingID,
		BillingType:       billingType,
		SourceRequestID:   requestID,
		SourceTaskID:      sourceTaskID,
		UserID:            userID,
		Username:          c.GetString("username"),
		TokenID:           tokenID,
		TokenName:         c.GetString("token_name"),
		UserGroup:         group,
		ChannelID:         channelID,
		ChannelName:       channelName,
		ModelName:         modelName,
		UpstreamModelName: upstreamModelName,
		Quota:             int64(quota),
		Currency:          common.GetServerBillingCurrency(),
		ServerRegion:      common.GetServerRegion(),
		Status:            status,
		UsageSource:       usageSource,
		IsStream:          isStream,
		CreatedAt:         common.GetTimestamp(),
		Other:             other,
	}
}

func startTaskBillingFinalization(ctx context.Context, billingID string, action string) bool {
	claimed, existing, err := model.TryStartBillingLedgerFinalization(billingID)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("start task billing finalization failed (billing_id=%s, action=%s): %s", billingID, action, err.Error()))
		return false
	}
	if claimed {
		return true
	}
	status := ""
	if existing != nil {
		status = existing.Status
	}
	logger.LogInfo(ctx, fmt.Sprintf("skip duplicate task billing finalization (billing_id=%s, status=%s, action=%s)", billingID, status, action))
	return false
}
