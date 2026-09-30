package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm/clause"
)

const (
	SeedanceOpenWhiteStatusPending       = "pending"
	SeedanceOpenWhiteStatusActive        = "active"
	SeedanceOpenWhiteStatusPartialFailed = "partial_failed"
	SeedanceOpenWhiteStatusFailed        = "failed"
	SeedanceOpenWhiteStatusTimeout       = "timeout"
)

type SeedanceOpenWhiteRecord struct {
	Id            uint64 `json:"id" gorm:"primaryKey"`
	OpenWhiteID   string `json:"open_white_id" gorm:"type:varchar(64);not null;uniqueIndex"`
	UserID        int    `json:"user_id" gorm:"not null;default:0;index"`
	TokenID       int    `json:"token_id" gorm:"not null;default:0;index"`
	GroupName     string `json:"group_name" gorm:"type:varchar(128);not null;default:'';index"`
	ModelName     string `json:"model_name" gorm:"type:varchar(255);not null;default:'';index"`
	SourceURLHash string `json:"source_url_hash" gorm:"type:char(64);not null;default:'';index"`
	SourceURL     string `json:"source_url" gorm:"type:text;not null"`
	MediaKind     string `json:"media_kind" gorm:"type:varchar(32);not null;default:'image'"`
	AssetType     string `json:"asset_type" gorm:"type:varchar(32);not null;default:'Image'"`
	Name          string `json:"name" gorm:"type:varchar(255);not null;default:''"`
	RequestNodeID string `json:"request_node_id" gorm:"type:varchar(64);not null;default:'';index"`
	Status        string `json:"status" gorm:"type:varchar(32);not null;default:'pending';index"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime;index"`
	UpdatedAt     int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (SeedanceOpenWhiteRecord) TableName() string {
	return "seedance_open_white_records"
}

type SeedanceOpenWhiteAsset struct {
	Id              uint64 `json:"id" gorm:"primaryKey"`
	OpenWhiteID     string `json:"open_white_id" gorm:"type:varchar(64);not null;uniqueIndex:uk_seedance_ow_asset,priority:1;index"`
	NodeID          string `json:"node_id" gorm:"type:varchar(64);not null;default:'';uniqueIndex:uk_seedance_ow_asset,priority:2;index"`
	Region          string `json:"region" gorm:"type:varchar(64);not null;default:'';index"`
	ChannelID       int    `json:"channel_id" gorm:"not null;default:0;uniqueIndex:uk_seedance_ow_asset,priority:3;index"`
	ChannelName     string `json:"channel_name" gorm:"type:varchar(255);not null;default:''"`
	RouteTag        string `json:"route_tag" gorm:"type:varchar(64);not null;default:'';index"`
	UpstreamAssetID string `json:"upstream_asset_id" gorm:"type:varchar(255);not null;default:'';index"`
	AssetRef        string `json:"asset_ref" gorm:"type:text;not null;default:''"`
	Status          string `json:"status" gorm:"type:varchar(32);not null;default:'pending';index"`
	ErrorCode       string `json:"error_code" gorm:"type:varchar(128);not null;default:''"`
	ErrorMessage    string `json:"error_message" gorm:"type:text"`
	LastUpstreamRaw string `json:"last_upstream_raw" gorm:"type:text"`
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime;index"`
	UpdatedAt       int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (SeedanceOpenWhiteAsset) TableName() string {
	return "seedance_open_white_assets"
}

func EnsureSeedanceOpenWhiteRecord(row *SeedanceOpenWhiteRecord) (*SeedanceOpenWhiteRecord, error) {
	if row == nil {
		row = &SeedanceOpenWhiteRecord{}
	}
	row.OpenWhiteID = strings.TrimSpace(row.OpenWhiteID)
	row.SourceURL = strings.TrimSpace(row.SourceURL)
	row.SourceURLHash = strings.TrimSpace(row.SourceURLHash)
	row.Status = strings.TrimSpace(row.Status)
	if row.Status == "" {
		row.Status = SeedanceOpenWhiteStatusPending
	}
	now := common.GetTimestamp()
	if row.CreatedAt == 0 {
		row.CreatedAt = now
	}
	row.UpdatedAt = now
	if err := DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "open_white_id"}},
		DoNothing: true,
	}).Create(row).Error; err != nil {
		return nil, err
	}
	return GetSeedanceOpenWhiteRecord(row.OpenWhiteID)
}

func GetSeedanceOpenWhiteRecord(openWhiteID string) (*SeedanceOpenWhiteRecord, error) {
	var row SeedanceOpenWhiteRecord
	err := DB.Where("open_white_id = ?", strings.TrimSpace(openWhiteID)).First(&row).Error
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func UpsertSeedanceOpenWhiteAsset(row *SeedanceOpenWhiteAsset) error {
	if row == nil {
		return nil
	}
	row.OpenWhiteID = strings.TrimSpace(row.OpenWhiteID)
	row.NodeID = strings.TrimSpace(row.NodeID)
	row.Region = strings.TrimSpace(row.Region)
	row.RouteTag = strings.TrimSpace(row.RouteTag)
	row.Status = strings.TrimSpace(row.Status)
	if row.Status == "" {
		row.Status = SeedanceOpenWhiteStatusPending
	}
	now := common.GetTimestamp()
	if row.CreatedAt == 0 {
		row.CreatedAt = now
	}
	row.UpdatedAt = now
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "open_white_id"}, {Name: "node_id"}, {Name: "channel_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"region",
			"channel_name",
			"route_tag",
			"upstream_asset_id",
			"asset_ref",
			"status",
			"error_code",
			"error_message",
			"last_upstream_raw",
			"updated_at",
		}),
	}).Create(row).Error
}

func ListSeedanceOpenWhiteAssets(openWhiteID string) ([]SeedanceOpenWhiteAsset, error) {
	var rows []SeedanceOpenWhiteAsset
	err := DB.Where("open_white_id = ?", strings.TrimSpace(openWhiteID)).
		Order("node_id asc, channel_id asc").
		Find(&rows).Error
	return rows, err
}

func UpdateSeedanceOpenWhiteRecordStatus(openWhiteID, status string) error {
	status = strings.TrimSpace(status)
	if status == "" {
		status = SeedanceOpenWhiteStatusPending
	}
	return DB.Model(&SeedanceOpenWhiteRecord{}).
		Where("open_white_id = ?", strings.TrimSpace(openWhiteID)).
		Updates(map[string]interface{}{
			"status":     status,
			"updated_at": common.GetTimestamp(),
		}).Error
}
