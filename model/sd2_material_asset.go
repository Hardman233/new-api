package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	SD2MaterialStatusPending = "pending"
	SD2MaterialStatusActive  = "active"
	SD2MaterialStatusFailed  = "failed"
	SD2MaterialStatusTimeout = "timeout"
)

type SD2MaterialAsset struct {
	Id              uint64 `json:"id" gorm:"primaryKey"`
	ChannelID       int    `json:"channel_id" gorm:"not null;uniqueIndex:uk_sd2_material_source,priority:1;index"`
	SourceURLHash   string `json:"source_url_hash" gorm:"type:char(64);not null;uniqueIndex:uk_sd2_material_source,priority:2"`
	AssetGroupID    string `json:"asset_group_id" gorm:"type:varchar(128);not null;default:'';uniqueIndex:uk_sd2_material_source,priority:3;index"`
	SourceURL       string `json:"source_url" gorm:"type:text;not null"`
	MediaKind       string `json:"media_kind" gorm:"type:varchar(32);not null;default:''"`
	AssetID         string `json:"asset_id" gorm:"type:varchar(128);not null;default:'';index"`
	Status          string `json:"status" gorm:"type:varchar(32);not null;default:'pending';index"`
	RetryCount      int    `json:"retry_count" gorm:"not null;default:0"`
	LastError       string `json:"last_error" gorm:"type:text"`
	CheckedAt       int64  `json:"checked_at" gorm:"not null;default:0"`
	ActivatedAt     int64  `json:"activated_at" gorm:"not null;default:0"`
	LastAttemptAt   int64  `json:"last_attempt_at" gorm:"not null;default:0"`
	LastUpstreamRaw string `json:"last_upstream_raw" gorm:"type:text"`
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime;index"`
	UpdatedAt       int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (SD2MaterialAsset) TableName() string {
	return "sd2_material_assets"
}

func GetSD2MaterialAsset(channelID int, groupID, sourceURLHash string) (*SD2MaterialAsset, error) {
	var asset SD2MaterialAsset
	err := DB.Where("channel_id = ? AND asset_group_id = ? AND source_url_hash = ?",
		channelID, strings.TrimSpace(groupID), strings.TrimSpace(sourceURLHash),
	).First(&asset).Error
	if err != nil {
		return nil, err
	}
	return &asset, nil
}

func EnsureSD2MaterialAssetRow(channelID int, groupID, sourceURLHash, sourceURL, mediaKind string) (*SD2MaterialAsset, error) {
	now := common.GetTimestamp()
	row := SD2MaterialAsset{
		ChannelID:     channelID,
		AssetGroupID:  strings.TrimSpace(groupID),
		SourceURLHash: strings.TrimSpace(sourceURLHash),
		SourceURL:     strings.TrimSpace(sourceURL),
		MediaKind:     strings.TrimSpace(mediaKind),
		Status:        SD2MaterialStatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "channel_id"}, {Name: "source_url_hash"}, {Name: "asset_group_id"}},
		DoNothing: true,
	}).Create(&row).Error; err != nil {
		return nil, err
	}
	return GetSD2MaterialAsset(channelID, groupID, sourceURLHash)
}

func MarkSD2MaterialAssetAttempt(id uint64, assetID, status, upstreamRaw string) error {
	now := common.GetTimestamp()
	updates := map[string]interface{}{
		"status":            strings.TrimSpace(status),
		"asset_id":          strings.TrimSpace(assetID),
		"retry_count":       gorm.Expr("retry_count + 1"),
		"last_attempt_at":   now,
		"checked_at":        now,
		"updated_at":        now,
		"last_upstream_raw": upstreamRaw,
		"last_error":        "",
	}
	if updates["status"] == "" {
		updates["status"] = SD2MaterialStatusPending
	}
	return DB.Model(&SD2MaterialAsset{}).Where("id = ?", id).Updates(updates).Error
}

func MarkSD2MaterialAssetActive(id uint64, assetID, upstreamRaw string) error {
	now := common.GetTimestamp()
	return DB.Model(&SD2MaterialAsset{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":            SD2MaterialStatusActive,
		"asset_id":          strings.TrimSpace(assetID),
		"last_error":        "",
		"checked_at":        now,
		"activated_at":      now,
		"updated_at":        now,
		"last_upstream_raw": upstreamRaw,
	}).Error
}

func MarkSD2MaterialAssetFailed(id uint64, status, errMsg, upstreamRaw string) error {
	status = strings.TrimSpace(status)
	if status == "" {
		status = SD2MaterialStatusFailed
	}
	now := common.GetTimestamp()
	return DB.Model(&SD2MaterialAsset{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":            status,
		"last_error":        strings.TrimSpace(errMsg),
		"checked_at":        now,
		"updated_at":        now,
		"last_upstream_raw": upstreamRaw,
	}).Error
}
