package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYouchuanFeeCostQuota(t *testing.T) {
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })

	tests := []struct {
		name       string
		feeCost    float64
		groupRatio float64
		wantQuota  int
		wantOK     bool
	}{
		{name: "standard price", feeCost: 60, groupRatio: 1, wantQuota: 300000, wantOK: true},
		{name: "discounted group", feeCost: 90, groupRatio: 0.8, wantQuota: 360000, wantOK: true},
		{name: "missing fee", feeCost: 0, groupRatio: 1, wantOK: false},
		{name: "negative fee", feeCost: -1, groupRatio: 1, wantOK: false},
		{name: "missing ratio", feeCost: 60, groupRatio: 0, wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			quota, ok := YouchuanFeeCostQuota(tt.feeCost, tt.groupRatio)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantQuota, quota)
		})
	}
}

func TestSettleYouchuanTaskQuotaRefundsFromRawCost(t *testing.T) {
	truncate(t)
	setYouchuanBillingTestCurrency(t, common.BillingCurrencyCNY)

	task := seedYouchuanBillingTask(t, "task-youchuan-cost-refund", 450000, 1, json.RawMessage(`{"feeCost":60,"fastCost":1,"relaxCost":0,"costAt":"2026-08-18T00:00:00Z"}`), json.RawMessage(`{}`))
	SettleYouchuanTaskQuota(context.Background(), task, "test callback")

	assert.Equal(t, 1150000, getUserQuota(t, task.UserId))
	assert.Equal(t, 2150000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	assert.Equal(t, 300000, task.Quota)

	ledger := getBillingLedger(t, task.TaskID)
	assert.Equal(t, model.BillingLedgerStatusSettled, ledger.Status)
	assert.Equal(t, model.BillingUsageSourceUpstream, ledger.UsageSource)
	assert.EqualValues(t, 300000, ledger.Quota)
	amount, _ := ledger.Amount.Float64()
	assert.InDelta(t, 0.6, amount, 0.000001)

	other := billingLedgerOther(t, ledger)
	assert.Equal(t, common.BillingCurrencyCNY, other["upstream_cost_currency"])
	assert.InDelta(t, 60, other["upstream_fee_cost_cent"].(float64), 0.000001)
	assert.InDelta(t, 0.6, other["upstream_cost_cny"].(float64), 0.000001)
	assert.InDelta(t, 1, other["billing_group_ratio"].(float64), 0.000001)
}

func TestSettleYouchuanTaskQuotaFallsBackToTaskDataAndChargesDifference(t *testing.T) {
	truncate(t)
	setYouchuanBillingTestCurrency(t, common.BillingCurrencyCNY)

	task := seedYouchuanBillingTask(t, "task-youchuan-cost-charge", 300000, 0.8, json.RawMessage(`{"feeCost":"invalid"}`), json.RawMessage(`{"cost":{"feeCost":90,"fastCost":2,"relaxCost":0}}`))
	SettleYouchuanTaskQuota(context.Background(), task, "test polling")
	SettleYouchuanTaskQuota(context.Background(), task, "duplicate polling")

	assert.Equal(t, 940000, getUserQuota(t, task.UserId))
	assert.Equal(t, 1940000, getTokenRemainQuota(t, task.PrivateData.TokenId))
	assert.Equal(t, 360000, task.Quota)
	assert.EqualValues(t, 1, countLogs(t))

	ledger := getBillingLedger(t, task.TaskID)
	assert.Equal(t, model.BillingLedgerStatusSettled, ledger.Status)
	assert.Equal(t, model.BillingUsageSourceUpstream, ledger.UsageSource)
	assert.EqualValues(t, 360000, ledger.Quota)
	amount, _ := ledger.Amount.Float64()
	assert.InDelta(t, 0.72, amount, 0.000001)
}

func TestSettleYouchuanTaskQuotaKeepsEstimateWithoutUsableCNYCost(t *testing.T) {
	tests := []struct {
		name     string
		currency string
		rawCost  json.RawMessage
	}{
		{name: "missing fee", currency: common.BillingCurrencyCNY, rawCost: json.RawMessage(`{"feeCost":0}`)},
		{name: "malformed cost", currency: common.BillingCurrencyCNY, rawCost: json.RawMessage(`{"feeCost":"invalid"}`)},
		{name: "USD server", currency: common.BillingCurrencyUSD, rawCost: json.RawMessage(`{"feeCost":90}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			truncate(t)
			setYouchuanBillingTestCurrency(t, tt.currency)
			task := seedYouchuanBillingTask(t, "task-youchuan-estimate-"+tt.name, 300000, 1, tt.rawCost, json.RawMessage(`{}`))

			SettleYouchuanTaskQuota(context.Background(), task, "test estimate")

			assert.Equal(t, 1000000, getUserQuota(t, task.UserId))
			assert.Equal(t, 2000000, getTokenRemainQuota(t, task.PrivateData.TokenId))
			assert.Equal(t, 300000, task.Quota)
			ledger := getBillingLedger(t, task.TaskID)
			assert.Equal(t, model.BillingLedgerStatusSettled, ledger.Status)
			assert.EqualValues(t, 300000, ledger.Quota)
		})
	}
}

func setYouchuanBillingTestCurrency(t *testing.T, currency string) {
	t.Helper()
	t.Setenv("SERVER_BILLING_CURRENCY", currency)
	oldQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldQuotaPerUnit })
}

func seedYouchuanBillingTask(t *testing.T, taskID string, preConsumedQuota int, groupRatio float64, rawCost, data json.RawMessage) *model.Task {
	t.Helper()
	const userID, tokenID, channelID = 81, 82, 83
	seedUser(t, userID, 1000000)
	seedToken(t, tokenID, userID, "sk-youchuan-billing-test", 2000000)
	seedChannel(t, channelID)
	task := makeTask(userID, channelID, preConsumedQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = taskID
	task.Data = data
	task.PrivateData.BillingContext.GroupRatio = groupRatio
	task.PrivateData.Youchuan = &model.YouchuanTaskPrivateData{RawCost: rawCost}
	require.NoError(t, model.DB.Create(task).Error)
	seedPendingTaskLedger(t, task)
	return task
}

func billingLedgerOther(t *testing.T, ledger *model.BillingLedger) map[string]interface{} {
	t.Helper()
	var other map[string]interface{}
	require.NoError(t, common.Unmarshal([]byte(ledger.Other), &other))
	return other
}
