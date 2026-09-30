package service

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/oss"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	auditDeadletterBatch = 200
	auditOssObjectBatch  = 200
	auditCleanupBatch    = 1000
)

var (
	auditReconcileOnce    sync.Once
	auditReconcileRunning atomic.Bool
)

// StartAuditReconcileTask 仅 master 节点启动：闲时补传死信 + 清理过期/本地副本。
func StartAuditReconcileTask() {
	if !common.AuditLogEnabled {
		return
	}
	auditReconcileOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		interval := time.Duration(common.AuditReconcileIntervalSec) * time.Second
		if interval <= 0 {
			interval = 10 * time.Minute
		}
		gopool.Go(func() {
			logger.LogInfo(context.Background(), fmt.Sprintf("audit reconcile task started: interval=%s", interval))
			ticker := time.NewTicker(interval)
			defer ticker.Stop()

			runAuditReconcileOnce()
			for range ticker.C {
				runAuditReconcileOnce()
			}
		})
	})
}

func runAuditReconcileOnce() {
	if !auditReconcileRunning.CompareAndSwap(false, true) {
		return
	}
	defer auditReconcileRunning.Store(false)
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("panic in audit reconcile: %v", r))
		}
	}()

	ctx := context.Background()
	retryDeadletters(ctx)
	reconcileAuditOssObjects(ctx)
	cleanupExpiredAuditLogs(ctx)
	cleanupSpool(ctx)
}

// retryDeadletters 扫描到期死信，逐条重传。
func retryDeadletters(ctx context.Context) {
	rows, err := model.GetPendingDeadletters(auditDeadletterBatch)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit get deadletters failed: %v", err))
		return
	}
	if len(rows) == 0 {
		return
	}
	uploader, err := oss.GetUploader()
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit reconcile: oss unavailable: %v", err))
		return
	}

	for _, dl := range rows {
		data, rerr := common.ReadAuditSpoolFile(dl.LocalPath)
		if rerr != nil {
			// 本地文件已不在，无法补传，直接放弃。
			_ = model.MarkDeadletterAbandoned(dl.Id, "local file missing: "+truncateErr(rerr))
			_ = model.UpdateAuditBodyStorage(dl.AuditLogId, model.AuditBodyAbandoned)
			_ = model.MarkAuditOssObjectAbandoned(dl.AuditLogId, dl.BodyKind, "local file missing: "+truncateErr(rerr))
			continue
		}
		if perr := uploader.Put(ctx, dl.OssKey, data, "application/octet-stream"); perr != nil {
			handleRetryFailure(ctx, dl, perr)
			continue
		}
		// 成功：审计行转 local+oss，死信销账。
		if uerr := model.UpdateAuditOssLocation(dl.AuditLogId, dl.BodyKind, dl.OssKey, model.AuditBodyLocalOSS); uerr != nil {
			logger.LogWarn(ctx, fmt.Sprintf("audit reconcile update location failed: %v", uerr))
		}
		_ = model.MarkAuditOssObjectUploaded(dl.AuditLogId, dl.BodyKind, false)
		_ = model.MarkDeadletterDone(dl.Id)
	}
}

func handleRetryFailure(ctx context.Context, dl *model.AuditUploadDeadletter, cause error) {
	next := dl.RetryCount + 1
	if next >= common.AuditUploadMaxRetry {
		_ = model.MarkDeadletterAbandoned(dl.Id, truncateErr(cause))
		_ = model.UpdateAuditBodyStorage(dl.AuditLogId, model.AuditBodyAbandoned)
		_ = model.MarkAuditOssObjectAbandoned(dl.AuditLogId, dl.BodyKind, truncateErr(cause))
		logger.LogWarn(ctx, fmt.Sprintf("audit upload abandoned after %d retries: audit_log_id=%d", next, dl.AuditLogId))
		return
	}
	_ = model.MarkDeadletterRetry(dl.Id, next, auditBackoff(next), truncateErr(cause))
	_ = model.MarkAuditOssObjectFailed(dl.AuditLogId, dl.BodyKind, truncateErr(cause))
}

func reconcileAuditOssObjects(ctx context.Context) {
	rows, err := model.GetAuditOssObjectsForReconcile(auditOssObjectBatch)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit get oss objects failed: %v", err))
		return
	}
	if len(rows) == 0 {
		return
	}
	uploader, err := oss.GetUploader()
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit oss object reconcile: oss unavailable: %v", err))
		return
	}

	for _, row := range rows {
		reconcileAuditOssObject(ctx, uploader, row)
	}
}

func reconcileAuditOssObject(ctx context.Context, uploader oss.Uploader, row *model.AuditOssObject) {
	if row == nil || row.OssKey == "" {
		return
	}
	switch row.Status {
	case model.AuditOssStatusUploaded, model.AuditOssStatusRepaired:
		info, err := uploader.Head(ctx, row.OssKey)
		if err == nil && (row.SizeBytes <= 0 || info == nil || info.SizeBytes == row.SizeBytes) {
			_ = model.MarkAuditOssObjectChecked(row.AuditLogId, row.BodyKind)
			return
		}
		reason := "oss object missing"
		if err != nil {
			reason = "oss head failed: " + truncateErr(err)
		} else if info != nil {
			reason = fmt.Sprintf("oss size mismatch: want=%d got=%d", row.SizeBytes, info.SizeBytes)
		}
		_ = model.MarkAuditOssObjectMissing(row.AuditLogId, row.BodyKind, reason)
		repairAuditOssObject(ctx, uploader, row, true, reason)
	case model.AuditOssStatusPending, model.AuditOssStatusFailed, model.AuditOssStatusMissing:
		repairAuditOssObject(ctx, uploader, row, row.Status == model.AuditOssStatusMissing, row.LastError)
	}
}

func repairAuditOssObject(ctx context.Context, uploader oss.Uploader, row *model.AuditOssObject, repaired bool, reason string) {
	data, err := common.ReadAuditSpoolFile(row.LocalPath)
	if err != nil {
		lastErr := "local file missing"
		if reason != "" {
			lastErr = reason + "; " + lastErr
		}
		lastErr += ": " + truncateErr(err)
		_ = model.MarkAuditOssObjectAbandoned(row.AuditLogId, row.BodyKind, lastErr)
		_ = model.UpdateAuditBodyStorage(row.AuditLogId, model.AuditBodyAbandoned)
		return
	}
	if err = uploader.Put(ctx, row.OssKey, data, "application/octet-stream"); err != nil {
		_ = model.MarkAuditOssObjectFailed(row.AuditLogId, row.BodyKind, truncateErr(err))
		return
	}
	if err = model.UpdateAuditOssLocation(row.AuditLogId, row.BodyKind, row.OssKey, model.AuditBodyLocalOSS); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit oss object repair update location failed: %v", err))
	}
	_ = model.MarkAuditOssObjectUploaded(row.AuditLogId, row.BodyKind, repaired)
	_ = model.MarkDeadlettersDoneByObject(row.AuditLogId, row.BodyKind)
}

// cleanupExpiredAuditLogs 分批删除过期审计行。
func cleanupExpiredAuditLogs(ctx context.Context) {
	for {
		n, err := model.CleanupExpiredAuditLogs(auditCleanupBatch)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("audit cleanup expired failed: %v", err))
			return
		}
		if n < int64(auditCleanupBatch) {
			break
		}
	}
}

// cleanupSpool 当 spool 超过容量上限时，按修改时间删除最旧文件。
// 注意：只在超限时清理；正常情况下本地副本作为快路径保留。
func cleanupSpool(ctx context.Context) {
	maxBytes := int64(common.AuditLogSpoolMaxMB) << 20
	if maxBytes <= 0 {
		return
	}
	_, total, err := common.GetAuditSpoolUsage()
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit spool usage failed: %v", err))
		return
	}
	if total <= maxBytes {
		return
	}
	// 超限：清理超过留存天数的文件。
	maxAge := time.Duration(common.AuditLogRetentionDays) * 24 * time.Hour
	if maxAge <= 0 {
		maxAge = 7 * 24 * time.Hour
	}
	if cerr := common.CleanupOldAuditSpoolFiles(maxAge); cerr != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit spool cleanup failed: %v", cerr))
	}
}
