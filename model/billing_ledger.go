package model

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var billingLedgerTableReady atomic.Bool

type BillingCNYReferenceSnapshot struct {
	Rate       float64
	Base       string
	Target     string
	UpdatedAt  int64
	Source     string
	Stale      bool
	CapturedAt int64
}

type BillingCNYReferenceProvider func() (BillingCNYReferenceSnapshot, bool)

var (
	billingCNYReferenceProviderMu sync.RWMutex
	billingCNYReferenceProvider   BillingCNYReferenceProvider
)

func SetBillingCNYReferenceProvider(provider BillingCNYReferenceProvider) func() {
	billingCNYReferenceProviderMu.Lock()
	previous := billingCNYReferenceProvider
	billingCNYReferenceProvider = provider
	billingCNYReferenceProviderMu.Unlock()
	return func() {
		billingCNYReferenceProviderMu.Lock()
		billingCNYReferenceProvider = previous
		billingCNYReferenceProviderMu.Unlock()
	}
}

const (
	BillingLedgerTypeRequest    = "request"
	BillingLedgerTypeTask       = "task"
	BillingLedgerTypeAdjustment = "adjustment"
	BillingLedgerTypeRefund     = "refund"

	BillingLedgerStatusPending    = "pending"
	BillingLedgerStatusFinalizing = "finalizing"
	BillingLedgerStatusSettled    = "settled"
	BillingLedgerStatusRefunded   = "refunded"
	BillingLedgerStatusFailed     = "failed"

	BillingUsageSourceUpstream   = "upstream_usage"
	BillingUsageSourceEstimated  = "estimated"
	BillingUsageSourcePerCall    = "per_call"
	BillingUsageSourceTaskActual = "task_actual"
	BillingUsageSourceNone       = "none"
)

type BillingLedger struct {
	Id uint64 `json:"id" gorm:"primaryKey"`

	BillingID       string `json:"billing_id" gorm:"type:varchar(192);not null;uniqueIndex:uk_billing_id"`
	BillingType     string `json:"billing_type" gorm:"type:varchar(32);not null;index:idx_billing_type"`
	SourceRequestID string `json:"source_request_id" gorm:"type:varchar(64);not null;default:'';index"`
	SourceTaskID    string `json:"source_task_id" gorm:"type:varchar(128);not null;default:'';index"`

	UserID    int    `json:"user_id" gorm:"column:user_id;not null;default:0;index:idx_user_time,priority:1"`
	Username  string `json:"username" gorm:"type:varchar(64);not null;default:''"`
	TokenID   int    `json:"token_id" gorm:"column:token_id;not null;default:0;index:idx_token_time,priority:1"`
	TokenName string `json:"token_name" gorm:"type:varchar(128);not null;default:''"`
	UserGroup string `json:"user_group" gorm:"type:varchar(64);not null;default:'';index"`

	ChannelID    int    `json:"channel_id" gorm:"column:channel_id;not null;default:0;index:idx_channel_time,priority:1"`
	ChannelName  string `json:"channel_name" gorm:"type:varchar(128);not null;default:''"`
	SupplierName string `json:"supplier_name" gorm:"type:varchar(128);not null;default:'';index:idx_supplier_currency_time,priority:1"`
	ServerRegion string `json:"server_region" gorm:"type:varchar(32);not null;default:'';index:idx_region_time,priority:1"`

	ModelName         string `json:"model_name" gorm:"type:varchar(128);not null;default:'';index:idx_model_time,priority:1"`
	UpstreamModelName string `json:"upstream_model_name" gorm:"type:varchar(128);not null;default:''"`

	Quota              int64           `json:"quota" gorm:"not null;default:0"`
	Currency           string          `json:"currency" gorm:"type:varchar(16);not null;default:'';index:idx_supplier_currency_time,priority:2"`
	Amount             decimal.Decimal `json:"amount" gorm:"type:decimal(20,8);not null;default:0"`
	AmountText         string          `json:"amount_text" gorm:"type:varchar(64);not null;default:''"`
	ExchangeRate       decimal.Decimal `json:"exchange_rate" gorm:"type:decimal(20,8);not null;default:0"`
	AmountCNYReference decimal.Decimal `json:"amount_cny_reference" gorm:"type:decimal(20,8);not null;default:0"`

	Status             string `json:"status" gorm:"type:varchar(32);not null;default:'settled';index:idx_status_time,priority:1"`
	UsageSource        string `json:"usage_source" gorm:"type:varchar(32);not null;default:'';index"`
	IsStream           bool   `json:"is_stream" gorm:"not null;default:false"`
	PromptTokens       int64  `json:"prompt_tokens" gorm:"not null;default:0"`
	CompletionTokens   int64  `json:"completion_tokens" gorm:"not null;default:0"`
	BillingMode        string `json:"billing_mode,omitempty" gorm:"type:varchar(32);not null;default:'';index"`
	BillingExprMatched bool   `json:"billing_expr_matched,omitempty" gorm:"not null;default:false;index"`
	BillingExprHash    string `json:"billing_expr_hash,omitempty" gorm:"type:char(64);not null;default:'';index"`
	BillingTier        string `json:"billing_tier,omitempty" gorm:"type:varchar(128);not null;default:'';index"`
	BillingMatchInfo   string `json:"billing_match_info,omitempty" gorm:"type:text"`
	BillingCalcDetail  string `json:"billing_calc_detail,omitempty" gorm:"type:text"`

	RequestHash  string `json:"request_hash" gorm:"type:char(64);not null;default:''"`
	ResponseHash string `json:"response_hash" gorm:"type:char(64);not null;default:''"`
	CreatedAt    int64  `json:"created_at" gorm:"not null;index:idx_created_at;index:idx_supplier_currency_time,priority:3;index:idx_channel_time,priority:2;index:idx_user_time,priority:2;index:idx_token_time,priority:2;index:idx_model_time,priority:2;index:idx_region_time,priority:2;index:idx_status_time,priority:2"`
	SettledAt    int64  `json:"settled_at" gorm:"not null;default:0"`
	Other        string `json:"other" gorm:"type:text"`
}

func (BillingLedger) TableName() string {
	return "billing_ledger"
}

type BillingLedgerUpsertParams struct {
	BillingID               string
	BillingType             string
	SourceRequestID         string
	SourceTaskID            string
	UserID                  int
	Username                string
	TokenID                 int
	TokenName               string
	UserGroup               string
	ChannelID               int
	ChannelName             string
	SupplierName            string
	ServerRegion            string
	ModelName               string
	UpstreamModelName       string
	Quota                   int64
	Currency                string
	ExchangeRate            decimal.Decimal
	AmountCNYReference      decimal.Decimal
	SkipCNYReferenceCapture bool
	Status                  string
	UsageSource             string
	IsStream                bool
	PromptTokens            int64
	CompletionTokens        int64
	RequestHash             string
	ResponseHash            string
	CreatedAt               int64
	SettledAt               int64
	Other                   map[string]interface{}
}

func UpsertBillingLedger(params BillingLedgerUpsertParams) error {
	params.BillingID = strings.TrimSpace(params.BillingID)
	if params.BillingID == "" {
		return nil
	}
	if !billingLedgerTableAvailable() {
		return nil
	}
	normalizeBillingLedgerParams(&params)
	preserveExistingBillingCNYReference(&params)
	ledger := newBillingLedgerFromParams(&params)
	assignments := map[string]interface{}{
		"billing_type":         ledger.BillingType,
		"source_request_id":    ledger.SourceRequestID,
		"source_task_id":       ledger.SourceTaskID,
		"user_id":              ledger.UserID,
		"username":             ledger.Username,
		"token_id":             ledger.TokenID,
		"token_name":           ledger.TokenName,
		"user_group":           ledger.UserGroup,
		"channel_id":           ledger.ChannelID,
		"channel_name":         ledger.ChannelName,
		"supplier_name":        ledger.SupplierName,
		"server_region":        ledger.ServerRegion,
		"model_name":           ledger.ModelName,
		"upstream_model_name":  ledger.UpstreamModelName,
		"quota":                ledger.Quota,
		"currency":             ledger.Currency,
		"amount":               ledger.Amount,
		"amount_text":          ledger.AmountText,
		"exchange_rate":        ledger.ExchangeRate,
		"amount_cny_reference": ledger.AmountCNYReference,
		"status":               ledger.Status,
		"usage_source":         ledger.UsageSource,
		"is_stream":            ledger.IsStream,
		"prompt_tokens":        ledger.PromptTokens,
		"completion_tokens":    ledger.CompletionTokens,
		"billing_mode":         ledger.BillingMode,
		"billing_expr_matched": ledger.BillingExprMatched,
		"billing_expr_hash":    ledger.BillingExprHash,
		"billing_tier":         ledger.BillingTier,
		"billing_match_info":   ledger.BillingMatchInfo,
		"billing_calc_detail":  ledger.BillingCalcDetail,
		"request_hash":         ledger.RequestHash,
		"response_hash":        ledger.ResponseHash,
		"settled_at":           ledger.SettledAt,
		"other":                ledger.Other,
	}
	err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "billing_id"}},
		DoUpdates: clause.Assignments(assignments),
	}).Create(&ledger).Error
	if isMissingBillingLedgerTableError(err) {
		return nil
	}
	if err == nil {
		MaybeEnqueueBillingSync(params.BillingID, params.Status)
	}
	return err
}

func preserveExistingBillingCNYReference(params *BillingLedgerUpsertParams) {
	if params == nil || !params.ExchangeRate.IsZero() ||
		!shouldPersistBillingCNYReference(params) {
		return
	}
	existing, err := GetBillingLedgerByBillingID(params.BillingID)
	if err != nil || existing == nil || !existing.ExchangeRate.GreaterThan(decimal.Zero) {
		return
	}
	params.ExchangeRate = existing.ExchangeRate
	if params.Other == nil {
		params.Other = map[string]interface{}{}
	}
	if _, exists := params.Other["cny_reference"]; exists {
		return
	}
	if existingOther, parseErr := common.StrToMap(existing.Other); parseErr == nil {
		if reference, exists := existingOther["cny_reference"]; exists {
			params.Other["cny_reference"] = reference
		}
	}
}

func TryCreatePendingBillingLedger(params BillingLedgerUpsertParams) (bool, *BillingLedger, error) {
	params.BillingID = strings.TrimSpace(params.BillingID)
	if params.BillingID == "" {
		return true, nil, nil
	}
	if !billingLedgerTableAvailable() {
		return true, nil, nil
	}
	params.Status = BillingLedgerStatusPending
	ledger := newBillingLedgerFromParams(&params)
	result := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "billing_id"}},
		DoNothing: true,
	}).Create(&ledger)
	if isMissingBillingLedgerTableError(result.Error) {
		return true, nil, nil
	}
	if result.Error != nil {
		return false, nil, result.Error
	}
	if result.RowsAffected > 0 {
		return true, &ledger, nil
	}
	existing, err := GetBillingLedgerByBillingID(params.BillingID)
	if err != nil {
		return false, nil, err
	}
	return false, existing, nil
}

func IsBillingLedgerFinalStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case BillingLedgerStatusSettled, BillingLedgerStatusRefunded, BillingLedgerStatusFailed:
		return true
	default:
		return false
	}
}

func IsBillingLedgerClosedForAdjustment(status string) bool {
	status = strings.TrimSpace(status)
	return status == BillingLedgerStatusFinalizing || IsBillingLedgerFinalStatus(status)
}

func TryStartBillingLedgerFinalization(billingID string) (bool, *BillingLedger, error) {
	billingID = strings.TrimSpace(billingID)
	if billingID == "" {
		return true, nil, nil
	}
	if !billingLedgerTableAvailable() {
		return true, nil, nil
	}
	result := DB.Model(&BillingLedger{}).
		Where("billing_id = ? AND status = ?", billingID, BillingLedgerStatusPending).
		Update("status", BillingLedgerStatusFinalizing)
	if isMissingBillingLedgerTableError(result.Error) {
		return true, nil, nil
	}
	if result.Error != nil {
		return false, nil, result.Error
	}
	if result.RowsAffected > 0 {
		ledger, err := GetBillingLedgerByBillingID(billingID)
		if err != nil {
			return true, nil, nil
		}
		return true, ledger, nil
	}
	existing, err := GetBillingLedgerByBillingID(billingID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return true, nil, nil
	}
	if err != nil {
		return false, nil, err
	}
	if IsBillingLedgerClosedForAdjustment(existing.Status) {
		return false, existing, nil
	}
	return false, existing, nil
}

func newBillingLedgerFromParams(params *BillingLedgerUpsertParams) BillingLedger {
	normalizeBillingLedgerParams(params)
	amount := common.BillingDisplayAmountFromQuota(params.Quota)
	amountDecimal := decimal.NewFromFloat(amount).Round(8)
	exchangeRate, amountCNYReference := resolveBillingCNYReference(params, amountDecimal)
	billingMode, billingExprMatched, billingExprHash, billingTier := billingTraceFromOther(params.Other)
	if billingMode == "" {
		billingMode = inferBillingLedgerMode(params)
	}
	billingMatchInfo, billingCalcDetail := billingLedgerEvidence(params, amountDecimal, billingMode, billingExprMatched, billingExprHash, billingTier)
	return BillingLedger{
		BillingID:          params.BillingID,
		BillingType:        params.BillingType,
		SourceRequestID:    params.SourceRequestID,
		SourceTaskID:       params.SourceTaskID,
		UserID:             params.UserID,
		Username:           params.Username,
		TokenID:            params.TokenID,
		TokenName:          params.TokenName,
		UserGroup:          params.UserGroup,
		ChannelID:          params.ChannelID,
		ChannelName:        params.ChannelName,
		SupplierName:       params.SupplierName,
		ServerRegion:       params.ServerRegion,
		ModelName:          params.ModelName,
		UpstreamModelName:  params.UpstreamModelName,
		Quota:              params.Quota,
		Currency:           params.Currency,
		Amount:             amountDecimal,
		AmountText:         common.FormatBillingAmountText(amount, params.Currency),
		ExchangeRate:       exchangeRate,
		AmountCNYReference: amountCNYReference,
		Status:             params.Status,
		UsageSource:        params.UsageSource,
		IsStream:           params.IsStream,
		PromptTokens:       params.PromptTokens,
		CompletionTokens:   params.CompletionTokens,
		BillingMode:        billingMode,
		BillingExprMatched: billingExprMatched,
		BillingExprHash:    billingExprHash,
		BillingTier:        billingTier,
		BillingMatchInfo:   billingMatchInfo,
		BillingCalcDetail:  billingCalcDetail,
		RequestHash:        params.RequestHash,
		ResponseHash:       params.ResponseHash,
		CreatedAt:          params.CreatedAt,
		SettledAt:          params.SettledAt,
		Other:              common.MapToJsonStr(params.Other),
	}
}

func resolveBillingCNYReference(params *BillingLedgerUpsertParams, amount decimal.Decimal) (decimal.Decimal, decimal.Decimal) {
	if !shouldPersistBillingCNYReference(params) {
		return decimal.Zero, decimal.Zero
	}

	exchangeRate := params.ExchangeRate.Round(8)
	amountCNYReference := params.AmountCNYReference.Round(8)
	if exchangeRate.GreaterThan(decimal.Zero) {
		if amountCNYReference.IsZero() {
			amountCNYReference = amount.Mul(exchangeRate).Round(8)
		}
		updateBillingCNYReferenceAmounts(params, amount, exchangeRate, amountCNYReference)
		return exchangeRate, amountCNYReference
	}
	if params.SkipCNYReferenceCapture {
		return decimal.Zero, decimal.Zero
	}

	billingCNYReferenceProviderMu.RLock()
	provider := billingCNYReferenceProvider
	billingCNYReferenceProviderMu.RUnlock()
	if provider == nil {
		return decimal.Zero, decimal.Zero
	}
	snapshot, ok := provider()
	if !ok || snapshot.Rate <= 0 {
		return decimal.Zero, decimal.Zero
	}

	exchangeRate = decimal.NewFromFloat(snapshot.Rate).Round(8)
	amountCNYReference = amount.Mul(exchangeRate).Round(8)
	if snapshot.CapturedAt <= 0 {
		snapshot.CapturedAt = common.GetTimestamp()
	}
	params.Other["cny_reference"] = map[string]interface{}{
		"schema_version":  1,
		"policy":          "settlement_snapshot",
		"backfilled":      false,
		"amount_usd":      amount.StringFixed(8),
		"amount_cny":      amountCNYReference.StringFixed(8),
		"exchange_rate":   exchangeRate.StringFixed(8),
		"base":            strings.ToUpper(strings.TrimSpace(snapshot.Base)),
		"target":          strings.ToUpper(strings.TrimSpace(snapshot.Target)),
		"rate_updated_at": snapshot.UpdatedAt,
		"captured_at":     snapshot.CapturedAt,
		"source":          strings.TrimSpace(snapshot.Source),
		"stale":           snapshot.Stale,
	}
	return exchangeRate, amountCNYReference
}

func shouldPersistBillingCNYReference(params *BillingLedgerUpsertParams) bool {
	return params != nil &&
		strings.EqualFold(strings.TrimSpace(params.ServerRegion), common.ServerRegionOverseas) &&
		strings.EqualFold(strings.TrimSpace(params.Currency), common.BillingCurrencyUSD) &&
		IsBillingLedgerFinalStatus(params.Status)
}

func updateBillingCNYReferenceAmounts(params *BillingLedgerUpsertParams, amount, exchangeRate, amountCNYReference decimal.Decimal) {
	if params == nil {
		return
	}
	if params.Other == nil {
		params.Other = map[string]interface{}{}
	}
	reference, _ := params.Other["cny_reference"].(map[string]interface{})
	if reference == nil {
		reference = map[string]interface{}{
			"schema_version": 1,
			"policy":         "provided_snapshot",
			"backfilled":     false,
			"base":           common.BillingCurrencyUSD,
			"target":         common.BillingCurrencyCNY,
		}
		params.Other["cny_reference"] = reference
	}
	reference["amount_usd"] = amount.StringFixed(8)
	reference["amount_cny"] = amountCNYReference.StringFixed(8)
	reference["exchange_rate"] = exchangeRate.StringFixed(8)
}

func billingLedgerEvidence(params *BillingLedgerUpsertParams, amount decimal.Decimal, mode string, exprMatched bool, exprHash string, tier string) (string, string) {
	if raw := billingLedgerJSONFieldFromOther(params.Other, "billing_match_info"); raw != "" {
		matchRaw := raw
		detailRaw := billingLedgerJSONFieldFromOther(params.Other, "billing_calc_detail")
		if detailRaw == "" {
			detailRaw = billingLedgerJSONFieldFromOther(params.Other, "billing_calculation_detail")
		}
		if detailRaw != "" {
			return matchRaw, detailRaw
		}
	}

	match := buildBillingMatchInfo(params, mode, exprMatched, exprHash, tier)
	detail := buildBillingCalcDetail(params, amount, match)
	return billingLedgerJSON(match), billingLedgerJSON(detail)
}

func buildBillingMatchInfo(params *BillingLedgerUpsertParams, mode string, exprMatched bool, exprHash string, tier string) map[string]interface{} {
	if mode == "" {
		mode = inferBillingLedgerMode(params)
	}
	matched := params.Status == BillingLedgerStatusSettled && params.Quota != 0
	if mode == "tiered_expr" {
		matched = exprMatched
	}
	if params.Status == BillingLedgerStatusFailed || params.Status == BillingLedgerStatusRefunded || params.UsageSource == BillingUsageSourceNone {
		matched = false
	}

	info := map[string]interface{}{
		"schema_version":      1,
		"mode":                mode,
		"matched":             matched,
		"status":              params.Status,
		"usage_source":        params.UsageSource,
		"billing_type":        params.BillingType,
		"billing_id":          params.BillingID,
		"model_name":          params.ModelName,
		"upstream_model_name": params.UpstreamModelName,
		"channel_id":          params.ChannelID,
		"channel_name":        params.ChannelName,
		"user_group":          params.UserGroup,
		"currency":            params.Currency,
		"quota":               params.Quota,
		"quota_per_unit":      common.QuotaPerUnit,
		"match_source":        billingMatchSource(mode),
	}
	if exprHash != "" {
		info["expr_hash"] = exprHash
	}
	if tier != "" {
		info["tier"] = tier
	}
	if estimatedTier := stringFromOther(params.Other, "estimated_tier"); estimatedTier != "" {
		info["estimated_tier"] = estimatedTier
	}
	if reason := stringFromOther(params.Other, "reason"); reason != "" {
		info["reason"] = reason
	}
	if failureReason := stringFromOther(params.Other, "failure_reason"); failureReason != "" {
		info["failure_reason"] = failureReason
	}
	if errMsg := stringFromOther(params.Other, "error"); errMsg != "" {
		info["error"] = errMsg
	}
	return info
}

func buildBillingCalcDetail(params *BillingLedgerUpsertParams, amount decimal.Decimal, match map[string]interface{}) map[string]interface{} {
	mode, _ := match["mode"].(string)
	usage := map[string]interface{}{
		"prompt_tokens":     params.PromptTokens,
		"completion_tokens": params.CompletionTokens,
		"total_tokens":      params.PromptTokens + params.CompletionTokens,
	}
	copyBillingOtherValue(params.Other, usage, "tiered_token_usage")
	copyBillingOtherValue(params.Other, usage, "usage")
	copyBillingOtherValue(params.Other, usage, "input_tokens_total")
	copyBillingOtherValue(params.Other, usage, "image_output")
	copyBillingOtherValue(params.Other, usage, "audio_input_token_count")

	pricing := billingPricingSnapshot(params.Other)
	tiered := billingTieredSnapshot(params.Other)

	detail := map[string]interface{}{
		"schema_version": 1,
		"mode":           mode,
		"formula":        billingFormulaForMode(mode),
		"usage":          usage,
		"pricing":        pricing,
		"tiered":         tiered,
		"result": map[string]interface{}{
			"quota":          params.Quota,
			"amount":         amount.StringFixed(8),
			"amount_text":    common.FormatBillingAmountText(amount.InexactFloat64(), params.Currency),
			"currency":       params.Currency,
			"quota_per_unit": common.QuotaPerUnit,
		},
		"steps": []map[string]interface{}{
			{
				"name":    "match_pricing_rule",
				"mode":    mode,
				"matched": match["matched"],
				"tier":    match["tier"],
			},
			{
				"name":  "collect_usage",
				"usage": usage,
			},
			{
				"name":    "apply_pricing",
				"formula": billingFormulaForMode(mode),
				"pricing": pricing,
				"tiered":  tiered,
			},
			{
				"name":           "quota_to_amount",
				"formula":        "amount = quota / quota_per_unit",
				"quota":          params.Quota,
				"quota_per_unit": common.QuotaPerUnit,
				"amount":         amount.StringFixed(8),
				"currency":       params.Currency,
			},
		},
	}
	if preConsumed := int64FromOther(params.Other, "pre_consumed_quota"); preConsumed >= 0 {
		detail["pre_consumed_quota"] = preConsumed
	}
	if actual := int64FromOther(params.Other, "actual_quota"); actual >= 0 {
		detail["actual_quota"] = actual
	}
	if exprB64 := stringFromOther(params.Other, "expr_b64"); exprB64 != "" {
		tiered["expr_b64"] = exprB64
	}
	return detail
}

func inferBillingLedgerMode(params *BillingLedgerUpsertParams) string {
	if params == nil {
		return ""
	}
	if mode := stringFromOther(params.Other, "billing_mode"); mode != "" {
		return mode
	}
	if params.Status == BillingLedgerStatusFailed || params.Status == BillingLedgerStatusRefunded || params.Quota == 0 || params.UsageSource == BillingUsageSourceNone {
		return BillingUsageSourceNone
	}
	if params.BillingType == BillingLedgerTypeTask {
		if int64FromOther(params.Other, "actual_quota") >= 0 {
			return BillingUsageSourceTaskActual
		}
		return BillingUsageSourcePerCall
	}
	if params.UsageSource == BillingUsageSourcePerCall || billingLedgerFloatFromOther(params.Other, "model_price") > 0 {
		return BillingUsageSourcePerCall
	}
	return "ratio"
}

func billingMatchSource(mode string) string {
	switch mode {
	case "tiered_expr":
		return "billing_expr"
	case BillingUsageSourcePerCall:
		return "model_price"
	case BillingUsageSourceTaskActual:
		return "task_actual_usage"
	case BillingUsageSourceNone:
		return "none"
	default:
		return "model_ratio"
	}
}

func billingFormulaForMode(mode string) string {
	switch mode {
	case "tiered_expr":
		return "quota = round((expr_result / 1000000 * quota_per_unit) * group_ratio + surcharges)"
	case BillingUsageSourcePerCall:
		return "quota = round(model_price * quota_per_unit * group_ratio + surcharges)"
	case BillingUsageSourceTaskActual:
		return "quota = actual task quota settled from upstream usage or task result"
	case BillingUsageSourceNone:
		return "quota = 0"
	default:
		return "quota = round(((text_input + cache*cache_ratio + image_input*image_ratio + audio_input*audio_price + completion*completion_ratio) * model_ratio * group_ratio * other_ratios) + surcharges)"
	}
}

func billingPricingSnapshot(other map[string]interface{}) map[string]interface{} {
	pricing := map[string]interface{}{}
	for key, value := range other {
		if value == nil {
			continue
		}
		lower := strings.ToLower(key)
		if strings.Contains(lower, "ratio") || strings.Contains(lower, "price") || strings.Contains(lower, "quota") {
			pricing[key] = value
		}
	}
	return pricing
}

func billingTieredSnapshot(other map[string]interface{}) map[string]interface{} {
	tiered := map[string]interface{}{}
	for _, key := range []string{
		"billing_expr_hash",
		"estimated_tier",
		"matched_tier",
		"billing_tier",
		"tiered_quota_before_group",
		"tiered_quota_after_group",
		"tiered_token_usage",
		"tiered_expr_params",
		"tiered_used_vars",
		"expr_b64",
	} {
		copyBillingOtherValue(other, tiered, key)
	}
	return tiered
}

func copyBillingOtherValue(source map[string]interface{}, target map[string]interface{}, key string) {
	if value, ok := billingOtherValue(source, key); ok {
		target[key] = value
	}
}

func billingOtherValue(other map[string]interface{}, key string) (interface{}, bool) {
	if other == nil {
		return nil, false
	}
	value, ok := other[key]
	if !ok || value == nil {
		return nil, false
	}
	return value, true
}

func billingLedgerJSONFieldFromOther(other map[string]interface{}, key string) string {
	value, ok := billingOtherValue(other, key)
	if !ok {
		return ""
	}
	if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
		return strings.TrimSpace(s)
	}
	return billingLedgerJSON(value)
}

func billingLedgerJSON(value interface{}) string {
	bytes, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(bytes)
}

func billingLedgerFloatFromOther(other map[string]interface{}, key string) float64 {
	value, ok := billingOtherValue(other, key)
	if !ok {
		return 0
	}
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
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err == nil {
			return f
		}
	}
	return 0
}

func normalizeBillingLedgerParams(params *BillingLedgerUpsertParams) {
	params.BillingType = strings.TrimSpace(params.BillingType)
	if params.BillingType == "" {
		params.BillingType = BillingLedgerTypeRequest
	}
	params.Status = strings.TrimSpace(params.Status)
	if params.Status == "" {
		params.Status = BillingLedgerStatusSettled
	}
	params.ServerRegion = strings.TrimSpace(params.ServerRegion)
	if params.ServerRegion == "" {
		params.ServerRegion = common.GetServerRegion()
	}
	params.Currency = strings.ToUpper(strings.TrimSpace(params.Currency))
	if params.Currency == "" {
		params.Currency = common.GetServerBillingCurrency()
	}
	if params.CreatedAt == 0 {
		params.CreatedAt = common.GetTimestamp()
	}
	if params.SettledAt == 0 && params.Status == BillingLedgerStatusSettled {
		params.SettledAt = params.CreatedAt
	}
	params.UsageSource = normalizeBillingUsageSource(params)
	if params.ChannelName == "" && params.ChannelID > 0 {
		if ch, err := GetChannelById(params.ChannelID, false); err == nil && ch != nil {
			params.ChannelName = ch.Name
		}
	}
	if params.SupplierName == "" {
		params.SupplierName = ResolveSupplierName(params.ChannelName)
	}
	if params.Other == nil {
		params.Other = map[string]interface{}{}
	}
	params.Other["server_region"] = params.ServerRegion
	params.Other["currency"] = params.Currency
	params.Other["supplier_name"] = params.SupplierName
	params.Other["usage_source"] = params.UsageSource
}

func normalizeBillingUsageSource(params *BillingLedgerUpsertParams) string {
	source := strings.TrimSpace(params.UsageSource)
	if source == "" && params.Other != nil {
		source = stringFromOther(params.Other, "usage_source")
	}
	switch source {
	case BillingUsageSourceUpstream, BillingUsageSourceEstimated, BillingUsageSourcePerCall, BillingUsageSourceTaskActual, BillingUsageSourceNone:
		return source
	}
	if params.Status == BillingLedgerStatusFailed || params.Status == BillingLedgerStatusRefunded || params.Quota == 0 {
		return BillingUsageSourceNone
	}
	if params.BillingType == BillingLedgerTypeTask {
		if params.Other != nil && int64FromOther(params.Other, "actual_quota") >= 0 {
			return BillingUsageSourceTaskActual
		}
		return BillingUsageSourcePerCall
	}
	if params.PromptTokens+params.CompletionTokens > 0 {
		return BillingUsageSourceUpstream
	}
	return BillingUsageSourcePerCall
}

func GetBillingLedgerByBillingID(billingID string) (*BillingLedger, error) {
	if !billingLedgerTableAvailable() {
		return nil, gorm.ErrRecordNotFound
	}
	var ledger BillingLedger
	err := DB.Where("billing_id = ?", strings.TrimSpace(billingID)).First(&ledger).Error
	if isMissingBillingLedgerTableError(err) {
		return nil, gorm.ErrRecordNotFound
	}
	if err != nil {
		return nil, err
	}
	return &ledger, nil
}

func billingLedgerTableAvailable() bool {
	if billingLedgerTableReady.Load() {
		return true
	}
	if DB == nil {
		return false
	}
	if DB.Migrator().HasTable(&BillingLedger{}) {
		billingLedgerTableReady.Store(true)
		return true
	}
	return false
}

func isMissingBillingLedgerTableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") ||
		strings.Contains(msg, "does not exist") ||
		strings.Contains(msg, "1146")
}

func ResolveSupplierName(channelName string) string {
	channelName = strings.TrimSpace(channelName)
	if channelName == "" {
		return ""
	}
	if strings.HasPrefix(channelName, "[") {
		parts := strings.Split(channelName, "]")
		if len(parts) >= 2 {
			candidate := strings.TrimPrefix(strings.TrimSpace(parts[1]), "[")
			candidate = strings.TrimSpace(candidate)
			if candidate != "" {
				return candidate
			}
		}
	}
	knownSuppliers := []string{"网宿", "熊大", "DeepSeek", "OpenAI", "Azure", "Claude", "Gemini", "Qwen", "Moonshot", "Volcengine"}
	lower := strings.ToLower(channelName)
	for _, supplier := range knownSuppliers {
		if strings.Contains(lower, strings.ToLower(supplier)) {
			return supplier
		}
	}
	fields := strings.Fields(channelName)
	if len(fields) > 0 {
		return fields[0]
	}
	return channelName
}

type SupplierMatchedChannel struct {
	Id   int    `json:"id"`
	Name string `json:"name"`
}

type SupplierStatsQuery struct {
	SupplierKeyword string
	StartTimestamp  int64
	EndTimestamp    int64
	ModelName       string
	UserGroup       string
	TokenName       string
	ServerRegion    string
	Currency        string
	Source          string
}

type SupplierStatsRow struct {
	SupplierName     string  `json:"supplier_name,omitempty"`
	ChannelID        int     `json:"channel_id,omitempty"`
	ChannelName      string  `json:"channel_name,omitempty"`
	ModelName        string  `json:"model_name,omitempty"`
	Currency         string  `json:"currency"`
	ConsumeQuota     int64   `json:"consume_quota"`
	RefundQuota      int64   `json:"refund_quota"`
	NetQuota         int64   `json:"net_quota"`
	Amount           float64 `json:"amount"`
	RequestCount     int64   `json:"request_count"`
	PromptTokens     int64   `json:"prompt_tokens,omitempty"`
	CompletionTokens int64   `json:"completion_tokens,omitempty"`
	TotalTokens      int64   `json:"total_tokens,omitempty"`
}

type SupplierStatsResult struct {
	SupplierKeyword   string                   `json:"supplier_keyword"`
	Source            string                   `json:"source"`
	MatchedChannels   []SupplierMatchedChannel `json:"matched_channels"`
	SummaryByCurrency []SupplierStatsRow       `json:"summary_by_currency"`
	BySupplier        []SupplierStatsRow       `json:"by_supplier"`
	ByChannel         []SupplierStatsRow       `json:"by_channel"`
	ByModel           []SupplierStatsRow       `json:"by_model"`
}

func FindChannelsBySupplierKeyword(keyword string, limit int) ([]SupplierMatchedChannel, error) {
	pattern, ok := logContainsPattern(keyword)
	if !ok {
		return nil, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	var channels []SupplierMatchedChannel
	err := DB.Table("channels").
		Select("id, name").
		Where("name LIKE ? ESCAPE '!'", pattern).
		Order("id asc").
		Limit(limit).
		Find(&channels).Error
	return channels, err
}

func GetSupplierStats(q SupplierStatsQuery) (*SupplierStatsResult, error) {
	channels, err := FindChannelsBySupplierKeyword(q.SupplierKeyword, 500)
	if err != nil {
		return nil, err
	}
	result := &SupplierStatsResult{
		SupplierKeyword: q.SupplierKeyword,
		Source:          "ledger",
		MatchedChannels: channels,
	}
	channelIDs := make([]int, 0, len(channels))
	for _, ch := range channels {
		channelIDs = append(channelIDs, ch.Id)
	}
	result.SummaryByCurrency, err = querySupplierLedgerStats(q, channelIDs, "currency")
	if err != nil {
		return nil, err
	}
	result.BySupplier, err = querySupplierLedgerStats(q, channelIDs, "supplier")
	if err != nil {
		return nil, err
	}
	result.ByChannel, err = querySupplierLedgerStats(q, channelIDs, "channel")
	if err != nil {
		return nil, err
	}
	result.ByModel, err = querySupplierLedgerStats(q, channelIDs, "model")
	if err != nil {
		return nil, err
	}
	return result, nil
}

func querySupplierLedgerStats(q SupplierStatsQuery, channelIDs []int, groupBy string) ([]SupplierStatsRow, error) {
	tx := DB.Table("billing_ledger").
		Where("status IN ?", []string{BillingLedgerStatusSettled, BillingLedgerStatusRefunded})
	if q.StartTimestamp > 0 {
		tx = tx.Where("created_at >= ?", q.StartTimestamp)
	}
	if q.EndTimestamp > 0 {
		tx = tx.Where("created_at <= ?", q.EndTimestamp)
	}
	if q.ModelName != "" {
		tx = applyLogContainsFilter(tx, "model_name", q.ModelName)
	}
	if q.UserGroup != "" {
		tx = tx.Where("user_group = ?", q.UserGroup)
	}
	if q.TokenName != "" {
		tx = applyLogContainsFilter(tx, "token_name", q.TokenName)
	}
	if q.ServerRegion != "" {
		tx = tx.Where("server_region = ?", q.ServerRegion)
	}
	if q.Currency != "" {
		tx = tx.Where("currency = ?", strings.ToUpper(strings.TrimSpace(q.Currency)))
	}
	pattern, hasKeyword := logContainsPattern(q.SupplierKeyword)
	if hasKeyword {
		if len(channelIDs) > 0 {
			tx = tx.Where("(supplier_name LIKE ? ESCAPE '!' OR channel_id IN ?)", pattern, channelIDs)
		} else {
			tx = tx.Where("supplier_name LIKE ? ESCAPE '!'", pattern)
		}
	} else if len(channelIDs) > 0 {
		tx = tx.Where("channel_id IN ?", channelIDs)
	}

	selectCols := []string{"currency"}
	groupCols := []string{"currency"}
	switch groupBy {
	case "supplier":
		selectCols = []string{"supplier_name", "currency"}
		groupCols = []string{"supplier_name", "currency"}
	case "channel":
		selectCols = []string{"channel_id", "channel_name", "currency"}
		groupCols = []string{"channel_id", "channel_name", "currency"}
	case "model":
		selectCols = []string{"model_name", "currency"}
		groupCols = []string{"model_name", "currency"}
	}
	selectSQL := strings.Join(selectCols, ", ") + `,
		COALESCE(SUM(CASE WHEN status = 'refunded' THEN 0 ELSE quota END), 0) AS consume_quota,
		COALESCE(SUM(CASE WHEN status = 'refunded' THEN quota ELSE 0 END), 0) AS refund_quota,
		COALESCE(SUM(CASE WHEN status = 'refunded' THEN 0 ELSE quota END), 0) - COALESCE(SUM(CASE WHEN status = 'refunded' THEN quota ELSE 0 END), 0) AS net_quota,
		COALESCE(SUM(CASE WHEN status = 'refunded' THEN -amount ELSE amount END), 0) AS amount,
		COUNT(*) AS request_count,
		COALESCE(SUM(prompt_tokens), 0) AS prompt_tokens,
		COALESCE(SUM(completion_tokens), 0) AS completion_tokens,
		COALESCE(SUM(prompt_tokens + completion_tokens), 0) AS total_tokens`
	var rows []SupplierStatsRow
	err := tx.Select(selectSQL).Group(strings.Join(groupCols, ", ")).Order(strings.Join(groupCols, ", ")).Scan(&rows).Error
	return rows, err
}
