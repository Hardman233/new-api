package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withBillingLedgerTestDefaults(t *testing.T) {
	t.Helper()
	t.Setenv("SERVER_REGION", common.ServerRegionOverseas)
	t.Setenv("SERVER_BILLING_CURRENCY", common.BillingCurrencyUSD)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQuotaPerUnit
	})
}

func insertBillingLedgerChannel(t *testing.T, id int, name string) {
	t.Helper()
	require.NoError(t, DB.Create(&Channel{
		Id:     id,
		Name:   name,
		Key:    "test-key",
		Status: common.ChannelStatusEnabled,
	}).Error)
}

func TestUpsertBillingLedgerCreatesAndUpdates(t *testing.T) {
	truncateTables(t)
	withBillingLedgerTestDefaults(t)
	insertBillingLedgerChannel(t, 1001, "DeepSeek Overseas")

	err := UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:        "req-ledger-upsert",
		BillingType:      BillingLedgerTypeRequest,
		SourceRequestID:  "req-ledger-upsert",
		UserID:           11,
		Username:         "ledger_user",
		TokenID:          22,
		TokenName:        "ledger_token",
		UserGroup:        "vip",
		ChannelID:        1001,
		ModelName:        "deepseek-v4-flash",
		Quota:            250000,
		Status:           BillingLedgerStatusSettled,
		PromptTokens:     10,
		CompletionTokens: 20,
		CreatedAt:        100,
	})
	require.NoError(t, err)

	ledger, err := GetBillingLedgerByBillingID("req-ledger-upsert")
	require.NoError(t, err)
	assert.Equal(t, BillingLedgerTypeRequest, ledger.BillingType)
	assert.Equal(t, "req-ledger-upsert", ledger.SourceRequestID)
	assert.Equal(t, common.ServerRegionOverseas, ledger.ServerRegion)
	assert.Equal(t, common.BillingCurrencyUSD, ledger.Currency)
	assert.Equal(t, "DeepSeek Overseas", ledger.ChannelName)
	assert.Equal(t, "DeepSeek", ledger.SupplierName)
	assert.EqualValues(t, 250000, ledger.Quota)
	assert.Equal(t, BillingUsageSourceUpstream, ledger.UsageSource)
	assert.Equal(t, "$0.500000", ledger.AmountText)
	amount, _ := ledger.Amount.Float64()
	assert.InDelta(t, 0.5, amount, 0.000001)
	require.NotEmpty(t, ledger.BillingMatchInfo)
	require.NotEmpty(t, ledger.BillingCalcDetail)
	matchInfo, err := common.StrToMap(ledger.BillingMatchInfo)
	require.NoError(t, err)
	assert.Equal(t, "ratio", matchInfo["mode"])
	calcDetail, err := common.StrToMap(ledger.BillingCalcDetail)
	require.NoError(t, err)
	result, ok := calcDetail["result"].(map[string]interface{})
	require.True(t, ok)
	assert.EqualValues(t, 250000, result["quota"])

	err = UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:        "req-ledger-upsert",
		BillingType:      BillingLedgerTypeRequest,
		SourceRequestID:  "req-ledger-upsert",
		UserID:           11,
		Username:         "ledger_user",
		TokenID:          22,
		TokenName:        "ledger_token",
		UserGroup:        "vip",
		ChannelID:        1001,
		ChannelName:      "DeepSeek Renamed Snapshot",
		SupplierName:     "DeepSeek",
		ModelName:        "deepseek-v4-flash",
		Quota:            1000000,
		Currency:         common.BillingCurrencyUSD,
		ServerRegion:     common.ServerRegionOverseas,
		Status:           BillingLedgerStatusSettled,
		PromptTokens:     30,
		CompletionTokens: 40,
		CreatedAt:        200,
		SettledAt:        220,
	})
	require.NoError(t, err)

	var count int64
	require.NoError(t, DB.Model(&BillingLedger{}).Where("billing_id = ?", "req-ledger-upsert").Count(&count).Error)
	assert.EqualValues(t, 1, count)

	ledger, err = GetBillingLedgerByBillingID("req-ledger-upsert")
	require.NoError(t, err)
	assert.EqualValues(t, 1000000, ledger.Quota)
	assert.Equal(t, "DeepSeek Renamed Snapshot", ledger.ChannelName)
	assert.Equal(t, BillingUsageSourceUpstream, ledger.UsageSource)
	assert.EqualValues(t, 220, ledger.SettledAt)
	amount, _ = ledger.Amount.Float64()
	assert.InDelta(t, 2.0, amount, 0.000001)
	require.NotEmpty(t, ledger.BillingMatchInfo)
	require.NotEmpty(t, ledger.BillingCalcDetail)
}

func TestUpsertBillingLedgerPersistsAndFreezesCNYReference(t *testing.T) {
	truncateTables(t)
	withBillingLedgerTestDefaults(t)
	providerRate := 7.123456
	restoreProvider := SetBillingCNYReferenceProvider(func() (BillingCNYReferenceSnapshot, bool) {
		return BillingCNYReferenceSnapshot{
			Rate:       providerRate,
			Base:       "USD",
			Target:     "CNY",
			UpdatedAt:  1000,
			Source:     "test-provider",
			CapturedAt: 1100,
		}, true
	})
	t.Cleanup(restoreProvider)

	params := BillingLedgerUpsertParams{
		BillingID:       "req-ledger-cny-snapshot",
		BillingType:     BillingLedgerTypeRequest,
		SourceRequestID: "req-ledger-cny-snapshot",
		UserID:          11,
		TokenID:         22,
		ModelName:       "test-model",
		Quota:           500000,
		Currency:        common.BillingCurrencyUSD,
		Status:          BillingLedgerStatusSettled,
		CreatedAt:       1200,
	}
	require.NoError(t, UpsertBillingLedger(params))

	ledger, err := GetBillingLedgerByBillingID(params.BillingID)
	require.NoError(t, err)
	rate, _ := ledger.ExchangeRate.Float64()
	amountCNY, _ := ledger.AmountCNYReference.Float64()
	assert.InDelta(t, providerRate, rate, 0.00000001)
	assert.InDelta(t, 1.0*providerRate, amountCNY, 0.00000001)

	providerRate = 8.000001
	params.Quota = 1000000
	params.CreatedAt = 1300
	require.NoError(t, UpsertBillingLedger(params))
	ledger, err = GetBillingLedgerByBillingID(params.BillingID)
	require.NoError(t, err)
	rate, _ = ledger.ExchangeRate.Float64()
	amountCNY, _ = ledger.AmountCNYReference.Float64()
	assert.InDelta(t, 7.123456, rate, 0.00000001)
	assert.InDelta(t, 2.0*7.123456, amountCNY, 0.00000001)

	other, err := common.StrToMap(ledger.Other)
	require.NoError(t, err)
	reference, ok := other["cny_reference"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "settlement_snapshot", reference["policy"])
	assert.Equal(t, "2.00000000", reference["amount_usd"])
	assert.Equal(t, "14.24691200", reference["amount_cny"])
}

func TestUpsertBillingLedgerCanPreserveMissingImportedCNYSnapshot(t *testing.T) {
	truncateTables(t)
	withBillingLedgerTestDefaults(t)
	restoreProvider := SetBillingCNYReferenceProvider(func() (BillingCNYReferenceSnapshot, bool) {
		return BillingCNYReferenceSnapshot{Rate: 7.5}, true
	})
	t.Cleanup(restoreProvider)

	require.NoError(t, UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:               "imported-without-cny-snapshot",
		Quota:                   500000,
		Currency:                common.BillingCurrencyUSD,
		Status:                  BillingLedgerStatusSettled,
		SkipCNYReferenceCapture: true,
	}))

	ledger, err := GetBillingLedgerByBillingID("imported-without-cny-snapshot")
	require.NoError(t, err)
	assert.True(t, ledger.ExchangeRate.IsZero())
	assert.True(t, ledger.AmountCNYReference.IsZero())
}

func TestUpsertBillingLedgerDoesNotPersistCNYForDomesticBill(t *testing.T) {
	truncateTables(t)
	withBillingLedgerTestDefaults(t)
	providerCalls := 0
	restoreProvider := SetBillingCNYReferenceProvider(func() (BillingCNYReferenceSnapshot, bool) {
		providerCalls++
		return BillingCNYReferenceSnapshot{Rate: 7.5}, true
	})
	t.Cleanup(restoreProvider)

	require.NoError(t, UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:    "domestic-usd-bill",
		Quota:        500000,
		Currency:     common.BillingCurrencyUSD,
		ServerRegion: common.ServerRegionDomestic,
		Status:       BillingLedgerStatusSettled,
	}))

	ledger, err := GetBillingLedgerByBillingID("domestic-usd-bill")
	require.NoError(t, err)
	assert.Zero(t, providerCalls)
	assert.True(t, ledger.ExchangeRate.IsZero())
	assert.True(t, ledger.AmountCNYReference.IsZero())
}

func TestBackfillBillingCNYReferencesUsesOneCurrentRateAndMarksProvenance(t *testing.T) {
	truncateTables(t)
	withBillingLedgerTestDefaults(t)
	require.NoError(t, DB.AutoMigrate(&BillingSyncOutbox{}))
	restoreProvider := SetBillingCNYReferenceProvider(nil)
	t.Cleanup(restoreProvider)

	require.NoError(t, UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:       "legacy-usd-bill",
		SourceRequestID: "legacy-usd-bill",
		Quota:           500000,
		Currency:        common.BillingCurrencyUSD,
		ServerRegion:    common.ServerRegionOverseas,
		Status:          BillingLedgerStatusSettled,
		CreatedAt:       100,
	}))
	require.NoError(t, UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:       "new-pending-bill",
		SourceRequestID: "new-pending-bill",
		Quota:           500000,
		Currency:        common.BillingCurrencyUSD,
		ServerRegion:    common.ServerRegionOverseas,
		Status:          BillingLedgerStatusPending,
		CreatedAt:       100,
	}))
	require.NoError(t, UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:               "domestic-legacy-usd-bill",
		SourceRequestID:         "domestic-legacy-usd-bill",
		Quota:                   500000,
		Currency:                common.BillingCurrencyUSD,
		ServerRegion:            common.ServerRegionDomestic,
		Status:                  BillingLedgerStatusSettled,
		CreatedAt:               100,
		SkipCNYReferenceCapture: true,
	}))
	require.NoError(t, DB.Create(&BillingSyncOutbox{
		BillingID: "legacy-usd-bill",
		Status:    BillingSyncStatusSynced,
		SyncedAt:  150,
	}).Error)

	options := BillingCNYBackfillOptions{
		Before:        200,
		ServerRegion:  common.ServerRegionOverseas,
		Rate:          decimal.RequireFromString("7.10000000"),
		Base:          "USD",
		Target:        "CNY",
		Source:        "test-current-rate",
		RateUpdatedAt: 175,
		CapturedAt:    180,
		BatchID:       "test-cny-backfill-v1",
	}
	preview, err := BackfillBillingCNYReferences(options)
	require.NoError(t, err)
	assert.Equal(t, 1, preview.Matched)
	assert.Equal(t, 0, preview.Updated)

	options.Apply = true
	applied, err := BackfillBillingCNYReferences(options)
	require.NoError(t, err)
	assert.Equal(t, 1, applied.Matched)
	assert.Equal(t, 1, applied.Updated)
	assert.Equal(t, 1, applied.OutboxReset)

	ledger, err := GetBillingLedgerByBillingID("legacy-usd-bill")
	require.NoError(t, err)
	rate, _ := ledger.ExchangeRate.Float64()
	amountCNY, _ := ledger.AmountCNYReference.Float64()
	assert.InDelta(t, 7.1, rate, 0.00000001)
	assert.InDelta(t, 7.1, amountCNY, 0.00000001)
	other, err := common.StrToMap(ledger.Other)
	require.NoError(t, err)
	reference, ok := other["cny_reference"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, true, reference["backfilled"])
	assert.Equal(t, "backfill_current_rate", reference["policy"])
	assert.Equal(t, "test-cny-backfill-v1", reference["backfill_batch_id"])

	var outbox BillingSyncOutbox
	require.NoError(t, DB.Where("billing_id = ?", "legacy-usd-bill").First(&outbox).Error)
	assert.Equal(t, BillingSyncStatusPending, outbox.Status)
	assert.Zero(t, outbox.SyncedAt)

	pending, err := GetBillingLedgerByBillingID("new-pending-bill")
	require.NoError(t, err)
	assert.True(t, pending.ExchangeRate.IsZero())
	domestic, err := GetBillingLedgerByBillingID("domestic-legacy-usd-bill")
	require.NoError(t, err)
	assert.True(t, domestic.ExchangeRate.IsZero())

	options.ServerRegion = common.ServerRegionDomestic
	_, err = BackfillBillingCNYReferences(options)
	require.EqualError(t, err, "CNY backfill only supports overseas billing ledgers")
}

func TestUpsertBillingLedgerStoresTieredEvidence(t *testing.T) {
	truncateTables(t)
	withBillingLedgerTestDefaults(t)

	err := UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:        "req-ledger-tiered-evidence",
		BillingType:      BillingLedgerTypeRequest,
		SourceRequestID:  "req-ledger-tiered-evidence",
		UserID:           11,
		TokenID:          22,
		UserGroup:        "vip",
		ModelName:        "gemini-3-pro-image-preview",
		Quota:            123456,
		Status:           BillingLedgerStatusSettled,
		UsageSource:      BillingUsageSourceUpstream,
		PromptTokens:     13,
		CompletionTokens: 1408,
		CreatedAt:        100,
		Other: map[string]interface{}{
			"billing_mode":              "tiered_expr",
			"billing_expr_matched":      true,
			"billing_expr_hash":         "abc123",
			"billing_tier":              "google_image",
			"matched_tier":              "google_image",
			"tiered_quota_before_group": 246912.0,
			"tiered_quota_after_group":  123456,
			"tiered_expr_params": map[string]interface{}{
				"p":     13,
				"c":     288,
				"img_o": 1120,
			},
			"tiered_token_usage": map[string]interface{}{
				"prompt_tokens":       13,
				"completion_tokens":   1408,
				"image_output_tokens": 1120,
			},
		},
	})
	require.NoError(t, err)

	ledger, err := GetBillingLedgerByBillingID("req-ledger-tiered-evidence")
	require.NoError(t, err)
	assert.Equal(t, "tiered_expr", ledger.BillingMode)
	assert.True(t, ledger.BillingExprMatched)
	assert.Equal(t, "abc123", ledger.BillingExprHash)
	assert.Equal(t, "google_image", ledger.BillingTier)

	matchInfo, err := common.StrToMap(ledger.BillingMatchInfo)
	require.NoError(t, err)
	assert.Equal(t, "tiered_expr", matchInfo["mode"])
	assert.Equal(t, "google_image", matchInfo["tier"])
	assert.Equal(t, "abc123", matchInfo["expr_hash"])

	calcDetail, err := common.StrToMap(ledger.BillingCalcDetail)
	require.NoError(t, err)
	tiered, ok := calcDetail["tiered"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "google_image", tiered["billing_tier"])
	assert.Contains(t, tiered, "tiered_expr_params")
}

func TestTryCreatePendingBillingLedgerIsIdempotent(t *testing.T) {
	truncateTables(t)
	withBillingLedgerTestDefaults(t)

	params := BillingLedgerUpsertParams{
		BillingID:       "req-ledger-pending",
		BillingType:     BillingLedgerTypeRequest,
		SourceRequestID: "req-ledger-pending",
		UserID:          11,
		TokenID:         22,
		UserGroup:       "default",
		ModelName:       "deepseek-v4-flash",
		Quota:           500000,
		UsageSource:     BillingUsageSourceEstimated,
		CreatedAt:       100,
	}

	claimed, ledger, err := TryCreatePendingBillingLedger(params)
	require.NoError(t, err)
	require.True(t, claimed)
	require.NotNil(t, ledger)
	assert.Equal(t, BillingLedgerStatusPending, ledger.Status)
	assert.Equal(t, BillingUsageSourceEstimated, ledger.UsageSource)

	claimed, existing, err := TryCreatePendingBillingLedger(params)
	require.NoError(t, err)
	require.False(t, claimed)
	require.NotNil(t, existing)
	assert.Equal(t, BillingLedgerStatusPending, existing.Status)
	assert.Equal(t, BillingUsageSourceEstimated, existing.UsageSource)

	var count int64
	require.NoError(t, DB.Model(&BillingLedger{}).Where("billing_id = ?", "req-ledger-pending").Count(&count).Error)
	assert.EqualValues(t, 1, count)
}

func TestGetSupplierStatsGroupsByCurrencyAndRegion(t *testing.T) {
	truncateTables(t)
	withBillingLedgerTestDefaults(t)
	insertBillingLedgerChannel(t, 2001, "DeepSeek Overseas")
	insertBillingLedgerChannel(t, 2002, "DeepSeek Domestic")
	insertBillingLedgerChannel(t, 2003, "Other Supplier")

	entries := []BillingLedgerUpsertParams{
		{
			BillingID:       "stats-usd-1",
			SourceRequestID: "stats-usd-1",
			UserID:          1,
			TokenID:         1,
			TokenName:       "deepseek-token",
			UserGroup:       "default",
			ChannelID:       2001,
			ChannelName:     "DeepSeek Overseas",
			SupplierName:    "DeepSeek",
			ServerRegion:    common.ServerRegionOverseas,
			ModelName:       "deepseek-v4-flash",
			Quota:           1000000,
			Currency:        common.BillingCurrencyUSD,
			Status:          BillingLedgerStatusSettled,
			CreatedAt:       100,
		},
		{
			BillingID:       "stats-cny-1",
			SourceRequestID: "stats-cny-1",
			UserID:          2,
			TokenID:         2,
			TokenName:       "deepseek-cny-token",
			UserGroup:       "domestic",
			ChannelID:       2002,
			ChannelName:     "DeepSeek Domestic",
			SupplierName:    "DeepSeek",
			ServerRegion:    common.ServerRegionDomestic,
			ModelName:       "deepseek-local",
			Quota:           500000,
			Currency:        common.BillingCurrencyCNY,
			Status:          BillingLedgerStatusSettled,
			CreatedAt:       110,
		},
		{
			BillingID:       "stats-usd-refund-1",
			BillingType:     BillingLedgerTypeRefund,
			SourceRequestID: "stats-usd-refund-1",
			UserID:          1,
			TokenID:         1,
			TokenName:       "deepseek-token",
			UserGroup:       "default",
			ChannelID:       2001,
			ChannelName:     "DeepSeek Overseas",
			SupplierName:    "DeepSeek",
			ServerRegion:    common.ServerRegionOverseas,
			ModelName:       "deepseek-v4-flash",
			Quota:           250000,
			Currency:        common.BillingCurrencyUSD,
			Status:          BillingLedgerStatusRefunded,
			CreatedAt:       115,
		},
		{
			BillingID:       "stats-other-1",
			SourceRequestID: "stats-other-1",
			UserID:          3,
			TokenID:         3,
			TokenName:       "other-token",
			UserGroup:       "default",
			ChannelID:       2003,
			ChannelName:     "Other Supplier",
			SupplierName:    "Other",
			ServerRegion:    common.ServerRegionOverseas,
			ModelName:       "other-model",
			Quota:           900000,
			Currency:        common.BillingCurrencyUSD,
			Status:          BillingLedgerStatusSettled,
			CreatedAt:       120,
		},
	}
	for _, entry := range entries {
		require.NoError(t, UpsertBillingLedger(entry))
	}

	result, err := GetSupplierStats(SupplierStatsQuery{SupplierKeyword: "DeepSeek"})
	require.NoError(t, err)
	require.Len(t, result.MatchedChannels, 2)
	require.Len(t, result.SummaryByCurrency, 2)

	byCurrency := map[string]SupplierStatsRow{}
	for _, row := range result.SummaryByCurrency {
		byCurrency[row.Currency] = row
	}
	assert.EqualValues(t, 500000, byCurrency[common.BillingCurrencyCNY].NetQuota)
	assert.InDelta(t, 1.0, byCurrency[common.BillingCurrencyCNY].Amount, 0.000001)
	assert.EqualValues(t, 750000, byCurrency[common.BillingCurrencyUSD].NetQuota)
	assert.InDelta(t, 1.5, byCurrency[common.BillingCurrencyUSD].Amount, 0.000001)

	usdOnly, err := GetSupplierStats(SupplierStatsQuery{
		SupplierKeyword: "DeepSeek",
		ServerRegion:    common.ServerRegionOverseas,
		Currency:        common.BillingCurrencyUSD,
	})
	require.NoError(t, err)
	require.Len(t, usdOnly.SummaryByCurrency, 1)
	assert.Equal(t, common.BillingCurrencyUSD, usdOnly.SummaryByCurrency[0].Currency)
	assert.EqualValues(t, 750000, usdOnly.SummaryByCurrency[0].NetQuota)
}
