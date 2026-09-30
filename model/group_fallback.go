package model

import (
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

type GroupFallbackSetting struct {
	Id                   int    `json:"id"`
	SourceGroup          string `json:"source_group" gorm:"type:varchar(64);not null;uniqueIndex"`
	Enabled              bool   `json:"enabled" gorm:"not null;default:false;index"`
	RetryEnabled         bool   `json:"retry_enabled" gorm:"not null;default:false"`
	RetryMaxRetries      int    `json:"retry_max_retries" gorm:"not null;default:0"`
	RetryHTTPStatusCodes string `json:"retry_http_status_codes" gorm:"type:varchar(255);default:''"`
	Reason               string `json:"reason" gorm:"type:varchar(255);default:''"`
	ActivatedBy          int    `json:"activated_by" gorm:"default:0"`
	ActivatedAt          int64  `json:"activated_at" gorm:"bigint;default:0"`
	CreatedTime          int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime          int64  `json:"updated_time" gorm:"bigint"`
}

type GroupFallbackRule struct {
	Id          int    `json:"id"`
	SourceGroup string `json:"source_group" gorm:"type:varchar(64);not null;uniqueIndex:uk_group_fallback_target,priority:1;index"`
	TargetGroup string `json:"target_group" gorm:"type:varchar(64);not null;uniqueIndex:uk_group_fallback_target,priority:2;index"`
	Enabled     bool   `json:"enabled" gorm:"not null;default:false;index"`
	SortOrder   int    `json:"sort_order" gorm:"not null;default:0"`
	CreatedTime int64  `json:"created_time" gorm:"bigint"`
	UpdatedTime int64  `json:"updated_time" gorm:"bigint"`
}

type GroupFallbackRuleModel struct {
	RuleId      int    `json:"rule_id" gorm:"primaryKey;autoIncrement:false;index"`
	SourceGroup string `json:"source_group" gorm:"type:varchar(64);not null;index"`
	ModelName   string `json:"model_name" gorm:"type:varchar(255);primaryKey;autoIncrement:false;index"`
	CreatedTime int64  `json:"created_time" gorm:"bigint"`
}

type GroupFallbackRuleConfig struct {
	Id          int      `json:"id"`
	TargetGroup string   `json:"target_group"`
	Enabled     bool     `json:"enabled"`
	Models      []string `json:"models"`
}

type GroupFallbackConfig struct {
	SourceGroup          string                    `json:"source_group"`
	Enabled              bool                      `json:"enabled"`
	RetryEnabled         bool                      `json:"retry_enabled"`
	RetryMaxRetries      int                       `json:"retry_max_retries"`
	RetryHTTPStatusCodes string                    `json:"retry_http_status_codes"`
	Reason               string                    `json:"reason"`
	ActivatedBy          int                       `json:"activated_by"`
	ActivatedAt          int64                     `json:"activated_at"`
	UpdatedTime          int64                     `json:"updated_time"`
	Rules                []GroupFallbackRuleConfig `json:"rules"`
}

type GroupFallbackMapping struct {
	RuleId      int
	SourceGroup string
	TargetGroup string
	ModelName   string
}

type GroupModelCapabilities struct {
	Configured map[string][]string `json:"configured"`
	Available  map[string][]string `json:"available"`
}

func fallbackGroupColumnSQL(alias string) string {
	if commonGroupCol != "" {
		return alias + commonGroupCol
	}
	if common.UsingPostgreSQL {
		return alias + `"group"`
	}
	return alias + "`group`"
}

func GetAllGroupFallbackConfigs() ([]GroupFallbackConfig, error) {
	var settings []GroupFallbackSetting
	if err := DB.Order("source_group ASC").Find(&settings).Error; err != nil {
		return nil, err
	}

	var rules []GroupFallbackRule
	if err := DB.Order("source_group ASC, sort_order ASC, id ASC").Find(&rules).Error; err != nil {
		return nil, err
	}

	var ruleModels []GroupFallbackRuleModel
	if err := DB.Order("model_name ASC").Find(&ruleModels).Error; err != nil {
		return nil, err
	}

	modelsByRule := make(map[int][]string)
	for _, item := range ruleModels {
		modelsByRule[item.RuleId] = append(modelsByRule[item.RuleId], item.ModelName)
	}
	rulesBySource := make(map[string][]GroupFallbackRuleConfig)
	for _, rule := range rules {
		rulesBySource[rule.SourceGroup] = append(rulesBySource[rule.SourceGroup], GroupFallbackRuleConfig{
			Id:          rule.Id,
			TargetGroup: rule.TargetGroup,
			Enabled:     rule.Enabled,
			Models:      modelsByRule[rule.Id],
		})
	}

	configs := make([]GroupFallbackConfig, 0, len(settings))
	for _, setting := range settings {
		configs = append(configs, GroupFallbackConfig{
			SourceGroup:          setting.SourceGroup,
			Enabled:              setting.Enabled,
			RetryEnabled:         setting.RetryEnabled,
			RetryMaxRetries:      setting.RetryMaxRetries,
			RetryHTTPStatusCodes: setting.RetryHTTPStatusCodes,
			Reason:               setting.Reason,
			ActivatedBy:          setting.ActivatedBy,
			ActivatedAt:          setting.ActivatedAt,
			UpdatedTime:          setting.UpdatedTime,
			Rules:                rulesBySource[setting.SourceGroup],
		})
	}
	return configs, nil
}

func GetGroupFallbackConfig(sourceGroup string) (*GroupFallbackConfig, error) {
	configs, err := GetAllGroupFallbackConfigs()
	if err != nil {
		return nil, err
	}
	for i := range configs {
		if configs[i].SourceGroup == sourceGroup {
			return &configs[i], nil
		}
	}
	return nil, gorm.ErrRecordNotFound
}

func ReplaceGroupFallbackConfig(config GroupFallbackConfig) error {
	now := common.GetTimestamp()
	return DB.Transaction(func(tx *gorm.DB) error {
		var setting GroupFallbackSetting
		err := tx.Where("source_group = ?", config.SourceGroup).First(&setting).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if err == gorm.ErrRecordNotFound {
			setting = GroupFallbackSetting{
				SourceGroup: config.SourceGroup,
				CreatedTime: now,
			}
		}
		if config.Enabled && (!setting.Enabled || setting.ActivatedAt == 0) {
			setting.ActivatedAt = now
			setting.ActivatedBy = config.ActivatedBy
		}
		setting.Enabled = config.Enabled
		setting.RetryEnabled = config.RetryEnabled
		setting.RetryMaxRetries = config.RetryMaxRetries
		setting.RetryHTTPStatusCodes = config.RetryHTTPStatusCodes
		setting.Reason = config.Reason
		setting.UpdatedTime = now
		if setting.Id == 0 {
			if err := tx.Create(&setting).Error; err != nil {
				return err
			}
		} else if err := tx.Save(&setting).Error; err != nil {
			return err
		}

		var oldRuleIds []int
		if err := tx.Model(&GroupFallbackRule{}).
			Where("source_group = ?", config.SourceGroup).
			Pluck("id", &oldRuleIds).Error; err != nil {
			return err
		}
		if len(oldRuleIds) > 0 {
			if err := tx.Where("rule_id IN ?", oldRuleIds).Delete(&GroupFallbackRuleModel{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("source_group = ?", config.SourceGroup).Delete(&GroupFallbackRule{}).Error; err != nil {
			return err
		}

		for index, inputRule := range config.Rules {
			rule := GroupFallbackRule{
				SourceGroup: config.SourceGroup,
				TargetGroup: inputRule.TargetGroup,
				Enabled:     inputRule.Enabled,
				SortOrder:   index,
				CreatedTime: now,
				UpdatedTime: now,
			}
			if err := tx.Create(&rule).Error; err != nil {
				return err
			}
			models := make([]GroupFallbackRuleModel, 0, len(inputRule.Models))
			for _, modelName := range inputRule.Models {
				models = append(models, GroupFallbackRuleModel{
					RuleId:      rule.Id,
					SourceGroup: config.SourceGroup,
					ModelName:   modelName,
					CreatedTime: now,
				})
			}
			if len(models) > 0 {
				if err := tx.Create(&models).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func GetEnabledGroupFallbackMappings() ([]GroupFallbackMapping, error) {
	var mappings []GroupFallbackMapping
	err := DB.Table("group_fallback_rule_models AS m").
		Select("r.id AS rule_id, r.source_group, r.target_group, m.model_name").
		Joins("JOIN group_fallback_rules AS r ON r.id = m.rule_id").
		Joins("JOIN group_fallback_settings AS s ON s.source_group = r.source_group").
		Where("s.enabled = ? AND r.enabled = ?", true, true).
		Order("r.source_group ASC, r.sort_order ASC, r.id ASC, m.model_name ASC").
		Scan(&mappings).Error
	return mappings, err
}

func GetEnabledGroupFallbackRetrySettings() ([]GroupFallbackSetting, error) {
	var settings []GroupFallbackSetting
	err := DB.
		Where("enabled = ? AND retry_enabled = ?", true, true).
		Order("source_group ASC").
		Find(&settings).Error
	return settings, err
}

// EnsureGroupFallbackSchema removes the legacy model uniqueness constraint.
// A model may be listed in multiple disabled rules, while only enabled rules
// are validated as mutually exclusive at runtime.
func EnsureGroupFallbackSchema() error {
	migrator := DB.Migrator()
	if migrator.HasIndex(&GroupFallbackRuleModel{}, "uk_group_fallback_model") {
		return migrator.DropIndex(&GroupFallbackRuleModel{}, "uk_group_fallback_model")
	}
	return nil
}

func GetGroupModelCapabilities() (GroupModelCapabilities, error) {
	capabilities := GroupModelCapabilities{
		Configured: make(map[string][]string),
		Available:  make(map[string][]string),
	}
	type abilityModel struct {
		GroupName string
		Model     string
	}
	var configured []abilityModel
	if err := DB.Model(&Ability{}).
		Select("DISTINCT " + fallbackGroupColumnSQL("") + " AS group_name, model").
		Scan(&configured).Error; err != nil {
		return capabilities, err
	}
	var available []abilityModel
	if err := DB.Table("abilities AS a").
		Select("DISTINCT "+fallbackGroupColumnSQL("a.")+" AS group_name, a.model").
		Joins("JOIN channels AS c ON c.id = a.channel_id").
		Where("a.enabled = ? AND c.status = ?", true, common.ChannelStatusEnabled).
		Scan(&available).Error; err != nil {
		return capabilities, err
	}
	for _, item := range configured {
		group := strings.TrimSpace(item.GroupName)
		modelName := strings.TrimSpace(item.Model)
		if group != "" && modelName != "" {
			capabilities.Configured[group] = append(capabilities.Configured[group], modelName)
		}
	}
	for _, item := range available {
		group := strings.TrimSpace(item.GroupName)
		modelName := strings.TrimSpace(item.Model)
		if group != "" && modelName != "" {
			capabilities.Available[group] = append(capabilities.Available[group], modelName)
		}
	}
	for group := range capabilities.Configured {
		capabilities.Configured[group] = uniqueSortedStrings(capabilities.Configured[group])
	}
	for group := range capabilities.Available {
		capabilities.Available[group] = uniqueSortedStrings(capabilities.Available[group])
	}
	return capabilities, nil
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
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
