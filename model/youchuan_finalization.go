package model

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// YouchuanTaskFinalization is the durable lease for terminal-task output
// processing. It is deliberately separate from private task JSON so multiple
// application instances can coordinate OSS and callback delivery.
type YouchuanTaskFinalization struct {
	ID                  uint64 `json:"id" gorm:"primaryKey"`
	TaskID              string `json:"task_id" gorm:"type:varchar(191);not null;uniqueIndex:uk_youchuan_finalization_task"`
	CallbackRequired    bool   `json:"callback_required" gorm:"not null;default:false;index"`
	OSSCompletedAt      int64  `json:"oss_completed_at" gorm:"not null;default:0;index"`
	CallbackDeliveredAt int64  `json:"callback_delivered_at" gorm:"not null;default:0;index"`
	Completed           bool   `json:"completed" gorm:"not null;default:false;index"`
	ClaimToken          string `json:"-" gorm:"type:char(64);not null;default:'';index"`
	ClaimUntil          int64  `json:"-" gorm:"not null;default:0;index"`
	AttemptCount        int    `json:"attempt_count" gorm:"not null;default:0"`
	NextRetryAt         int64  `json:"next_retry_at" gorm:"not null;default:0;index"`
	LastError           string `json:"last_error,omitempty" gorm:"type:text"`
	CreatedAt           int64  `json:"created_at" gorm:"not null;index"`
	UpdatedAt           int64  `json:"updated_at" gorm:"not null;index"`
}

func (YouchuanTaskFinalization) TableName() string {
	return "youchuan_task_finalizations"
}

func EnsureYouchuanTaskFinalization(taskID string, callbackRequired bool) error {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil
	}
	now := time.Now().Unix()
	row := YouchuanTaskFinalization{
		TaskID:           taskID,
		CallbackRequired: callbackRequired,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	result := DB.Where("task_id = ?", taskID).First(&row)
	if errors.Is(result.Error, gorm.ErrRecordNotFound) {
		if err := DB.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "task_id"}},
			DoNothing: true,
		}).Create(&row).Error; err != nil {
			return err
		}
		if callbackRequired {
			return DB.Model(&YouchuanTaskFinalization{}).Where("task_id = ?", taskID).Updates(map[string]any{
				"callback_required": true,
				"updated_at":        now,
			}).Error
		}
		return nil
	}
	if result.Error != nil {
		return result.Error
	}
	if callbackRequired && !row.CallbackRequired {
		return DB.Model(&YouchuanTaskFinalization{}).Where("id = ?", row.ID).Updates(map[string]any{
			"callback_required": true,
			"updated_at":        now,
		}).Error
	}
	return nil
}

// ClaimYouchuanTaskFinalization returns claimed=false when another instance
// owns a live lease or the task is already complete.
func ClaimYouchuanTaskFinalization(taskID string, callbackRequired bool, lease time.Duration) (string, bool, *YouchuanTaskFinalization, error) {
	if err := EnsureYouchuanTaskFinalization(taskID, callbackRequired); err != nil {
		return "", false, nil, err
	}
	var row YouchuanTaskFinalization
	if err := DB.Where("task_id = ?", strings.TrimSpace(taskID)).First(&row).Error; err != nil {
		return "", false, nil, err
	}
	if row.Completed {
		return "", false, &row, nil
	}
	if row.NextRetryAt > time.Now().Unix() {
		return "", false, &row, nil
	}
	token, _ := common.GenerateRandomCharsKey(32)
	if token == "" {
		return "", false, &row, errors.New("failed to generate Youchuan finalization claim token")
	}
	now := time.Now().Unix()
	until := now + int64(lease/time.Second)
	if until <= now {
		until = now + 60
	}
	result := DB.Model(&YouchuanTaskFinalization{}).
		Where("id = ? AND completed = ? AND next_retry_at <= ? AND (claim_token = '' OR claim_until < ?)", row.ID, false, now, now).
		Updates(map[string]any{
			"claim_token": token,
			"claim_until": until,
			"updated_at":  now,
		})
	if result.Error != nil {
		return "", false, &row, result.Error
	}
	if result.RowsAffected == 0 {
		return "", false, &row, nil
	}
	row.ClaimToken = token
	row.ClaimUntil = until
	return token, true, &row, nil
}

func MarkYouchuanTaskOSSCompleted(taskID, claimToken string, completedAt int64) error {
	result := DB.Model(&YouchuanTaskFinalization{}).
		Where("task_id = ? AND claim_token = ?", strings.TrimSpace(taskID), claimToken).
		Updates(map[string]any{
			"oss_completed_at": completedAt,
			"updated_at":       time.Now().Unix(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("Youchuan finalization lease was lost before OSS completion")
	}
	return nil
}

func RenewYouchuanTaskFinalization(taskID, claimToken string, lease time.Duration) error {
	now := time.Now().Unix()
	until := now + int64(lease/time.Second)
	if until <= now {
		until = now + 60
	}
	result := DB.Model(&YouchuanTaskFinalization{}).
		Where("task_id = ? AND claim_token = ? AND completed = ?", strings.TrimSpace(taskID), claimToken, false).
		Updates(map[string]any{
			"claim_until": until,
			"updated_at":  now,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("Youchuan finalization lease was lost during renewal")
	}
	return nil
}

func CompleteYouchuanTaskFinalization(taskID, claimToken string, callbackDeliveredAt int64) error {
	now := time.Now().Unix()
	updates := map[string]any{
		"completed":             true,
		"claim_token":           "",
		"claim_until":           0,
		"callback_delivered_at": callbackDeliveredAt,
		"next_retry_at":         0,
		"last_error":            "",
		"updated_at":            now,
	}
	result := DB.Model(&YouchuanTaskFinalization{}).
		Where("task_id = ? AND claim_token = ?", strings.TrimSpace(taskID), claimToken).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return errors.New("Youchuan finalization lease was lost before completion")
	}
	return nil
}

func ReleaseYouchuanTaskFinalization(taskID, claimToken, failure string) error {
	var row YouchuanTaskFinalization
	if err := DB.Where("task_id = ? AND claim_token = ?", strings.TrimSpace(taskID), claimToken).First(&row).Error; err != nil {
		return err
	}
	attempt := row.AttemptCount + 1
	backoff := int64(15)
	for i := 1; i < attempt && backoff < 900; i++ {
		backoff *= 2
	}
	if backoff > 900 {
		backoff = 900
	}
	now := time.Now().Unix()
	return DB.Model(&YouchuanTaskFinalization{}).
		Where("id = ? AND claim_token = ?", row.ID, claimToken).
		Updates(map[string]any{
			"claim_token":   "",
			"claim_until":   0,
			"attempt_count": attempt,
			"next_retry_at": now + backoff,
			"last_error":    truncateYouchuanFinalizationError(failure),
			"updated_at":    now,
		}).Error
}

func MarkYouchuanCallbackDelivered(taskID string, deliveredAt int64) error {
	var task Task
	if err := DB.Where("task_id = ?", strings.TrimSpace(taskID)).First(&task).Error; err != nil {
		return err
	}
	if task.PrivateData.Youchuan == nil {
		task.PrivateData.Youchuan = &YouchuanTaskPrivateData{}
	}
	if task.PrivateData.Youchuan.CallbackDeliveredAt < deliveredAt {
		task.PrivateData.Youchuan.CallbackDeliveredAt = deliveredAt
	}
	return UpdateTaskPrivateData(&task)
}

func GetPendingYouchuanTaskFinalizations(limit int) ([]*Task, error) {
	if limit <= 0 {
		limit = 100
	}
	now := time.Now().Unix()
	var rows []YouchuanTaskFinalization
	err := DB.Table("youchuan_task_finalizations AS f").
		Select("f.*").
		Joins("JOIN tasks AS t ON t.task_id = f.task_id").
		Where("t.platform = ? AND t.status IN ? AND f.completed = ? AND f.next_retry_at <= ? AND (f.claim_token = '' OR f.claim_until < ?)",
			constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeAliYouchuan)), []TaskStatus{TaskStatusSuccess, TaskStatusFailure}, false, now, now).
		Order("f.next_retry_at ASC, f.id ASC").
		Limit(limit).
		Find(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]*Task, 0, len(rows))
	for _, row := range rows {
		var task Task
		if err := DB.Where("task_id = ?", row.TaskID).First(&task).Error; err != nil {
			continue
		}
		result = append(result, &task)
	}
	return result, nil
}

func truncateYouchuanFinalizationError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 2048 {
		return value[:2048]
	}
	return value
}
