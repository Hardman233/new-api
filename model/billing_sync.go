package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BillingSyncStatusPending = "pending"
	BillingSyncStatusSynced  = "synced"
	BillingSyncStatusFailed  = "failed"
)

type BillingSyncOutbox struct {
	Id            uint64 `json:"id" gorm:"primaryKey"`
	BillingID     string `json:"billing_id" gorm:"type:varchar(192);not null;uniqueIndex:uk_billing_sync_billing_id"`
	Status        string `json:"status" gorm:"type:varchar(32);not null;default:'pending';index:idx_billing_sync_status_next,priority:1"`
	RetryCount    int    `json:"retry_count" gorm:"not null;default:0"`
	NextRetryAt   int64  `json:"next_retry_at" gorm:"not null;default:0;index:idx_billing_sync_status_next,priority:2"`
	LastError     string `json:"last_error" gorm:"type:text"`
	LastAttemptAt int64  `json:"last_attempt_at" gorm:"not null;default:0"`
	SyncedAt      int64  `json:"synced_at" gorm:"not null;default:0"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime;index"`
	UpdatedAt     int64  `json:"updated_at" gorm:"autoUpdateTime"`
}

func (BillingSyncOutbox) TableName() string {
	return "billing_sync_outbox"
}

func billingSyncEnabledForLocalNode() bool {
	return DB != nil &&
		common.GetEnvOrDefaultBool("BILLING_SYNC_ENABLED", false) &&
		common.GetServerRegion() != common.ServerRegionDomestic
}

func MaybeEnqueueBillingSync(billingID string, status string) {
	if !billingSyncEnabledForLocalNode() || !IsBillingLedgerFinalStatus(status) {
		return
	}
	if err := EnqueueBillingSync(billingID); err != nil {
		common.SysLog("enqueue billing sync failed: " + err.Error())
	}
}

func EnqueueBillingSync(billingID string) error {
	billingID = strings.TrimSpace(billingID)
	if billingID == "" || !billingSyncEnabledForLocalNode() {
		return nil
	}
	now := common.GetTimestamp()
	outbox := BillingSyncOutbox{
		BillingID:   billingID,
		Status:      BillingSyncStatusPending,
		CreatedAt:   now,
		UpdatedAt:   now,
		NextRetryAt: 0,
	}
	return DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "billing_id"}},
		DoNothing: true,
	}).Create(&outbox).Error
}

func SeedBillingSyncOutboxFromLedger(limit int) error {
	if !billingSyncEnabledForLocalNode() {
		return nil
	}
	if limit <= 0 {
		limit = 100
	}
	var rows []struct {
		BillingID string `gorm:"column:billing_id"`
	}
	err := DB.Table("billing_ledger AS l").
		Select("l.billing_id").
		Joins("LEFT JOIN billing_sync_outbox AS o ON o.billing_id = l.billing_id").
		Where("l.server_region = ? AND l.status IN ? AND o.id IS NULL",
			common.GetServerRegion(),
			[]string{BillingLedgerStatusSettled, BillingLedgerStatusRefunded, BillingLedgerStatusFailed},
		).
		Order("l.id ASC").
		Limit(limit).
		Scan(&rows).Error
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := EnqueueBillingSync(row.BillingID); err != nil {
			return err
		}
	}
	return nil
}

func ListReadyBillingSyncOutboxes(limit int) ([]BillingSyncOutbox, error) {
	if limit <= 0 {
		limit = 100
	}
	var rows []BillingSyncOutbox
	err := DB.Where("status IN ? AND next_retry_at <= ?",
		[]string{BillingSyncStatusPending, BillingSyncStatusFailed},
		common.GetTimestamp(),
	).Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func MarkBillingSyncSynced(id uint64) error {
	now := common.GetTimestamp()
	return DB.Model(&BillingSyncOutbox{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":          BillingSyncStatusSynced,
			"last_error":      "",
			"last_attempt_at": now,
			"synced_at":       now,
			"updated_at":      now,
		}).Error
}

func MarkBillingSyncFailed(id uint64, errMsg string, nextRetryAt int64) error {
	now := common.GetTimestamp()
	return DB.Model(&BillingSyncOutbox{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":          BillingSyncStatusFailed,
			"retry_count":     gorm.Expr("retry_count + 1"),
			"last_error":      errMsg,
			"last_attempt_at": now,
			"next_retry_at":   nextRetryAt,
			"updated_at":      now,
		}).Error
}
