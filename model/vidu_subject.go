package model

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

const (
	ViduSubjectOwnershipPrivate = "private"
	ViduSubjectOwnershipSystem  = "system"
)

type ViduSubject struct {
	Id uint64 `json:"id" gorm:"primaryKey"`

	CreatedAt int64 `json:"created_at" gorm:"autoCreateTime;index"`
	UpdatedAt int64 `json:"updated_at" gorm:"autoUpdateTime"`

	PublicSubjectID   string `json:"public_subject_id" gorm:"type:varchar(128);not null;uniqueIndex;index"`
	UpstreamSubjectID string `json:"upstream_subject_id" gorm:"type:varchar(128);not null;default:'';index:idx_vidu_subject_upstream,priority:3"`
	Ownership         string `json:"ownership" gorm:"type:varchar(32);not null;default:'private';index"`

	UserID    int    `json:"user_id" gorm:"not null;default:0;index:idx_vidu_subject_user,priority:1"`
	Username  string `json:"username" gorm:"type:varchar(64);not null;default:''"`
	TokenID   int    `json:"token_id" gorm:"not null;default:0;index"`
	TokenName string `json:"token_name" gorm:"type:varchar(128);not null;default:''"`

	ChannelID   int    `json:"channel_id" gorm:"not null;default:0;index:idx_vidu_subject_upstream,priority:1"`
	ChannelName string `json:"channel_name" gorm:"type:varchar(128);not null;default:''"`

	Name        string `json:"name" gorm:"type:varchar(255);not null;default:'';index"`
	ImagesJSON  string `json:"images_json" gorm:"type:text"`
	VideosJSON  string `json:"videos_json" gorm:"type:text"`
	VoiceID     string `json:"voice_id" gorm:"type:varchar(255);not null;default:''"`
	Style       string `json:"style" gorm:"type:varchar(255);not null;default:''"`
	Description string `json:"description" gorm:"type:text"`
	Credits     int    `json:"credits" gorm:"not null;default:0"`
	Status      string `json:"status" gorm:"type:varchar(32);not null;default:'';index"`

	LastUpstreamRaw string `json:"last_upstream_raw" gorm:"type:text"`
}

func (ViduSubject) TableName() string {
	return "vidu_subjects"
}

type ViduSubjectUpsertParams struct {
	PublicSubjectID   string
	UpstreamSubjectID string
	Ownership         string
	UserID            int
	Username          string
	TokenID           int
	TokenName         string
	ChannelID         int
	ChannelName       string
	Name              string
	Images            interface{}
	Videos            interface{}
	VoiceID           string
	Style             string
	Description       string
	Credits           int
	Status            string
	LastUpstreamRaw   string
}

func GenerateViduSubjectID() string {
	return "vidu_subj_" + common.GetUUID()[:24]
}

func UpsertViduSubject(params ViduSubjectUpsertParams) (*ViduSubject, error) {
	params.PublicSubjectID = strings.TrimSpace(params.PublicSubjectID)
	params.UpstreamSubjectID = strings.TrimSpace(params.UpstreamSubjectID)
	params.Ownership = normalizeViduSubjectOwnership(params.Ownership)
	if params.UpstreamSubjectID == "" || params.UserID <= 0 || params.ChannelID <= 0 {
		return nil, nil
	}

	var row ViduSubject
	err := DB.Where("user_id = ? AND channel_id = ? AND upstream_subject_id = ?",
		params.UserID, params.ChannelID, params.UpstreamSubjectID).First(&row).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		row = ViduSubject{
			PublicSubjectID:   firstNonEmpty(params.PublicSubjectID, GenerateViduSubjectID()),
			UpstreamSubjectID: params.UpstreamSubjectID,
			Ownership:         params.Ownership,
			UserID:            params.UserID,
			Username:          strings.TrimSpace(params.Username),
			TokenID:           params.TokenID,
			TokenName:         strings.TrimSpace(params.TokenName),
			ChannelID:         params.ChannelID,
			ChannelName:       strings.TrimSpace(params.ChannelName),
		}
	} else if params.PublicSubjectID != "" {
		row.PublicSubjectID = params.PublicSubjectID
	}

	row.Ownership = params.Ownership
	row.Name = strings.TrimSpace(params.Name)
	row.ImagesJSON = marshalViduSubjectJSON(params.Images)
	row.VideosJSON = marshalViduSubjectJSON(params.Videos)
	row.VoiceID = strings.TrimSpace(params.VoiceID)
	row.Style = strings.TrimSpace(params.Style)
	row.Description = strings.TrimSpace(params.Description)
	row.Credits = params.Credits
	row.Status = strings.TrimSpace(params.Status)
	row.LastUpstreamRaw = params.LastUpstreamRaw
	row.ChannelName = strings.TrimSpace(params.ChannelName)
	row.Username = strings.TrimSpace(params.Username)
	row.TokenID = params.TokenID
	row.TokenName = strings.TrimSpace(params.TokenName)

	if row.Id == 0 {
		if err := DB.Create(&row).Error; err != nil {
			return nil, err
		}
		return &row, nil
	}
	if err := DB.Save(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

func GetViduSubjectByPublicID(userID int, publicSubjectID string) (*ViduSubject, bool, error) {
	publicSubjectID = strings.TrimSpace(publicSubjectID)
	if userID <= 0 || publicSubjectID == "" {
		return nil, false, nil
	}
	var row ViduSubject
	err := DB.Where("user_id = ? AND public_subject_id = ?", userID, publicSubjectID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &row, true, nil
}

func ListViduSubjects(userID, offset, limit int, publicSubjectIDs []string) ([]ViduSubject, int64, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	query := DB.Model(&ViduSubject{}).Where("user_id = ? AND ownership = ?", userID, ViduSubjectOwnershipPrivate)
	if ids := normalizeSubjectIDs(publicSubjectIDs); len(ids) > 0 {
		query = query.Where("public_subject_id IN ?", ids)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []ViduSubject
	err := query.Order("id desc").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}

func DeleteViduSubjectByPublicID(userID int, publicSubjectID string) (bool, error) {
	publicSubjectID = strings.TrimSpace(publicSubjectID)
	if userID <= 0 || publicSubjectID == "" {
		return false, nil
	}
	result := DB.Where("user_id = ? AND public_subject_id = ?", userID, publicSubjectID).Delete(&ViduSubject{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func normalizeViduSubjectOwnership(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == ViduSubjectOwnershipSystem {
		return value
	}
	return ViduSubjectOwnershipPrivate
}

func normalizeSubjectIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func marshalViduSubjectJSON(value interface{}) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
