package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SD2MaterialGroupStatusPending = "pending"
	SD2MaterialGroupStatusReady   = "ready"
	SD2MaterialGroupStatusFailed  = "failed"
)

type SD2MaterialGroup struct {
	Id              uint64 `json:"id" gorm:"primaryKey"`
	ChannelID       int    `json:"channel_id" gorm:"not null;uniqueIndex:uk_sd2_material_group,priority:1;index"`
	GroupName       string `json:"group_name" gorm:"type:varchar(128);not null;default:'';uniqueIndex:uk_sd2_material_group,priority:2"`
	AssetGroupID    string `json:"asset_group_id" gorm:"type:varchar(128);not null;default:'';index"`
	Status          string `json:"status" gorm:"type:varchar(32);not null;default:'pending';index"`
	RetryCount      int    `json:"retry_count" gorm:"not null;default:0"`
	LastError       string `json:"last_error" gorm:"type:text"`
	LastAttemptAt   int64  `json:"last_attempt_at" gorm:"not null;default:0"`
	LastUpstreamRaw string `json:"last_upstream_raw" gorm:"type:text"`
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime;index"`
	UpdatedAt       int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (SD2MaterialGroup) TableName() string {
	return "sd2_material_groups"
}

func EnsureSD2MaterialGroupRow(channelID int, groupName string) (*SD2MaterialGroup, error) {
	now := common.GetTimestamp()
	groupName = strings.TrimSpace(groupName)
	row := SD2MaterialGroup{
		ChannelID: channelID,
		GroupName: groupName,
		Status:    SD2MaterialGroupStatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "channel_id"}, {Name: "group_name"}},
		DoNothing: true,
	}).Create(&row).Error; err != nil {
		return nil, err
	}
	var saved SD2MaterialGroup
	err := DB.Where("channel_id = ? AND group_name = ?", channelID, groupName).First(&saved).Error
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

func MarkSD2MaterialGroupReady(id uint64, assetGroupID, upstreamRaw string) error {
	now := common.GetTimestamp()
	return DB.Model(&SD2MaterialGroup{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":            SD2MaterialGroupStatusReady,
		"asset_group_id":    strings.TrimSpace(assetGroupID),
		"last_error":        "",
		"last_attempt_at":   now,
		"updated_at":        now,
		"last_upstream_raw": upstreamRaw,
	}).Error
}

func MarkSD2MaterialGroupFailed(id uint64, errMsg, upstreamRaw string) error {
	now := common.GetTimestamp()
	return DB.Model(&SD2MaterialGroup{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":            SD2MaterialGroupStatusFailed,
		"retry_count":       gorm.Expr("retry_count + 1"),
		"last_error":        strings.TrimSpace(errMsg),
		"last_attempt_at":   now,
		"updated_at":        now,
		"last_upstream_raw": upstreamRaw,
	}).Error
}
