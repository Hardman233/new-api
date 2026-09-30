package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm/clause"
)

const (
	GeneratedMediaKindImage = "image"
	GeneratedMediaKindVideo = "video"

	GeneratedMediaSourceResponseBase64 = "response_base64"
	GeneratedMediaSourceResponseURL    = "response_url"
	GeneratedMediaSourceTaskURL        = "task_url"

	GeneratedMediaStatusPending    = "pending"
	GeneratedMediaStatusProcessing = "processing"
	GeneratedMediaStatusUploaded   = "uploaded"
	GeneratedMediaStatusFailed     = "failed"
	GeneratedMediaStatusAbandoned  = "abandoned"
)

type GeneratedMediaObject struct {
	Id uint64 `json:"id" gorm:"primaryKey"`

	CreatedAt   int64 `json:"created_at" gorm:"bigint;index"`
	UpdatedAt   int64 `json:"updated_at" gorm:"bigint;index"`
	NextRetryAt int64 `json:"next_retry_at" gorm:"bigint;index:idx_generated_media_status_retry,priority:2"`
	UploadedAt  int64 `json:"uploaded_at" gorm:"bigint"`

	SourceType string `json:"source_type" gorm:"type:varchar(32);not null;default:'';uniqueIndex:uk_generated_media_source,priority:1"`
	MediaKind  string `json:"media_kind" gorm:"type:varchar(16);not null;default:'';index"`
	SourceHash string `json:"source_hash" gorm:"type:char(64);not null;default:'';uniqueIndex:uk_generated_media_source,priority:2"`
	SourceURL  string `json:"source_url" gorm:"type:text"`
	LocalPath  string `json:"local_path" gorm:"type:varchar(512);not null;default:''"`

	RequestId string `json:"request_id" gorm:"type:varchar(64);not null;default:'';uniqueIndex:uk_generated_media_source,priority:3;index"`
	BillingID string `json:"billing_id" gorm:"type:varchar(192);not null;default:'';index"`
	TaskID    string `json:"task_id" gorm:"type:varchar(128);not null;default:'';uniqueIndex:uk_generated_media_source,priority:4;index"`

	UserID    int    `json:"user_id" gorm:"not null;default:0;index"`
	Username  string `json:"username" gorm:"type:varchar(64);not null;default:''"`
	TokenID   int    `json:"token_id" gorm:"not null;default:0;index"`
	TokenName string `json:"token_name" gorm:"type:varchar(128);not null;default:''"`
	UserGroup string `json:"user_group" gorm:"type:varchar(64);not null;default:'';index"`

	ChannelID    int    `json:"channel_id" gorm:"not null;default:0;index"`
	ChannelName  string `json:"channel_name" gorm:"type:varchar(128);not null;default:''"`
	ModelName    string `json:"model_name" gorm:"type:varchar(128);not null;default:'';index"`
	ServerRegion string `json:"server_region" gorm:"type:varchar(32);not null;default:'';index"`

	ContentType string `json:"content_type" gorm:"type:varchar(128);not null;default:''"`
	SizeBytes   int64  `json:"size_bytes" gorm:"not null;default:0"`
	Sha256      string `json:"sha256" gorm:"type:char(64);not null;default:''"`
	OssKey      string `json:"oss_key" gorm:"type:varchar(512);not null;default:'';index"`
	OssURL      string `json:"oss_url" gorm:"type:varchar(1024);not null;default:''"`

	Status     string `json:"status" gorm:"type:varchar(16);not null;default:'pending';index:idx_generated_media_status_retry,priority:1"`
	RetryCount int    `json:"retry_count" gorm:"not null;default:0"`
	LastError  string `json:"last_error" gorm:"type:text"`
	Other      string `json:"other" gorm:"type:text"`
}

func (GeneratedMediaObject) TableName() string {
	return "generated_media_objects"
}

type GeneratedMediaObjectParams struct {
	SourceType   string
	MediaKind    string
	SourceHash   string
	SourceURL    string
	LocalPath    string
	RequestId    string
	BillingID    string
	TaskID       string
	UserID       int
	Username     string
	TokenID      int
	TokenName    string
	UserGroup    string
	ChannelID    int
	ChannelName  string
	ModelName    string
	ServerRegion string
	ContentType  string
	SizeBytes    int64
	Sha256       string
	OssKey       string
	OssURL       string
	Other        string
}

func EnsureGeneratedMediaObjectPending(params GeneratedMediaObjectParams) (*GeneratedMediaObject, bool, error) {
	params.SourceType = strings.TrimSpace(params.SourceType)
	params.MediaKind = strings.TrimSpace(params.MediaKind)
	params.SourceHash = strings.TrimSpace(params.SourceHash)
	if params.SourceType == "" || params.MediaKind == "" || params.SourceHash == "" {
		return nil, false, nil
	}
	now := common.GetTimestamp()
	row := GeneratedMediaObject{
		CreatedAt:    now,
		UpdatedAt:    now,
		NextRetryAt:  now,
		SourceType:   params.SourceType,
		MediaKind:    params.MediaKind,
		SourceHash:   params.SourceHash,
		SourceURL:    strings.TrimSpace(params.SourceURL),
		LocalPath:    strings.TrimSpace(params.LocalPath),
		RequestId:    strings.TrimSpace(params.RequestId),
		BillingID:    strings.TrimSpace(params.BillingID),
		TaskID:       strings.TrimSpace(params.TaskID),
		UserID:       params.UserID,
		Username:     strings.TrimSpace(params.Username),
		TokenID:      params.TokenID,
		TokenName:    strings.TrimSpace(params.TokenName),
		UserGroup:    strings.TrimSpace(params.UserGroup),
		ChannelID:    params.ChannelID,
		ChannelName:  strings.TrimSpace(params.ChannelName),
		ModelName:    strings.TrimSpace(params.ModelName),
		ServerRegion: strings.TrimSpace(params.ServerRegion),
		ContentType:  strings.TrimSpace(params.ContentType),
		SizeBytes:    params.SizeBytes,
		Sha256:       strings.TrimSpace(params.Sha256),
		OssKey:       strings.TrimSpace(params.OssKey),
		OssURL:       strings.TrimSpace(params.OssURL),
		Status:       GeneratedMediaStatusPending,
		Other:        params.Other,
	}
	result := DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "source_type"},
			{Name: "source_hash"},
			{Name: "request_id"},
			{Name: "task_id"},
		},
		DoNothing: true,
	}).Create(&row)
	if result.Error != nil {
		return nil, false, result.Error
	}
	if result.RowsAffected > 0 {
		return &row, true, nil
	}
	var existing GeneratedMediaObject
	err := DB.Where("source_type = ? AND source_hash = ? AND request_id = ? AND task_id = ?",
		row.SourceType, row.SourceHash, row.RequestId, row.TaskID).First(&existing).Error
	if err != nil {
		return nil, false, err
	}
	return &existing, false, nil
}

func GetReadyGeneratedMediaObjects(limit int) ([]*GeneratedMediaObject, error) {
	if limit <= 0 {
		limit = 100
	}
	now := common.GetTimestamp()
	staleProcessingBefore := now - 15*60
	var rows []*GeneratedMediaObject
	err := DB.Where(
		"(status IN ? AND next_retry_at <= ?) OR (status = ? AND updated_at <= ?)",
		[]string{GeneratedMediaStatusPending, GeneratedMediaStatusFailed},
		now,
		GeneratedMediaStatusProcessing,
		staleProcessingBefore,
	).
		Order("next_retry_at asc, id asc").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}

func ClaimGeneratedMediaObject(id uint64) (bool, error) {
	now := common.GetTimestamp()
	staleProcessingBefore := now - 15*60
	result := DB.Model(&GeneratedMediaObject{}).
		Where("id = ?", id).
		Where("(status IN ? AND next_retry_at <= ?) OR (status = ? AND updated_at <= ?)",
			[]string{GeneratedMediaStatusPending, GeneratedMediaStatusFailed},
			now,
			GeneratedMediaStatusProcessing,
			staleProcessingBefore,
		).
		Updates(map[string]interface{}{
			"status":     GeneratedMediaStatusProcessing,
			"updated_at": now,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func UpdateGeneratedMediaLocal(id uint64, localPath, contentType, sha256 string, sizeBytes int64) error {
	return DB.Model(&GeneratedMediaObject{}).Where("id = ?", id).Updates(map[string]interface{}{
		"local_path":   strings.TrimSpace(localPath),
		"content_type": strings.TrimSpace(contentType),
		"sha256":       strings.TrimSpace(sha256),
		"size_bytes":   sizeBytes,
		"updated_at":   common.GetTimestamp(),
	}).Error
}

func MarkGeneratedMediaUploaded(id uint64, ossKey, ossURL, contentType, sha256 string, sizeBytes int64) error {
	now := common.GetTimestamp()
	return DB.Model(&GeneratedMediaObject{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":       GeneratedMediaStatusUploaded,
		"local_path":   "",
		"oss_key":      strings.TrimSpace(ossKey),
		"oss_url":      strings.TrimSpace(ossURL),
		"content_type": strings.TrimSpace(contentType),
		"sha256":       strings.TrimSpace(sha256),
		"size_bytes":   sizeBytes,
		"last_error":   "",
		"updated_at":   now,
		"uploaded_at":  now,
	}).Error
}

func MarkGeneratedMediaFailed(id uint64, lastErr string, retryCount int, nextRetryAt int64) error {
	return DB.Model(&GeneratedMediaObject{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":        GeneratedMediaStatusFailed,
		"retry_count":   retryCount,
		"next_retry_at": nextRetryAt,
		"last_error":    lastErr,
		"updated_at":    common.GetTimestamp(),
	}).Error
}

func MarkGeneratedMediaAbandoned(id uint64, lastErr string) error {
	return DB.Model(&GeneratedMediaObject{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":     GeneratedMediaStatusAbandoned,
		"last_error": lastErr,
		"updated_at": common.GetTimestamp(),
	}).Error
}

func GetGeneratedMediaSizeBytesByOSSRef(mediaKind, taskID, ossKey, ossURL string) (int64, error) {
	mediaKind = strings.TrimSpace(mediaKind)
	taskID = strings.TrimSpace(taskID)
	ossKey = strings.TrimSpace(ossKey)
	ossURL = strings.TrimSpace(ossURL)

	var clauses []string
	var args []interface{}
	if taskID != "" {
		clauses = append(clauses, "task_id = ?")
		args = append(args, taskID)
	}
	if ossKey != "" {
		clauses = append(clauses, "oss_key = ?")
		args = append(args, ossKey)
	}
	if ossURL != "" {
		clauses = append(clauses, "oss_url = ?")
		args = append(args, ossURL)
	}
	if len(clauses) == 0 {
		return 0, nil
	}

	var row GeneratedMediaObject
	query := DB.Model(&GeneratedMediaObject{}).
		Select("size_bytes").
		Where("status = ? AND size_bytes > 0", GeneratedMediaStatusUploaded).
		Where("("+strings.Join(clauses, " OR ")+")", args...)
	if mediaKind != "" {
		query = query.Where("media_kind = ?", mediaKind)
	}
	if err := query.Order("uploaded_at desc, id desc").Limit(1).Find(&row).Error; err != nil {
		return 0, err
	}
	return row.SizeBytes, nil
}

func UpdateTaskGeneratedMediaResult(taskID, ossKey, ossURL string, sizeBytes ...int64) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	var task Task
	if err := DB.Where("task_id = ?", taskID).First(&task).Error; err != nil {
		return err
	}
	if strings.TrimSpace(ossKey) != "" {
		task.PrivateData.OssResultKey = strings.TrimSpace(ossKey)
	}
	if strings.TrimSpace(ossURL) != "" {
		task.PrivateData.OssResultURL = strings.TrimSpace(ossURL)
	}
	if len(sizeBytes) > 0 && sizeBytes[0] > 0 {
		task.PrivateData.OssResultSizeBytes = sizeBytes[0]
	}
	task.sanitizePrivateDataForPersistence()
	return DB.Model(&Task{}).Where("id = ?", task.ID).Update("private_data", task.PrivateData).Error
}
