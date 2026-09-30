package service

import (
	"context"
	"fmt"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/oss"

	"github.com/bytedance/gopkg/util/gopool"
)

// AuditBinaryBody 待上传 OSS 的二进制正文（已压缩加密落盘）。
type AuditBinaryBody struct {
	Kind        string // req | resp
	LocalPath   string // 落盘路径
	OssKey      string // 目标 OSS key
	SizeBytes   int64
	Sha256      string
	ContentType string
}

// AuditLogPayload 一条审计任务：元信息行 + 0~2 个待上传二进制。
type AuditLogPayload struct {
	Log      model.ApiAuditLog
	Binaries []AuditBinaryBody
}

var (
	auditQueue     chan AuditLogPayload
	auditQueueOnce sync.Once
)

// InitAuditQueue 初始化异步队列（幂等）。
func InitAuditQueue() {
	auditQueueOnce.Do(func() {
		size := common.AuditLogQueueSize
		if size <= 0 {
			size = 1000
		}
		auditQueue = make(chan AuditLogPayload, size)
	})
}

// EnqueueAuditLog 非阻塞入队；队列满则丢弃并告警，绝不阻塞 API。
func EnqueueAuditLog(payload AuditLogPayload) {
	if !common.AuditLogEnabled || auditQueue == nil {
		return
	}
	select {
	case auditQueue <- payload:
	default:
		logger.LogWarn(context.Background(), "audit log queue full, drop audit log")
	}
}

// StartAuditWorkers 启动 N 个消费 goroutine。不限 master，每个节点处理本机入队的任务。
func StartAuditWorkers() {
	if !common.AuditLogEnabled {
		return
	}
	InitAuditQueue()
	workers := common.AuditUploadWorkers
	if workers <= 0 {
		workers = 2
	}
	for i := 0; i < workers; i++ {
		gopool.Go(func() {
			for payload := range auditQueue {
				processAuditPayload(payload)
			}
		})
	}
	logger.LogInfo(context.Background(), fmt.Sprintf("audit workers started: %d", workers))
}

// processAuditPayload 落库元信息行，再逐个上传二进制正文。
func processAuditPayload(payload AuditLogPayload) {
	defer func() {
		if r := recover(); r != nil {
			common.SysError(fmt.Sprintf("panic in audit worker: %v", r))
		}
	}()

	ctx := context.Background()
	logRow := payload.Log
	if err := logRow.Insert(); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit log insert failed: %v", err))
		return
	}

	if len(payload.Binaries) == 0 {
		return
	}
	for _, b := range payload.Binaries {
		if err := model.EnsureAuditOssObjectPending(auditOssObjectParams(logRow, b)); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("audit oss object pending insert failed: %v", err))
		}
	}

	uploader, err := oss.GetUploader()
	if err != nil {
		// OSS 不可用：所有二进制进死信表，等 reconcile 重试。
		for _, b := range payload.Binaries {
			_ = model.MarkAuditOssObjectFailed(logRow.Id, b.Kind, truncateErr(err))
			enqueueDeadletter(ctx, logRow.Id, b, err)
		}
		return
	}

	for _, b := range payload.Binaries {
		uploadOneBinary(ctx, uploader, logRow.Id, b)
	}
}

// uploadOneBinary 读盘 -> 上传 -> 成功更新行；失败写死信表。
func uploadOneBinary(ctx context.Context, uploader oss.Uploader, auditLogId uint64, b AuditBinaryBody) {
	data, err := common.ReadAuditSpoolFile(b.LocalPath)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit read spool failed: %v", err))
		_ = model.MarkAuditOssObjectFailed(auditLogId, b.Kind, truncateErr(err))
		enqueueDeadletter(ctx, auditLogId, b, err)
		return
	}
	if err = uploader.Put(ctx, b.OssKey, data, b.ContentType); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit oss put failed: %v", err))
		_ = model.MarkAuditOssObjectFailed(auditLogId, b.Kind, truncateErr(err))
		enqueueDeadletter(ctx, auditLogId, b, err)
		return
	}
	// 上传成功：行转 local+oss，本地文件保留作快路径。
	if err = model.UpdateAuditOssLocation(auditLogId, b.Kind, b.OssKey, model.AuditBodyLocalOSS); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit update oss location failed: %v", err))
	}
	if err = model.MarkAuditOssObjectUploaded(auditLogId, b.Kind, false); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit mark oss object uploaded failed: %v", err))
	}
	_ = model.MarkDeadlettersDoneByObject(auditLogId, b.Kind)
}

// enqueueDeadletter 写一条待补传死信记录。
func enqueueDeadletter(ctx context.Context, auditLogId uint64, b AuditBinaryBody, cause error) {
	dl := model.AuditUploadDeadletter{
		AuditLogId:  auditLogId,
		BodyKind:    b.Kind,
		LocalPath:   b.LocalPath,
		OssKey:      b.OssKey,
		SizeBytes:   b.SizeBytes,
		Sha256:      b.Sha256,
		RetryCount:  0,
		LastError:   truncateErr(cause),
		CreatedAt:   common.GetTimestamp(),
		NextRetryAt: common.GetTimestamp() + 60, // 1 分钟后首次重试
		Status:      model.DeadletterPending,
	}
	if err := dl.Insert(); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("audit deadletter insert failed: %v", err))
	}
}

func auditOssObjectParams(logRow model.ApiAuditLog, b AuditBinaryBody) model.AuditOssObjectParams {
	return model.AuditOssObjectParams{
		AuditLogId: logRow.Id,
		RequestId:  logRow.RequestId,
		BodyKind:   b.Kind,
		LocalPath:  b.LocalPath,
		OssKey:     b.OssKey,
		SizeBytes:  b.SizeBytes,
		Sha256:     b.Sha256,
	}
}

func truncateErr(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if len(s) > 1000 {
		return s[:1000]
	}
	return s
}

// auditBackoff 指数退避：base 60s，按重试次数翻倍，封顶 1 小时。
func auditBackoff(retryCount int) int64 {
	base := int64(60)
	d := base << retryCount
	if d > 3600 {
		d = 3600
	}
	return common.GetTimestamp() + d
}
