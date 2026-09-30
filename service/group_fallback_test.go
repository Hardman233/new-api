package service

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func resetGroupFallbackTestTables(t *testing.T) {
	t.Helper()
	require.NoError(t, model.DB.AutoMigrate(
		&model.Channel{},
		&model.Ability{},
		&model.GroupFallbackSetting{},
		&model.GroupFallbackRule{},
		&model.GroupFallbackRuleModel{},
	))
	model.DB.Exec("DELETE FROM group_fallback_rule_models")
	model.DB.Exec("DELETE FROM group_fallback_rules")
	model.DB.Exec("DELETE FROM group_fallback_settings")
	model.DB.Exec("DELETE FROM abilities")
	model.DB.Exec("DELETE FROM channels")
	InvalidateGroupFallbackCache()
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM group_fallback_rule_models")
		model.DB.Exec("DELETE FROM group_fallback_rules")
		model.DB.Exec("DELETE FROM group_fallback_settings")
		model.DB.Exec("DELETE FROM abilities")
		model.DB.Exec("DELETE FROM channels")
		InvalidateGroupFallbackCache()
	})
}

func TestResolveGroupFallbackUsesEnabledModelMapping(t *testing.T) {
	resetGroupFallbackTestTables(t)
	require.NoError(t, model.DB.Create(&model.GroupFallbackSetting{SourceGroup: "source", Enabled: true}).Error)
	rule := &model.GroupFallbackRule{SourceGroup: "source", TargetGroup: "backup", Enabled: true}
	require.NoError(t, model.DB.Create(rule).Error)
	require.NoError(t, model.DB.Create(&model.GroupFallbackRuleModel{
		RuleId: rule.Id, SourceGroup: "source", ModelName: "model-a",
	}).Error)

	decision, ok := ResolveGroupFallback("source", "model-a")
	require.True(t, ok)
	require.Equal(t, "backup", decision.TargetGroup)
	require.Equal(t, rule.Id, decision.RuleId)
	require.False(t, ShouldDeferGroupFallback("source", "model-a"))
	_, ok = ResolveGroupFallback("source", "model-b")
	require.False(t, ok)
}

func TestValidateGroupFallbackRetryPolicy(t *testing.T) {
	tests := []struct {
		name   string
		config model.GroupFallbackConfig
	}{
		{
			name: "fallback disabled",
			config: model.GroupFallbackConfig{
				SourceGroup: "source", RetryEnabled: true, RetryMaxRetries: 2, RetryHTTPStatusCodes: "429",
			},
		},
		{
			name: "zero retries",
			config: model.GroupFallbackConfig{
				SourceGroup: "source", Enabled: true, RetryEnabled: true, RetryHTTPStatusCodes: "429",
			},
		},
		{
			name: "successful status",
			config: model.GroupFallbackConfig{
				SourceGroup: "source", Enabled: true, RetryEnabled: true, RetryMaxRetries: 2, RetryHTTPStatusCodes: "200",
			},
		},
		{
			name: "invalid status",
			config: model.GroupFallbackConfig{
				SourceGroup: "source", Enabled: true, RetryEnabled: true, RetryMaxRetries: 2, RetryHTTPStatusCodes: "bad",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, validateGroupFallbackConfig(&tt.config))
		})
	}
}

func TestSaveAndResolveRetryBeforeFallback(t *testing.T) {
	resetGroupFallbackTestTables(t)
	oldRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"source":1,"backup":2}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios)) })

	require.NoError(t, model.DB.Create(&model.Channel{Id: 921, Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 922, Status: common.ChannelStatusEnabled}).Error)
	for _, ability := range []model.Ability{
		{Group: "source", Model: "model-a", ChannelId: 921, Enabled: true},
		{Group: "backup", Model: "model-a", ChannelId: 922, Enabled: true},
	} {
		require.NoError(t, model.DB.Create(&ability).Error)
	}

	require.NoError(t, SaveGroupFallbackConfig(model.GroupFallbackConfig{
		SourceGroup:          "source",
		Enabled:              true,
		RetryEnabled:         true,
		RetryMaxRetries:      3,
		RetryHTTPStatusCodes: "429,502-504",
		Rules: []model.GroupFallbackRuleConfig{
			{TargetGroup: "backup", Enabled: true, Models: []string{"model-a"}},
		},
	}, 42))

	config, err := model.GetGroupFallbackConfig("source")
	require.NoError(t, err)
	require.True(t, config.RetryEnabled)
	require.Equal(t, 3, config.RetryMaxRetries)
	require.Equal(t, "429,502-504", config.RetryHTTPStatusCodes)

	decision, policy, enabled := ResolveGroupFallbackRetry("source", "model-a")
	require.True(t, enabled)
	require.Equal(t, "backup", decision.TargetGroup)
	require.Equal(t, 3, policy.MaxRetries)
	require.Equal(t, "429,502-504", policy.HTTPStatusCodes)
	require.True(t, ShouldDeferGroupFallback("source", "model-a"))
	require.False(t, ShouldDeferGroupFallback("source", "model-b"))
}

func TestApplyGroupFallbackSetsEffectiveGroup(t *testing.T) {
	resetGroupFallbackTestTables(t)
	require.NoError(t, model.DB.Create(&model.GroupFallbackSetting{SourceGroup: "source", Enabled: true}).Error)
	rule := &model.GroupFallbackRule{SourceGroup: "source", TargetGroup: "backup", Enabled: true}
	require.NoError(t, model.DB.Create(rule).Error)
	require.NoError(t, model.DB.Create(&model.GroupFallbackRuleModel{
		RuleId: rule.Id, SourceGroup: "source", ModelName: "model-a",
	}).Error)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	effective, applied := ApplyGroupFallback(c, "source", "model-a")
	require.True(t, applied)
	require.Equal(t, "backup", effective)
	require.Equal(t, "backup", common.GetContextKeyString(c, constant.ContextKeyUsingGroup))
	require.True(t, common.GetContextKeyBool(c, constant.ContextKeyGroupFallbackApplied))
	require.Equal(t, "true", c.Writer.Header().Get("X-Group-Fallback-Enabled"))
	require.Equal(t, "source", c.Writer.Header().Get("X-Group-Fallback-Source-Group"))
	require.Equal(t, "backup", c.Writer.Header().Get("X-Group-Fallback-Target-Group"))
	require.Equal(t, "source", GroupForRetry(c, "source", "source"))
	require.Equal(t, "backup", GroupForRetry(c, "source", effective))
}

func TestApplyGroupFallbackLeavesUnmatchedRequestsUntouched(t *testing.T) {
	resetGroupFallbackTestTables(t)
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	effective, applied := ApplyGroupFallback(c, "source", "model-without-fallback")

	require.False(t, applied)
	require.Equal(t, "source", effective)
	require.False(t, common.GetContextKeyBool(c, constant.ContextKeyGroupFallbackApplied))
	require.Empty(t, recorder.Header().Get("X-Group-Fallback-Enabled"))
	require.Empty(t, recorder.Header().Get("X-Group-Fallback-Source-Group"))
	require.Empty(t, recorder.Header().Get("X-Group-Fallback-Target-Group"))
	_, ok := GetGroupFallbackResponseMetadata(c)
	require.False(t, ok)
}

func TestValidateGroupFallbackRejectsDuplicateModelAndMissingChannel(t *testing.T) {
	resetGroupFallbackTestTables(t)
	oldRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"source":1,"backup-a":2,"backup-b":3}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios)) })

	require.NoError(t, model.DB.Create(&model.Channel{Id: 901, Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 902, Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group: "source", Model: "model-a", ChannelId: 901, Enabled: true,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group: "backup-a", Model: "model-a", ChannelId: 902, Enabled: true,
	}).Error)
	require.NoError(t, model.DB.Create(&model.Ability{
		Group: "backup-b", Model: "model-a", ChannelId: 902, Enabled: true,
	}).Error)

	err := validateGroupFallbackConfig(&model.GroupFallbackConfig{
		SourceGroup: "source", Enabled: true,
		Rules: []model.GroupFallbackRuleConfig{
			{TargetGroup: "backup-a", Enabled: true, Models: []string{"model-a"}},
			{TargetGroup: "backup-b", Enabled: true, Models: []string{"model-a"}},
		},
	})
	require.ErrorContains(t, err, "已绑定兜底分组")
	require.NoError(t, model.DB.Model(&model.Ability{}).
		Where("channel_id = ? AND model = ?", 902, "model-a").
		Update("enabled", false).Error)

	err = validateGroupFallbackConfig(&model.GroupFallbackConfig{
		SourceGroup: "source", Enabled: true,
		Rules: []model.GroupFallbackRuleConfig{
			{TargetGroup: "backup-b", Enabled: true, Models: []string{"model-a"}},
		},
	})
	require.ErrorContains(t, err, "没有模型 model-a 的可用渠道")
}

func TestSaveGroupFallbackAllowsModelInDisabledRule(t *testing.T) {
	resetGroupFallbackTestTables(t)
	oldRatios := ratio_setting.GroupRatio2JSONString()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"source":1,"backup-a":2,"backup-b":3}`))
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(oldRatios)) })

	require.NoError(t, model.DB.Create(&model.Channel{Id: 911, Status: common.ChannelStatusEnabled}).Error)
	require.NoError(t, model.DB.Create(&model.Channel{Id: 912, Status: common.ChannelStatusEnabled}).Error)
	for _, ability := range []model.Ability{
		{Group: "source", Model: "model-a", ChannelId: 911, Enabled: true},
		{Group: "backup-a", Model: "model-a", ChannelId: 912, Enabled: true},
		{Group: "backup-b", Model: "model-a", ChannelId: 912, Enabled: true},
	} {
		require.NoError(t, model.DB.Create(&ability).Error)
	}

	err := SaveGroupFallbackConfig(model.GroupFallbackConfig{
		SourceGroup: "source",
		Enabled:     true,
		Rules: []model.GroupFallbackRuleConfig{
			{TargetGroup: "backup-a", Enabled: true, Models: []string{"model-a"}},
			{TargetGroup: "backup-b", Enabled: false, Models: []string{"model-a"}},
		},
	}, 42)
	require.NoError(t, err)

	config, err := model.GetGroupFallbackConfig("source")
	require.NoError(t, err)
	require.Len(t, config.Rules, 2)
	require.Equal(t, 42, config.ActivatedBy)
	decision, ok := ResolveGroupFallback("source", "model-a")
	require.True(t, ok)
	require.Equal(t, "backup-a", decision.TargetGroup)
}
