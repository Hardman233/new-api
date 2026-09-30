package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	AuditOssStatusPending   = "pending"
	AuditOssStatusUploaded  = "uploaded"
	AuditOssStatusMissing   = "missing"
	AuditOssStatusRepaired  = "repaired"
	AuditOssStatusFailed    = "failed"
	AuditOssStatusAbandoned = "abandoned"
)

type AuditOssObject struct {
	Id uint64 `json:"id" gorm:"primaryKey"`

	AuditLogId uint64 `json:"audit_log_id" gorm:"uniqueIndex:uk_audit_oss_body,priority:1;index"`
	RequestId  string `json:"request_id" gorm:"type:varchar(64);index;default:''"`
	BodyKind   string `json:"body_kind" gorm:"type:varchar(8);uniqueIndex:uk_audit_oss_body,priority:2;default:''"`

	LocalPath string `json:"local_path" gorm:"type:varchar(512);default:''"`
	OssKey    string `json:"oss_key" gorm:"type:varchar(512);index;default:''"`
	SizeBytes int64  `json:"size_bytes"`
	Sha256    string `json:"sha256" gorm:"type:char(64);default:''"`

	Status     string `json:"status" gorm:"type:varchar(16);index:idx_audit_oss_status_checked,priority:1;default:'pending'"`
	RetryCount int    `json:"retry_count"`
	LastError  string `json:"last_error" gorm:"type:text"`

	CreatedAt  int64 `json:"created_at" gorm:"bigint;index"`
	CheckedAt  int64 `json:"checked_at" gorm:"bigint;index:idx_audit_oss_status_checked,priority:2"`
	UploadedAt int64 `json:"uploaded_at" gorm:"bigint"`
}

func (AuditOssObject) TableName() string {
	return "audit_oss_objects"
}

type AuditOssObjectParams struct {
	AuditLogId uint64
	RequestId  string
	BodyKind   string
	LocalPath  string
	OssKey     string
	SizeBytes  int64
	Sha256     string
}

func EnsureAuditOssObjectPending(params AuditOssObjectParams) error {
	params.BodyKind = strings.TrimSpace(params.BodyKind)
	params.OssKey = strings.TrimSpace(params.OssKey)
	if params.AuditLogId == 0 || params.BodyKind == "" {
		return nil
	}
	now := common.GetTimestamp()
	row := AuditOssObject{
		AuditLogId: params.AuditLogId,
		RequestId:  params.RequestId,
		BodyKind:   params.BodyKind,
		LocalPath:  params.LocalPath,
		OssKey:     params.OssKey,
		SizeBytes:  params.SizeBytes,
		Sha256:     params.Sha256,
		Status:     AuditOssStatusPending,
		CreatedAt:  now,
	}
	return LOG_DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "audit_log_id"}, {Name: "body_kind"}},
		DoNothing: true,
	}).Create(&row).Error
}

func MarkAuditOssObjectUploaded(auditLogId uint64, bodyKind string, repaired bool) error {
	status := AuditOssStatusUploaded
	if repaired {
		status = AuditOssStatusRepaired
	}
	now := common.GetTimestamp()
	return LOG_DB.Model(&AuditOssObject{}).
		Where("audit_log_id = ? AND body_kind = ?", auditLogId, strings.TrimSpace(bodyKind)).
		Updates(map[string]interface{}{
			"status":      status,
			"last_error":  "",
			"checked_at":  now,
			"uploaded_at": now,
		}).Error
}

func MarkAuditOssObjectChecked(auditLogId uint64, bodyKind string) error {
	return LOG_DB.Model(&AuditOssObject{}).
		Where("audit_log_id = ? AND body_kind = ?", auditLogId, strings.TrimSpace(bodyKind)).
		Update("checked_at", common.GetTimestamp()).Error
}

func MarkAuditOssObjectFailed(auditLogId uint64, bodyKind string, lastErr string) error {
	return LOG_DB.Model(&AuditOssObject{}).
		Where("audit_log_id = ? AND body_kind = ?", auditLogId, strings.TrimSpace(bodyKind)).
		Updates(map[string]interface{}{
			"status":      AuditOssStatusFailed,
			"retry_count": gorm.Expr("retry_count + ?", 1),
			"last_error":  lastErr,
			"checked_at":  common.GetTimestamp(),
		}).Error
}

func MarkAuditOssObjectMissing(auditLogId uint64, bodyKind string, lastErr string) error {
	return LOG_DB.Model(&AuditOssObject{}).
		Where("audit_log_id = ? AND body_kind = ?", auditLogId, strings.TrimSpace(bodyKind)).
		Updates(map[string]interface{}{
			"status":     AuditOssStatusMissing,
			"last_error": lastErr,
			"checked_at": common.GetTimestamp(),
		}).Error
}

func MarkAuditOssObjectAbandoned(auditLogId uint64, bodyKind string, lastErr string) error {
	return LOG_DB.Model(&AuditOssObject{}).
		Where("audit_log_id = ? AND body_kind = ?", auditLogId, strings.TrimSpace(bodyKind)).
		Updates(map[string]interface{}{
			"status":     AuditOssStatusAbandoned,
			"last_error": lastErr,
			"checked_at": common.GetTimestamp(),
		}).Error
}

func GetAuditOssObjectsForReconcile(limit int) ([]*AuditOssObject, error) {
	if limit <= 0 {
		limit = 200
	}
	var rows []*AuditOssObject
	err := LOG_DB.Where("status IN ? AND oss_key <> ?", []string{
		AuditOssStatusPending,
		AuditOssStatusUploaded,
		AuditOssStatusRepaired,
		AuditOssStatusFailed,
		AuditOssStatusMissing,
	}, "").
		Order("checked_at asc, id asc").
		Limit(limit).
		Find(&rows).Error
	return rows, err
}
