package controller

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestBillingTokensFromLedgerFallsBackToTaskData(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() {
		model.DB = oldDB
	})

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Task{}))

	task := &model.Task{
		TaskID: "task_legacy_zero_tokens",
		Data:   json.RawMessage(`{"usage":{"completion_tokens":50638,"total_tokens":50638,"tool_usage":{"web_search":0}}}`),
	}
	require.NoError(t, db.Create(task).Error)

	tokens := billingTokensFromLedger(&model.BillingLedger{
		BillingID:        task.TaskID,
		BillingType:      model.BillingLedgerTypeTask,
		SourceTaskID:     task.TaskID,
		PromptTokens:     0,
		CompletionTokens: 0,
	})

	require.EqualValues(t, 0, tokens.PromptTokens)
	require.EqualValues(t, 50638, tokens.CompletionTokens)
	require.EqualValues(t, 50638, tokens.TotalTokens)
	require.EqualValues(t, 0, tokens.Usage["tool_usage"].(map[string]interface{})["web_search"])
}

func TestReconcileTaskLedgerBillingUsesSuccessfulTaskQuota(t *testing.T) {
	oldDB := model.DB
	oldQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() {
		model.DB = oldDB
		common.QuotaPerUnit = oldQuotaPerUnit
	})
	common.QuotaPerUnit = 500000

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.Task{}))

	task := &model.Task{
		TaskID: "task_stale_ledger",
		Quota:  12002205,
		Status: model.TaskStatusSuccess,
	}
	require.NoError(t, db.Create(task).Error)

	ledgerAmount := 22.324102
	reconciliation := reconcileTaskLedgerBilling(&model.BillingLedger{
		BillingID:    task.TaskID,
		BillingType:  model.BillingLedgerTypeTask,
		SourceTaskID: task.TaskID,
		Quota:        11162051,
		Amount:       decimal.NewFromFloat(ledgerAmount),
		AmountText:   "CNY 22.324102",
		Currency:     common.BillingCurrencyCNY,
		Status:       model.BillingLedgerStatusSettled,
	}, 11162051, ledgerAmount, "CNY 22.324102")

	require.True(t, reconciliation.Reconciled)
	require.Equal(t, "task_quota_mismatch", reconciliation.Reason)
	require.EqualValues(t, 11162051, reconciliation.LedgerQuota)
	require.EqualValues(t, 12002205, reconciliation.TaskQuota)
	require.EqualValues(t, 12002205, reconciliation.Quota)
	require.InDelta(t, 24.004410, reconciliation.Amount, 0.000001)
	require.Equal(t, "CNY 24.004410", reconciliation.AmountText)
}

func TestAttachLedgerBillingCNYReferenceUsesStoredRate(t *testing.T) {
	billing := gin.H{
		"display_amount":   2.0,
		"display_currency": common.BillingCurrencyUSD,
	}
	ledger := &model.BillingLedger{
		Currency:           common.BillingCurrencyUSD,
		Amount:             decimal.RequireFromString("2.00000000"),
		ExchangeRate:       decimal.RequireFromString("7.10000000"),
		AmountCNYReference: decimal.RequireFromString("14.20000000"),
		Other:              `{"cny_reference":{"policy":"backfill_current_rate","backfilled":true,"source":"test-rate","rate_updated_at":123,"stale":false}}`,
	}

	require.True(t, attachLedgerBillingCNYReference(billing, ledger, true))
	require.Equal(t, 2.0, billing["amount_usd"])
	require.Equal(t, 14.2, billing["amount_cny"])
	require.Equal(t, 7.1, billing["exchange_rate"])
	require.Equal(t, "test-rate", billing["exchange_rate_source"])
	require.Equal(t, true, billing["amount_cny_reference_backfilled"])
	require.Equal(t, "backfill_current_rate", billing["amount_cny_reference_policy"])

	billing["display_amount"] = 3.0
	require.True(t, attachLedgerBillingCNYReference(billing, ledger, true))
	require.Equal(t, 21.3, billing["amount_cny"])
}

func TestAttachLedgerBillingCNYReferencePersistedOnlyDoesNotRecalculate(t *testing.T) {
	billing := gin.H{
		"display_amount":   3.0,
		"display_currency": common.BillingCurrencyUSD,
	}
	ledger := &model.BillingLedger{
		Currency:           common.BillingCurrencyUSD,
		Amount:             decimal.RequireFromString("2.00000000"),
		ExchangeRate:       decimal.RequireFromString("7.10000000"),
		AmountCNYReference: decimal.RequireFromString("14.20000000"),
	}

	require.True(t, attachLedgerBillingCNYReference(billing, ledger, false))
	require.Equal(t, 2.0, billing["amount_usd"])
	require.Equal(t, 14.2, billing["amount_cny"])
}

func TestGetTokenBillingQueryEnforcesOwnershipAndOmitsUpstreamResponse(t *testing.T) {
	oldDB := model.DB
	t.Cleanup(func() {
		model.DB = oldDB
	})

	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	require.NoError(t, db.AutoMigrate(&model.BillingLedger{}, &model.Task{}))
	require.NoError(t, db.Create(&model.BillingLedger{
		BillingID:          "owned-bill",
		BillingType:        model.BillingLedgerTypeRequest,
		UserID:             101,
		TokenID:            202,
		ModelName:          "test-model",
		Quota:              500000,
		Currency:           common.BillingCurrencyUSD,
		Amount:             decimal.RequireFromString("1.00000000"),
		AmountText:         "$1.000000",
		ExchangeRate:       decimal.RequireFromString("7.10000000"),
		AmountCNYReference: decimal.RequireFromString("7.10000000"),
		Status:             model.BillingLedgerStatusSettled,
	}).Error)

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("GET", "/api/billing?billing_id=owned-bill&include_upstream_response=true", nil)
	ctx.Set("id", 101)
	GetTokenBillingQuery(ctx)

	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, true, response["success"])
	data, ok := response["data"].(map[string]interface{})
	require.True(t, ok)
	require.NotContains(t, data, "upstream_response_log")
	billing, ok := data["billing"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, 7.1, billing["amount_cny"])

	otherRecorder := httptest.NewRecorder()
	otherCtx, _ := gin.CreateTestContext(otherRecorder)
	otherCtx.Request = httptest.NewRequest("GET", "/api/billing?billing_id=owned-bill", nil)
	otherCtx.Set("id", 999)
	GetTokenBillingQuery(otherCtx)
	response = nil
	require.NoError(t, json.Unmarshal(otherRecorder.Body.Bytes(), &response))
	require.Equal(t, false, response["success"])
	require.NotContains(t, response, "data")
}

func TestBuildLedgerBillingQueryDataPersistedOnlyDoesNotCalculateLegacyCNY(t *testing.T) {
	t.Setenv("BILLING_CNY_REFERENCE_ENABLED", "true")
	data := buildLedgerBillingQueryData(&model.BillingLedger{
		BillingID:   "legacy-without-cny-snapshot",
		BillingType: model.BillingLedgerTypeRequest,
		Currency:    common.BillingCurrencyUSD,
		Amount:      decimal.RequireFromString("1.00000000"),
		AmountText:  "$1.000000",
		Quota:       500000,
		Status:      model.BillingLedgerStatusSettled,
	}, true)

	billing, ok := data["billing"].(gin.H)
	require.True(t, ok)
	require.NotContains(t, billing, "exchange_rate")
	require.NotContains(t, billing, "amount_cny")
}
