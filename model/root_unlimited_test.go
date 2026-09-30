package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRootUserDecreaseQuotaIsNoop(t *testing.T) {
	truncateTables(t)

	root := &User{Id: 9001, Username: "root_unlimited", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: 0, AffCode: "root-unlimited"}
	commonUser := &User{Id: 9002, Username: "common_limited", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Quota: 1000, AffCode: "common-limited"}
	require.NoError(t, DB.Create(root).Error)
	require.NoError(t, DB.Create(commonUser).Error)

	require.NoError(t, DecreaseUserQuota(root.Id, 500, true))
	require.NoError(t, DecreaseUserQuota(commonUser.Id, 500, true))

	var rootQuota int
	var commonQuota int
	require.NoError(t, DB.Model(&User{}).Where("id = ?", root.Id).Select("quota").Take(&rootQuota).Error)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", commonUser.Id).Select("quota").Take(&commonQuota).Error)
	assert.Equal(t, 0, rootQuota)
	assert.Equal(t, 500, commonQuota)
}

func TestRootUserTokenWithNoRemainQuotaIsStillValid(t *testing.T) {
	truncateTables(t)

	initCol()
	root := &User{Id: 9011, Username: "root_token", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AffCode: "root-token"}
	token := &Token{Id: 9012, UserId: root.Id, Key: "root-token-empty", Status: common.TokenStatusEnabled, RemainQuota: 0, UsedQuota: 0}
	require.NoError(t, DB.Create(root).Error)
	require.NoError(t, DB.Create(token).Error)

	got, err := ValidateUserToken(token.Key)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, token.Id, got.Id)

	require.NoError(t, DecreaseTokenQuota(token.Id, token.Key, 500))
	var updated Token
	require.NoError(t, DB.Where("id = ?", token.Id).First(&updated).Error)
	assert.Equal(t, 0, updated.RemainQuota)
	assert.Equal(t, 500, updated.UsedQuota)
}

func TestGetDailyConsumptionDataUsesNetSettledQuota(t *testing.T) {
	truncateTables(t)
	withBillingLedgerTestDefaults(t)

	day := int64(1783555200)
	require.NoError(t, UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:        "daily-settled",
		BillingType:      BillingLedgerTypeRequest,
		UserID:           1,
		Username:         "root",
		ModelName:        "model-a",
		Quota:            1000,
		Status:           BillingLedgerStatusSettled,
		PromptTokens:     10,
		CompletionTokens: 20,
		CreatedAt:        day + 60,
		SettledAt:        day + 60,
	}))
	require.NoError(t, UpsertBillingLedger(BillingLedgerUpsertParams{
		BillingID:        "daily-refunded",
		BillingType:      BillingLedgerTypeRefund,
		UserID:           1,
		Username:         "root",
		ModelName:        "model-a",
		Quota:            300,
		Status:           BillingLedgerStatusRefunded,
		PromptTokens:     0,
		CompletionTokens: 0,
		CreatedAt:        day + 120,
		SettledAt:        day + 120,
	}))

	rows, err := GetDailyConsumptionData(day, day+86399, "root", 0)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 700, rows[0].Quota)
	assert.EqualValues(t, 1, rows[0].RequestCount)
	assert.EqualValues(t, 30, rows[0].TotalTokens)
}
