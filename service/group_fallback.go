package service

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

const (
	groupFallbackCacheTTL   = 5 * time.Second
	MaxGroupFallbackRetries = 10
)

type GroupFallbackDecision struct {
	RuleId      int    `json:"rule_id"`
	SourceGroup string `json:"source_group"`
	TargetGroup string `json:"target_group"`
	ModelName   string `json:"model_name"`
}

type GroupFallbackRetryPolicy struct {
	MaxRetries      int    `json:"max_retries"`
	HTTPStatusCodes string `json:"http_status_codes"`
}

type GroupFallbackResponseMetadata struct {
	Enabled     bool   `json:"enabled"`
	SourceGroup string `json:"source_group"`
	TargetGroup string `json:"target_group"`
	ChannelId   int    `json:"channel_id,omitempty"`
	ChannelName string `json:"channel_name,omitempty"`
}

type GroupFallbackAdminData struct {
	Groups  []GroupFallbackGroupView    `json:"groups"`
	Configs []model.GroupFallbackConfig `json:"configs"`
}

type GroupFallbackGroupView struct {
	Name             string   `json:"name"`
	Ratio            float64  `json:"ratio"`
	ConfiguredModels []string `json:"configured_models"`
	AvailableModels  []string `json:"available_models"`
}

var groupFallbackRuntimeCache = struct {
	sync.RWMutex
	loadedAt time.Time
	items    map[string]GroupFallbackDecision
	retries  map[string]GroupFallbackRetryPolicy
}{
	items:   make(map[string]GroupFallbackDecision),
	retries: make(map[string]GroupFallbackRetryPolicy),
}

func GetGroupFallbackAdminData() (*GroupFallbackAdminData, error) {
	configs, err := model.GetAllGroupFallbackConfigs()
	if err != nil {
		return nil, err
	}
	capabilities, err := model.GetGroupModelCapabilities()
	if err != nil {
		return nil, err
	}
	ratioMap := ratio_setting.GetGroupRatioCopy()
	groups := make([]GroupFallbackGroupView, 0, len(ratioMap))
	for groupName, ratio := range ratioMap {
		if groupName == "auto" {
			continue
		}
		groups = append(groups, GroupFallbackGroupView{
			Name:             groupName,
			Ratio:            ratio,
			ConfiguredModels: capabilities.Configured[groupName],
			AvailableModels:  capabilities.Available[groupName],
		})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return &GroupFallbackAdminData{Groups: groups, Configs: configs}, nil
}

func SaveGroupFallbackConfig(config model.GroupFallbackConfig, actorId int) error {
	config.SourceGroup = strings.TrimSpace(config.SourceGroup)
	config.Reason = strings.TrimSpace(config.Reason)
	config.RetryHTTPStatusCodes = strings.TrimSpace(config.RetryHTTPStatusCodes)
	config.ActivatedBy = actorId
	if err := validateGroupFallbackConfig(&config); err != nil {
		return err
	}
	if err := model.ReplaceGroupFallbackConfig(config); err != nil {
		return err
	}
	InvalidateGroupFallbackCache()
	return nil
}

func validateGroupFallbackConfig(config *model.GroupFallbackConfig) error {
	if config == nil {
		return fmt.Errorf("兜底配置不能为空")
	}
	if config.SourceGroup == "" {
		return fmt.Errorf("源分组不能为空")
	}
	if config.SourceGroup == "auto" {
		return fmt.Errorf("auto 分组暂不支持兜底配置")
	}
	if config.RetryEnabled {
		if !config.Enabled {
			return fmt.Errorf("retry-before-fallback requires group fallback to be enabled")
		}
		if config.RetryMaxRetries < 1 || config.RetryMaxRetries > MaxGroupFallbackRetries {
			return fmt.Errorf("group retry max_retries must be between 1 and %d", MaxGroupFallbackRetries)
		}
		ranges, err := operation_setting.ParseHTTPStatusCodeRanges(config.RetryHTTPStatusCodes)
		if err != nil {
			return fmt.Errorf("invalid group retry HTTP status codes: %w", err)
		}
		if len(ranges) == 0 {
			return fmt.Errorf("group retry HTTP status codes are required")
		}
		for _, statusRange := range ranges {
			if statusRange.Start < http.StatusMultipleChoices && statusRange.End >= http.StatusOK {
				return fmt.Errorf("group retry HTTP status codes cannot include successful 2xx responses")
			}
		}
	}
	ratioMap := ratio_setting.GetGroupRatioCopy()
	if _, exists := ratioMap[config.SourceGroup]; !exists {
		return fmt.Errorf("源分组 %s 不存在", config.SourceGroup)
	}
	capabilities, err := model.GetGroupModelCapabilities()
	if err != nil {
		return err
	}
	sourceModels := stringSet(capabilities.Configured[config.SourceGroup])
	seenTargets := make(map[string]struct{}, len(config.Rules))
	seenModels := make(map[string]string)
	enabledRules := 0

	for index := range config.Rules {
		rule := &config.Rules[index]
		rule.TargetGroup = strings.TrimSpace(rule.TargetGroup)
		if rule.TargetGroup == "" {
			return fmt.Errorf("第 %d 条规则的兜底分组不能为空", index+1)
		}
		if rule.TargetGroup == "auto" {
			return fmt.Errorf("auto 不能作为兜底分组")
		}
		if rule.TargetGroup == config.SourceGroup {
			return fmt.Errorf("源分组和兜底分组不能相同")
		}
		if _, exists := ratioMap[rule.TargetGroup]; !exists {
			return fmt.Errorf("兜底分组 %s 不存在", rule.TargetGroup)
		}
		if _, duplicate := seenTargets[rule.TargetGroup]; duplicate {
			return fmt.Errorf("兜底分组 %s 重复配置", rule.TargetGroup)
		}
		seenTargets[rule.TargetGroup] = struct{}{}
		rule.Models = uniqueTrimmedStrings(rule.Models)
		ruleActive := config.Enabled && rule.Enabled
		if ruleActive {
			enabledRules++
			if len(rule.Models) == 0 {
				return fmt.Errorf("已开启的兜底分组 %s 至少选择一个模型", rule.TargetGroup)
			}
		}

		targetConfigured := stringSet(capabilities.Configured[rule.TargetGroup])
		targetAvailable := stringSet(capabilities.Available[rule.TargetGroup])
		for _, modelName := range rule.Models {
			if _, exists := sourceModels[modelName]; !exists {
				return fmt.Errorf("模型 %s 不属于源分组 %s", modelName, config.SourceGroup)
			}
			if _, exists := targetConfigured[modelName]; !exists {
				return fmt.Errorf("模型 %s 不属于兜底分组 %s", modelName, rule.TargetGroup)
			}
			if ruleActive {
				if _, exists := targetAvailable[modelName]; !exists {
					return fmt.Errorf("兜底分组 %s 当前没有模型 %s 的可用渠道", rule.TargetGroup, modelName)
				}
			}
			if ruleActive {
				if previousTarget, duplicate := seenModels[modelName]; duplicate {
					return fmt.Errorf("模型 %s 已绑定兜底分组 %s，不能重复绑定 %s", modelName, previousTarget, rule.TargetGroup)
				}
				seenModels[modelName] = rule.TargetGroup
			}
		}
	}
	if config.Enabled && enabledRules == 0 {
		return fmt.Errorf("开启分组兜底前至少启用一条规则")
	}
	return nil
}

func ResolveGroupFallback(sourceGroup, modelName string) (GroupFallbackDecision, bool) {
	sourceGroup = strings.TrimSpace(sourceGroup)
	modelName = strings.TrimSpace(modelName)
	if sourceGroup == "" || sourceGroup == "auto" || modelName == "" {
		return GroupFallbackDecision{}, false
	}
	if err := ensureGroupFallbackCache(); err != nil {
		return GroupFallbackDecision{}, false
	}
	groupFallbackRuntimeCache.RLock()
	defer groupFallbackRuntimeCache.RUnlock()
	if decision, exists := groupFallbackRuntimeCache.items[groupFallbackCacheKey(sourceGroup, modelName)]; exists {
		return decision, true
	}
	normalized := ratio_setting.FormatMatchingModelName(modelName)
	if normalized != "" && normalized != modelName {
		decision, exists := groupFallbackRuntimeCache.items[groupFallbackCacheKey(sourceGroup, normalized)]
		return decision, exists
	}
	return GroupFallbackDecision{}, false
}

func ResolveGroupFallbackRetry(sourceGroup, modelName string) (GroupFallbackDecision, GroupFallbackRetryPolicy, bool) {
	sourceGroup = strings.TrimSpace(sourceGroup)
	modelName = strings.TrimSpace(modelName)
	if sourceGroup == "" || sourceGroup == "auto" || modelName == "" {
		return GroupFallbackDecision{}, GroupFallbackRetryPolicy{}, false
	}
	if err := ensureGroupFallbackCache(); err != nil {
		return GroupFallbackDecision{}, GroupFallbackRetryPolicy{}, false
	}
	groupFallbackRuntimeCache.RLock()
	defer groupFallbackRuntimeCache.RUnlock()
	policy, configured := groupFallbackRuntimeCache.retries[sourceGroup]
	if !configured || policy.MaxRetries < 1 || policy.MaxRetries > MaxGroupFallbackRetries {
		return GroupFallbackDecision{}, GroupFallbackRetryPolicy{}, false
	}
	ranges, err := operation_setting.ParseHTTPStatusCodeRanges(policy.HTTPStatusCodes)
	if err != nil || len(ranges) == 0 {
		return GroupFallbackDecision{}, GroupFallbackRetryPolicy{}, false
	}
	for _, statusRange := range ranges {
		if statusRange.Start < http.StatusMultipleChoices && statusRange.End >= http.StatusOK {
			return GroupFallbackDecision{}, GroupFallbackRetryPolicy{}, false
		}
	}
	decision, mapped := groupFallbackRuntimeCache.items[groupFallbackCacheKey(sourceGroup, modelName)]
	if !mapped {
		normalized := ratio_setting.FormatMatchingModelName(modelName)
		if normalized != "" && normalized != modelName {
			decision, mapped = groupFallbackRuntimeCache.items[groupFallbackCacheKey(sourceGroup, normalized)]
		}
	}
	if !mapped {
		return GroupFallbackDecision{}, GroupFallbackRetryPolicy{}, false
	}
	return decision, policy, true
}

func ShouldDeferGroupFallback(sourceGroup, modelName string) bool {
	_, _, enabled := ResolveGroupFallbackRetry(sourceGroup, modelName)
	return enabled
}

func ApplyGroupFallback(c *gin.Context, sourceGroup, modelName string) (string, bool) {
	sourceGroup = strings.TrimSpace(sourceGroup)
	decision, applied := ResolveGroupFallback(sourceGroup, modelName)
	if !applied {
		if sourceGroup == "" {
			sourceGroup = common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
		}
		if sourceGroup == "" {
			sourceGroup = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		}
		return sourceGroup, false
	}
	common.SetContextKey(c, constant.ContextKeyGroupFallbackApplied, true)
	common.SetContextKey(c, constant.ContextKeyGroupFallbackRuleId, decision.RuleId)
	common.SetContextKey(c, constant.ContextKeyGroupFallbackSourceGroup, decision.SourceGroup)
	common.SetContextKey(c, constant.ContextKeyUsingGroup, decision.TargetGroup)
	setGroupFallbackResponseHeaders(c, true, decision.SourceGroup, decision.TargetGroup)
	return decision.TargetGroup, true
}

func GetGroupFallbackResponseMetadata(c *gin.Context) (GroupFallbackResponseMetadata, bool) {
	if c == nil || !common.GetContextKeyBool(c, constant.ContextKeyGroupFallbackApplied) {
		return GroupFallbackResponseMetadata{}, false
	}

	sourceGroup := common.GetContextKeyString(c, constant.ContextKeyGroupFallbackSourceGroup)
	if sourceGroup == "" {
		sourceGroup = common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	}
	if sourceGroup == "" {
		sourceGroup = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
	}
	sourceGroup = strings.TrimSpace(sourceGroup)
	if sourceGroup == "" {
		return GroupFallbackResponseMetadata{}, false
	}

	metadata := GroupFallbackResponseMetadata{
		Enabled:     true,
		SourceGroup: sourceGroup,
		TargetGroup: strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyUsingGroup)),
		ChannelId:   common.GetContextKeyInt(c, constant.ContextKeyChannelId),
		ChannelName: strings.TrimSpace(common.GetContextKeyString(c, constant.ContextKeyChannelName)),
	}
	return metadata, true
}

func setGroupFallbackResponseHeaders(c *gin.Context, enabled bool, sourceGroup, targetGroup string) {
	if c == nil {
		return
	}
	c.Header("X-Group-Fallback-Enabled", strconv.FormatBool(enabled))
	c.Header("X-Group-Fallback-Source-Group", strings.TrimSpace(sourceGroup))
	c.Header("X-Group-Fallback-Target-Group", strings.TrimSpace(targetGroup))
}

func GroupForRetry(c *gin.Context, tokenGroup, usingGroup string) string {
	if common.GetContextKeyBool(c, constant.ContextKeyGroupFallbackApplied) {
		return usingGroup
	}
	return tokenGroup
}

func InvalidateGroupFallbackCache() {
	groupFallbackRuntimeCache.Lock()
	groupFallbackRuntimeCache.loadedAt = time.Time{}
	groupFallbackRuntimeCache.items = make(map[string]GroupFallbackDecision)
	groupFallbackRuntimeCache.retries = make(map[string]GroupFallbackRetryPolicy)
	groupFallbackRuntimeCache.Unlock()
}

func ensureGroupFallbackCache() error {
	groupFallbackRuntimeCache.Lock()
	defer groupFallbackRuntimeCache.Unlock()
	if !groupFallbackRuntimeCache.loadedAt.IsZero() && time.Since(groupFallbackRuntimeCache.loadedAt) < groupFallbackCacheTTL {
		return nil
	}
	mappings, err := model.GetEnabledGroupFallbackMappings()
	if err != nil {
		return err
	}
	retrySettings, err := model.GetEnabledGroupFallbackRetrySettings()
	if err != nil {
		return err
	}
	items := make(map[string]GroupFallbackDecision, len(mappings))
	for _, mapping := range mappings {
		items[groupFallbackCacheKey(mapping.SourceGroup, mapping.ModelName)] = GroupFallbackDecision{
			RuleId:      mapping.RuleId,
			SourceGroup: mapping.SourceGroup,
			TargetGroup: mapping.TargetGroup,
			ModelName:   mapping.ModelName,
		}
	}
	retries := make(map[string]GroupFallbackRetryPolicy, len(retrySettings))
	for _, setting := range retrySettings {
		retries[strings.TrimSpace(setting.SourceGroup)] = GroupFallbackRetryPolicy{
			MaxRetries:      setting.RetryMaxRetries,
			HTTPStatusCodes: strings.TrimSpace(setting.RetryHTTPStatusCodes),
		}
	}
	groupFallbackRuntimeCache.items = items
	groupFallbackRuntimeCache.retries = retries
	groupFallbackRuntimeCache.loadedAt = time.Now()
	return nil
}

func groupFallbackCacheKey(group, modelName string) string {
	return strings.TrimSpace(group) + "\x00" + strings.TrimSpace(modelName)
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func uniqueTrimmedStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
