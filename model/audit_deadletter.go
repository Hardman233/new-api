package model

import (
	"github.com/QuantumNous/new-api/common"
)

// 死信表状态
const (
	DeadletterPending   = "pending"
	DeadletterDone      = "done"
	DeadletterAbandoned = "abandoned"
)

// AuditUploadDeadletter 记录二进制正文上传 OSS 失败的待补传任务，
// 由 master 节点闲时扫描重传。
type AuditUploadDeadletter struct {
	Id          uint64 `json:"id" gorm:"primaryKey"`
	AuditLogId  uint64 `json:"audit_log_id" gorm:"index"`
	BodyKind    string `json:"body_kind" gorm:"type:varchar(8);default:''"` // req | resp
	LocalPath   string `json:"local_path" gorm:"type:varchar(512);default:''"`
	OssKey      string `json:"oss_key" gorm:"type:varchar(512);default:''"`
	SizeBytes   int64  `json:"size_bytes"`
	Sha256      string `json:"sha256" gorm:"type:char(64);default:''"`
	RetryCount  int    `json:"retry_count"`
	LastError   string `json:"last_error" gorm:"type:text"`
	CreatedAt   int64  `json:"created_at" gorm:"bigint"`
	NextRetryAt int64  `json:"next_retry_at" gorm:"bigint;index:idx_dl_status_next,priority:2"`
	Status      string `json:"status" gorm:"type:varchar(16);index:idx_dl_status_next,priority:1;default:'pending'"`
}

func (d *AuditUploadDeadletter) Insert() error {
	return LOG_DB.Create(d).Error
}

// GetPendingDeadletters 取到期可重试的死信记录。
func GetPendingDeadletters(limit int) ([]*AuditUploadDeadletter, error) {
	now := common.GetTimestamp()
	var rows []*AuditUploadDeadletter
	err := LOG_DB.Where("status = ? AND next_retry_at <= ?", DeadletterPending, now).
		Order("next_retry_at asc").Limit(limit).Find(&rows).Error
	return rows, err
}

// MarkDeadletterDone 标记补传成功。
func MarkDeadletterDone(id uint64) error {
	return LOG_DB.Model(&AuditUploadDeadletter{}).Where("id = ?", id).
		Update("status", DeadletterDone).Error
}

func MarkDeadlettersDoneByObject(auditLogId uint64, bodyKind string) error {
	return LOG_DB.Model(&AuditUploadDeadletter{}).
		Where("audit_log_id = ? AND body_kind = ? AND status = ?", auditLogId, bodyKind, DeadletterPending).
		Update("status", DeadletterDone).Error
}

// MarkDeadletterRetry 记录一次失败：累加重试次数、更新退避时间与错误信息。
func MarkDeadletterRetry(id uint64, retryCount int, nextRetryAt int64, lastErr string) error {
	return LOG_DB.Model(&AuditUploadDeadletter{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"retry_count":   retryCount,
			"next_retry_at": nextRetryAt,
			"last_error":    lastErr,
		}).Error
}

// MarkDeadletterAbandoned 超过最大重试次数后放弃。
func MarkDeadletterAbandoned(id uint64, lastErr string) error {
	return LOG_DB.Model(&AuditUploadDeadletter{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     DeadletterAbandoned,
			"last_error": lastErr,
		}).Error
}
