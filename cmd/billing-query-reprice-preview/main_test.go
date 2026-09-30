package main

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStoredTieredTokenParamsUsesFrozenValues(t *testing.T) {
	other := map[string]interface{}{
		"tiered_expr_params": map[string]interface{}{
			"p": 12.0, "c": 34.0, "len": 56.0, "cr": 7.0, "cc": 8.0,
			"cc1h": 9.0, "img": 10.0, "img_o": 11.0, "ai": 12.0, "ao": 13.0,
		},
	}

	params, found, err := storedTieredTokenParams(other, map[string]bool{"p": true, "c": true, "len": true})
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, billingexpr.TokenParams{
		P: 12, C: 34, Len: 56, CR: 7, CC: 8, CC1h: 9, Img: 10, ImgO: 11, AI: 12, AO: 13,
	}, params)
}

func TestStoredTieredTokenParamsRejectsMissingUsedValue(t *testing.T) {
	other := map[string]interface{}{
		"tiered_expr_params": map[string]interface{}{"p": 12.0, "c": 34.0},
	}

	_, found, err := storedTieredTokenParams(other, map[string]bool{"len": true})
	require.True(t, found)
	require.EqualError(t, err, "tiered_expr_params_missing_len")
}

func TestApplySavedRequestParamsOverridesServiceTier(t *testing.T) {
	input := billingexpr.RequestInput{Body: []byte(`{"service_tier":"priority","prompt":"hello"}`)}

	updated, applied, err := applySavedRequestParams(input, map[string]interface{}{"service_tier": "flex"})
	require.NoError(t, err)
	require.True(t, applied)
	assert.Equal(t, "flex", gjson.GetBytes(updated.Body, "service_tier").String())
	assert.Equal(t, "hello", gjson.GetBytes(updated.Body, "prompt").String())
}

func TestApplySavedRequestParamsBuildsMinimalBody(t *testing.T) {
	updated, applied, err := applySavedRequestParams(billingexpr.RequestInput{}, map[string]interface{}{"service_tier": "priority"})
	require.NoError(t, err)
	require.True(t, applied)
	assert.Equal(t, "priority", gjson.GetBytes(updated.Body, "service_tier").String())
}

func TestDisplayAmountMatchesRoundedQuota(t *testing.T) {
	assert.Equal(t, "0.00000200", displayAmount(1).StringFixed(8))
}

func TestTokenParamsFromUsageJSONSeparatesGeminiImageOutput(t *testing.T) {
	raw := `{
		"total_tokens":4099,
		"prompt_tokens":2499,
		"completion_tokens":1600,
		"prompt_tokens_details":{"text_tokens":259,"image_tokens":2240},
		"completion_tokens_details":{"image_tokens":1120,"reasoning_tokens":480}
	}`
	used := map[string]bool{"p": true, "c": true, "img_o": true}

	params, usable, err := tokenParamsFromUsageJSON(raw, nil, used)
	require.NoError(t, err)
	require.True(t, usable)
	assert.Equal(t, float64(2499), params.P)
	assert.Equal(t, float64(480), params.C)
	assert.Equal(t, float64(1120), params.ImgO)
	assert.Equal(t, float64(2240), params.Img)
}

func TestTokenParamsFromUsageJSONHandlesImageOnlyCompletion(t *testing.T) {
	raw := `{
		"total_tokens":6103,
		"prompt_tokens":3583,
		"completion_tokens":2520,
		"prompt_tokens_details":{"text_tokens":223,"image_tokens":3360},
		"completion_tokens_details":{"image_tokens":2520}
	}`
	used := map[string]bool{"p": true, "c": true, "img_o": true}

	params, usable, err := tokenParamsFromUsageJSON(raw, nil, used)
	require.NoError(t, err)
	require.True(t, usable)
	assert.Equal(t, float64(3583), params.P)
	assert.Zero(t, params.C)
	assert.Equal(t, float64(2520), params.ImgO)
}

func TestValidateApplyRequestRequiresExactBatchAndTotals(t *testing.T) {
	config := batchConfig{
		BatchID: "batch-one",
		ApplyGuard: applyGuard{
			BackupLedgerTable: "billing_ledger_bak_one", BackupOutboxTable: "billing_outbox_bak_one",
			ExpectedRows: 1, ExpectedOutboxRows: 1, ExpectedOldQuota: 10, ExpectedOldAmount: "0.00002000",
			ExpectedNewQuota: 20, ExpectedNewAmount: "0.00004000",
		},
	}
	ready := []calculatedRow{{
		Ledger:   ledgerRow{OldQuota: 10, OldAmount: decimal.RequireFromString("0.00002000")},
		NewQuota: 20, NewAmount: decimal.RequireFromString("0.00004000"),
	}}

	err := validateApplyRequest(cliOptions{Apply: true, ConfirmBatch: "wrong"}, config, ready, nil)
	require.EqualError(t, err, "--confirm-batch must exactly equal \"batch-one\"")

	require.NoError(t, validateApplyRequest(cliOptions{Apply: true, ConfirmBatch: "batch-one"}, config, ready, nil))

	ready[0].NewQuota++
	err = validateApplyRequest(cliOptions{Apply: true, ConfirmBatch: "batch-one"}, config, ready, nil)
	require.ErrorContains(t, err, "new totals changed")
}

func TestValidateApplyGuardRejectsUnsafeTableName(t *testing.T) {
	guard := applyGuard{
		BackupLedgerTable: "billing_ledger_bak;drop", BackupOutboxTable: "billing_outbox_bak",
		ExpectedRows: 1, ExpectedOutboxRows: 1, ExpectedOldQuota: 1, ExpectedOldAmount: "1.00000000",
		ExpectedNewQuota: 1, ExpectedNewAmount: "1.00000000",
	}

	require.EqualError(t, validateApplyGuard(guard), "apply_guard backup table names must be simple SQL identifiers")
}

func TestBuildRepricedEvidencePreservesOldEvidenceAndPatchesDisplayResult(t *testing.T) {
	row := calculatedRow{
		Ledger: ledgerRow{
			ID: 7, BillingID: "billing-7", BillingType: "request", UserGroup: "group-a",
			ChannelID: 4, ChannelName: "channel-a", ModelName: "model-a", Currency: "USD",
			OldQuota: 100, OldAmount: decimal.RequireFromString("0.00020000"),
			Other:             "{\"tiered_expr_params\":{\"p\":10},\"historical_marker\":\"keep\"}",
			BillingMatchInfo:  "{\"quota\":100,\"historical_marker\":\"old-match\"}",
			BillingCalcDetail: "{\"result\":{\"quota\":100},\"usage\":{\"prompt_tokens\":10},\"steps\":[{\"name\":\"quota_to_amount\",\"quota\":100}]}",
		},
		GroupRatio: 0.848, NewQuota: 150, NewAmount: decimal.RequireFromString("0.00030000"),
		Mode: billing_setting.BillingModeTieredExpr, Pricing: "p * 2", UsageEvidence: "tiered_expr_params",
	}

	otherRaw, matchRaw, detailRaw, amountText, err := buildRepricedEvidence("batch-one", row, "2026-07-29T15:30:00Z")
	require.NoError(t, err)
	assert.Equal(t, "$0.000300", amountText)

	var other, match, detail map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(otherRaw), &other))
	require.NoError(t, json.Unmarshal([]byte(matchRaw), &match))
	require.NoError(t, json.Unmarshal([]byte(detailRaw), &detail))

	assert.Equal(t, "keep", other["historical_marker"])
	assert.EqualValues(t, 150, match["quota"])
	assert.Equal(t, "display_reprice", match["match_source"])
	assert.EqualValues(t, 150, detail["result"].(map[string]interface{})["quota"])
	assert.Equal(t, "0.00030000", detail["result"].(map[string]interface{})["amount"])
	assert.Equal(t, "batch-one", other["display_reprice"].(map[string]interface{})["batch_id"])
	assert.Equal(t, "old-match", other["display_reprice"].(map[string]interface{})["previous_billing_match_info"].(map[string]interface{})["historical_marker"])
}
