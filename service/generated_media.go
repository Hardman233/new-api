package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service/oss"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	defaultGeneratedMediaCaptureMaxMB  = 64
	defaultGeneratedMediaDownloadMaxMB = 1024
	defaultGeneratedMediaQueueSize     = 128
	defaultGeneratedMediaPollSec       = 30
	defaultGeneratedMediaBatchSize     = 100
)

type GeneratedMediaCandidate struct {
	MediaKind   string
	ContentType string
	Data        []byte
	Sha256      string
	FieldPath   string
}

type generatedMediaCapturePayload struct {
	Log          model.ApiAuditLog
	ResponseBody []byte
}

type limitedBufferWriter struct {
	buf bytes.Buffer
	max int
}

func (w *limitedBufferWriter) Write(p []byte) (int, error) {
	if w.max > 0 && w.buf.Len() < w.max {
		remain := w.max - w.buf.Len()
		if len(p) > remain {
			w.buf.Write(p[:remain])
		} else {
			w.buf.Write(p)
		}
	}
	return len(p), nil
}

var (
	generatedMediaOnce         sync.Once
	generatedMediaCaptureQueue chan generatedMediaCapturePayload
	generatedMediaWakeCh       chan struct{}
)

func GeneratedMediaEnabled() bool {
	if !common.GetEnvOrDefaultBool("GENERATED_MEDIA_OSS_ENABLED", true) {
		return false
	}
	return common.AuditLogOSSConfigured()
}

func GeneratedMediaCaptureMaxBytes() int {
	mb := common.GetEnvOrDefault("GENERATED_MEDIA_CAPTURE_MAX_MB", defaultGeneratedMediaCaptureMaxMB)
	if mb <= 0 {
		return 0
	}
	return mb * 1024 * 1024
}

func StartGeneratedMediaWorkers() {
	if !GeneratedMediaEnabled() {
		common.SysLog("generated media OSS retention disabled")
		return
	}
	generatedMediaOnce.Do(func() {
		queueSize := common.GetEnvOrDefault("GENERATED_MEDIA_QUEUE_SIZE", defaultGeneratedMediaQueueSize)
		if queueSize <= 0 {
			queueSize = defaultGeneratedMediaQueueSize
		}
		generatedMediaCaptureQueue = make(chan generatedMediaCapturePayload, queueSize)
		generatedMediaWakeCh = make(chan struct{}, 1)

		captureWorkers := common.GetEnvOrDefault("GENERATED_MEDIA_CAPTURE_WORKERS", 1)
		if captureWorkers <= 0 {
			captureWorkers = 1
		}
		for i := 0; i < captureWorkers; i++ {
			gopool.Go(generatedMediaCaptureLoop)
		}

		workers := common.GetEnvOrDefault("GENERATED_MEDIA_WORKERS", 2)
		if workers <= 0 {
			workers = 2
		}
		for i := 0; i < workers; i++ {
			gopool.Go(generatedMediaWorkerLoop)
		}

		logger.LogInfo(context.Background(), fmt.Sprintf("generated media OSS workers started: capture=%d upload=%d", captureWorkers, workers))
		wakeGeneratedMediaWorkers()
	})
}

func EnqueueGeneratedMediaFromAuditResponse(logRow model.ApiAuditLog, responseBody []byte) {
	if !GeneratedMediaEnabled() || len(responseBody) == 0 || generatedMediaCaptureQueue == nil {
		return
	}
	bodyCopy := append([]byte(nil), responseBody...)
	payload := generatedMediaCapturePayload{Log: logRow, ResponseBody: bodyCopy}
	select {
	case generatedMediaCaptureQueue <- payload:
	default:
		logger.LogWarn(context.Background(), "generated media capture queue full, drop response media")
	}
}

func EnqueueGeneratedMediaForTask(ctx context.Context, task *model.Task, taskResult *relaycommon.TaskInfo) {
	if !GeneratedMediaEnabled() || task == nil {
		return
	}
	outputs := generatedMediaTaskOutputs(task, taskResult)
	if len(outputs) == 0 {
		return
	}
	username, _ := model.GetUsernameById(task.UserId, false)
	tokenName := ""
	if task.PrivateData.TokenId > 0 {
		if token, err := model.GetTokenById(task.PrivateData.TokenId); err == nil && token != nil {
			tokenName = token.Name
		}
	}
	channelName := ""
	if ch, err := model.CacheGetChannel(task.ChannelId); err == nil && ch != nil {
		channelName = ch.Name
	}
	modelName := task.Properties.OriginModelName
	if modelName == "" && task.PrivateData.BillingContext != nil {
		modelName = task.PrivateData.BillingContext.OriginModelName
	}
	if modelName == "" {
		modelName = task.Properties.UpstreamModelName
	}
	if task.Platform != constant.TaskPlatform(fmt.Sprint(constant.ChannelTypeAliYouchuan)) {
		resultURL := strings.TrimSpace(outputs[0].RemoteURL)
		if resultURL == "" {
			return
		}
		other := common.MapToJsonStr(map[string]any{
			"source":            "task_result_url",
			"create_request_id": task.Properties.CreateRequestID,
		})
		row, created, err := model.EnsureGeneratedMediaObjectPending(model.GeneratedMediaObjectParams{
			SourceType:   model.GeneratedMediaSourceTaskURL,
			MediaKind:    model.GeneratedMediaKindVideo,
			SourceHash:   common.Sha256Hex([]byte(resultURL)),
			SourceURL:    resultURL,
			RequestId:    task.Properties.CreateRequestID,
			BillingID:    task.TaskID,
			TaskID:       task.TaskID,
			UserID:       task.UserId,
			Username:     username,
			TokenID:      task.PrivateData.TokenId,
			TokenName:    tokenName,
			UserGroup:    task.Group,
			ChannelID:    task.ChannelId,
			ChannelName:  channelName,
			ModelName:    modelName,
			ServerRegion: common.GetServerRegion(),
			Other:        other,
		})
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("generated media task enqueue failed task=%s: %v", task.TaskID, err))
			return
		}
		if created || row != nil {
			wakeGeneratedMediaWorkers()
		}
		return
	}
	createdAny := false
	for _, output := range outputs {
		resultURL := strings.TrimSpace(output.RemoteURL)
		if resultURL == "" {
			continue
		}
		mediaKind := strings.TrimSpace(output.MediaKind)
		if mediaKind == "" {
			mediaKind = model.GeneratedMediaKindVideo
		}
		other := common.MapToJsonStr(map[string]any{
			"source":            "task_result_url",
			"create_request_id": task.Properties.CreateRequestID,
			"output_index":      output.Index,
			"audit":             output.Audit,
		})
		row, created, err := model.EnsureGeneratedMediaObjectPending(model.GeneratedMediaObjectParams{
			SourceType:   model.GeneratedMediaSourceTaskURL,
			MediaKind:    mediaKind,
			SourceHash:   common.Sha256Hex([]byte(resultURL)),
			SourceURL:    resultURL,
			RequestId:    task.Properties.CreateRequestID,
			BillingID:    task.TaskID,
			TaskID:       task.TaskID,
			UserID:       task.UserId,
			Username:     username,
			TokenID:      task.PrivateData.TokenId,
			TokenName:    tokenName,
			UserGroup:    task.Group,
			ChannelID:    task.ChannelId,
			ChannelName:  channelName,
			ModelName:    modelName,
			ServerRegion: common.GetServerRegion(),
			Other:        other,
		})
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("generated media task enqueue failed task=%s output=%d: %v", task.TaskID, output.Index, err))
			continue
		}
		createdAny = createdAny || created || row != nil
	}
	if createdAny {
		wakeGeneratedMediaWorkers()
	}
}

func generatedMediaTaskOutputs(task *model.Task, taskResult *relaycommon.TaskInfo) []relaycommon.TaskOutput {
	if task.Platform != constant.TaskPlatform(fmt.Sprint(constant.ChannelTypeAliYouchuan)) {
		resultURL := chooseTaskGeneratedMediaURL(task, taskResult)
		if resultURL == "" {
			return nil
		}
		return []relaycommon.TaskOutput{{Index: 0, RemoteURL: resultURL, MediaKind: model.GeneratedMediaKindVideo}}
	}
	if taskResult != nil && len(taskResult.Outputs) > 0 {
		persistYouchuanTaskOutputs(task, taskResult.Outputs)
		return append([]relaycommon.TaskOutput(nil), taskResult.Outputs...)
	}
	if task.PrivateData.Youchuan != nil && len(task.PrivateData.Youchuan.Outputs) > 0 {
		outputs := make([]relaycommon.TaskOutput, 0, len(task.PrivateData.Youchuan.Outputs))
		for _, output := range task.PrivateData.Youchuan.Outputs {
			outputs = append(outputs, relaycommon.TaskOutput{
				Index: output.Index, RemoteURL: output.RemoteURL, URL: output.URL,
				OSSKey: output.OSSKey, SizeBytes: output.SizeBytes, MediaKind: output.MediaKind,
				Audit: output.Audit, UploadStatus: output.UploadStatus, UploadError: output.UploadError,
			})
		}
		return outputs
	}
	return nil
}

func persistYouchuanTaskOutputs(task *model.Task, outputs []relaycommon.TaskOutput) {
	if task.PrivateData.Youchuan == nil {
		task.PrivateData.Youchuan = &model.YouchuanTaskPrivateData{}
	}
	persisted := make([]model.YouchuanTaskOutput, 0, len(outputs))
	for _, output := range outputs {
		persisted = append(persisted, model.YouchuanTaskOutput{
			Index: output.Index, RemoteURL: output.RemoteURL, URL: output.URL,
			OSSKey: output.OSSKey, SizeBytes: output.SizeBytes, MediaKind: output.MediaKind,
			Audit: output.Audit, UploadStatus: output.UploadStatus, UploadError: output.UploadError,
		})
	}
	task.PrivateData.Youchuan.Outputs = persisted
	if err := model.UpdateTaskPrivateData(task); err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("persist Youchuan outputs failed task=%s: %v", task.TaskID, err))
	}
}

func generatedMediaCaptureLoop() {
	for payload := range generatedMediaCaptureQueue {
		func() {
			defer func() {
				if r := recover(); r != nil {
					common.SysError(fmt.Sprintf("panic in generated media capture worker: %v", r))
				}
			}()
			processGeneratedMediaCapture(payload)
		}()
	}
}

func generatedMediaWorkerLoop() {
	interval := time.Duration(common.GetEnvOrDefault("GENERATED_MEDIA_POLL_INTERVAL_SEC", defaultGeneratedMediaPollSec)) * time.Second
	if interval <= 0 {
		interval = defaultGeneratedMediaPollSec * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		runGeneratedMediaOnce()
		select {
		case <-generatedMediaWakeCh:
		case <-ticker.C:
		}
	}
}

func runGeneratedMediaOnce() {
	if !GeneratedMediaEnabled() {
		return
	}
	ctx := context.Background()
	limit := common.GetEnvOrDefault("GENERATED_MEDIA_BATCH_SIZE", defaultGeneratedMediaBatchSize)
	rows, err := model.GetReadyGeneratedMediaObjects(limit)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("generated media query failed: %v", err))
		return
	}
	for _, row := range rows {
		processGeneratedMediaRow(ctx, row)
	}
}

func processGeneratedMediaCapture(payload generatedMediaCapturePayload) {
	candidates := ExtractGeneratedMediaBase64(payload.ResponseBody)
	if len(candidates) == 0 {
		return
	}
	for _, c := range candidates {
		localPath, err := writeGeneratedMediaSpool(c.MediaKind, c.ContentType, c.Sha256, c.Data)
		if err != nil {
			logger.LogWarn(context.Background(), fmt.Sprintf("generated media spool write failed request=%s: %v", payload.Log.RequestId, err))
			continue
		}
		key := buildGeneratedMediaOSSKey(c.MediaKind, c.ContentType, c.Sha256)
		row, created, err := model.EnsureGeneratedMediaObjectPending(model.GeneratedMediaObjectParams{
			SourceType:   model.GeneratedMediaSourceResponseBase64,
			MediaKind:    c.MediaKind,
			SourceHash:   c.Sha256,
			LocalPath:    localPath,
			RequestId:    payload.Log.RequestId,
			BillingID:    billingIDFromAuditLog(payload.Log),
			UserID:       payload.Log.UserId,
			Username:     payload.Log.Username,
			TokenID:      payload.Log.TokenId,
			TokenName:    payload.Log.TokenName,
			UserGroup:    payload.Log.UserGroup,
			ChannelID:    payload.Log.ChannelId,
			ChannelName:  payload.Log.ChannelName,
			ModelName:    payload.Log.ModelName,
			ServerRegion: common.GetServerRegion(),
			ContentType:  c.ContentType,
			SizeBytes:    int64(len(c.Data)),
			Sha256:       c.Sha256,
			OssKey:       key,
			Other: common.MapToJsonStr(map[string]any{
				"source":   "response_base64",
				"path":     payload.Log.Path,
				"field":    c.FieldPath,
				"status":   payload.Log.StatusCode,
				"mime":     c.ContentType,
				"body_sha": payload.Log.ResponseBodySha256,
			}),
		})
		if err != nil {
			_ = os.Remove(localPath)
			logger.LogWarn(context.Background(), fmt.Sprintf("generated media pending insert failed request=%s: %v", payload.Log.RequestId, err))
			continue
		}
		if !created {
			if row != nil && row.Status != model.GeneratedMediaStatusUploaded && (strings.TrimSpace(row.LocalPath) == "" || !fileExists(row.LocalPath)) {
				_ = model.UpdateGeneratedMediaLocal(row.Id, localPath, c.ContentType, c.Sha256, int64(len(c.Data)))
			} else {
				_ = os.Remove(localPath)
			}
		}
		wakeGeneratedMediaWorkers()
	}
}

func processGeneratedMediaRow(ctx context.Context, row *model.GeneratedMediaObject) {
	if row == nil {
		return
	}
	claimed, err := model.ClaimGeneratedMediaObject(row.Id)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("generated media claim failed id=%d: %v", row.Id, err))
		return
	}
	if !claimed {
		return
	}
	if err = uploadGeneratedMediaRow(ctx, row); err != nil {
		retry := row.RetryCount + 1
		if retry >= generatedMediaMaxRetry() {
			_ = model.MarkGeneratedMediaAbandoned(row.Id, truncateErr(err))
			logger.LogWarn(ctx, fmt.Sprintf("generated media abandoned id=%d: %v", row.Id, err))
			return
		}
		_ = model.MarkGeneratedMediaFailed(row.Id, truncateErr(err), retry, generatedMediaBackoff(retry))
		logger.LogWarn(ctx, fmt.Sprintf("generated media failed id=%d retry=%d: %v", row.Id, retry, err))
	}
}

func uploadGeneratedMediaRow(ctx context.Context, row *model.GeneratedMediaObject) error {
	localPath := strings.TrimSpace(row.LocalPath)
	contentType := strings.TrimSpace(row.ContentType)
	shaHex := strings.TrimSpace(row.Sha256)
	sizeBytes := row.SizeBytes

	if (row.SourceType == model.GeneratedMediaSourceTaskURL || row.SourceType == model.GeneratedMediaSourceResponseURL) && localPath == "" {
		downloaded, ct, sha, size, err := downloadGeneratedMediaToSpool(row.SourceURL, row.MediaKind)
		if err != nil {
			return err
		}
		localPath = downloaded
		contentType = ct
		shaHex = sha
		sizeBytes = size
		if err = model.UpdateGeneratedMediaLocal(row.Id, localPath, contentType, shaHex, sizeBytes); err != nil {
			return err
		}
	}
	if localPath == "" {
		return errors.New("generated media local path is empty")
	}
	if !fileExists(localPath) {
		if (row.SourceType == model.GeneratedMediaSourceTaskURL || row.SourceType == model.GeneratedMediaSourceResponseURL) && isHTTPURL(row.SourceURL) {
			downloaded, ct, sha, size, err := downloadGeneratedMediaToSpool(row.SourceURL, row.MediaKind)
			if err != nil {
				return err
			}
			localPath = downloaded
			contentType = ct
			shaHex = sha
			sizeBytes = size
			if err = model.UpdateGeneratedMediaLocal(row.Id, localPath, contentType, shaHex, sizeBytes); err != nil {
				return err
			}
		} else {
			return fmt.Errorf("generated media local file missing: %s", localPath)
		}
	}
	if shaHex == "" || sizeBytes <= 0 || contentType == "" {
		sha, size, ct, err := inspectGeneratedMediaFile(localPath, contentType)
		if err != nil {
			return err
		}
		if shaHex == "" {
			shaHex = sha
		}
		if sizeBytes <= 0 {
			sizeBytes = size
		}
		if contentType == "" {
			contentType = ct
		}
	}
	key := strings.TrimSpace(row.OssKey)
	if key == "" {
		key = buildGeneratedMediaOSSKey(row.MediaKind, contentType, shaHex)
	}
	uploader, err := oss.GetUploader()
	if err != nil {
		return err
	}
	if err = putGeneratedMediaFileWithRetry(ctx, uploader, key, localPath, contentType); err != nil {
		return err
	}
	ossURL := generatedMediaPublicURL(key)
	if err = model.MarkGeneratedMediaUploaded(row.Id, key, ossURL, contentType, shaHex, sizeBytes); err != nil {
		return err
	}
	row.OssKey = key
	row.OssURL = ossURL
	row.ContentType = contentType
	row.Sha256 = shaHex
	row.SizeBytes = sizeBytes
	if row.TaskID != "" && row.MediaKind == model.GeneratedMediaKindVideo {
		if err = model.UpdateTaskGeneratedMediaResult(row.TaskID, key, ossURL, sizeBytes); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("generated media update task result failed task=%s: %v", row.TaskID, err))
		}
	}
	if row.TaskID != "" && strings.HasPrefix(row.ModelName, "youchuan-") {
		if _, updateErr := model.UpdateYouchuanTaskGeneratedMedia(row.TaskID, row.SourceURL, key, ossURL, sizeBytes); updateErr != nil {
			logger.LogWarn(ctx, fmt.Sprintf("generated media update Youchuan output failed task=%s: %v", row.TaskID, updateErr))
		}
	}
	_ = os.Remove(localPath)
	return nil
}

func putGeneratedMediaFileWithRetry(ctx context.Context, uploader oss.Uploader, key, localPath, contentType string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	attempts := generatedMediaUploadRetryCount() + 1
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			logger.LogWarn(ctx, fmt.Sprintf("generated media upload retry attempt=%d key=%s after error: %v", attempt, key, lastErr))
			if err := sleepGeneratedMediaUploadRetry(ctx); err != nil {
				return lastErr
			}
		}
		file, err := os.Open(localPath)
		if err != nil {
			return err
		}
		attemptCtx, cancel := generatedMediaUploadAttemptContext(ctx)
		sizeBytes := generatedMediaUploadFileSize(localPath)
		timeoutSec := generatedMediaUploadAttemptTimeoutSec()
		startedAt := time.Now()
		logger.LogInfo(ctx, fmt.Sprintf(
			"generated media upload start key=%s attempt=%d/%d size_bytes=%d content_type=%s attempt_timeout_sec=%d",
			key, attempt+1, attempts, sizeBytes, contentType, timeoutSec,
		))
		reader := io.Reader(file)
		if sizeBytes > 0 {
			reader = newGeneratedMediaUploadProgressReader(ctx, file, key, attempt+1, sizeBytes)
		}
		lastErr = uploader.PutReader(attemptCtx, key, reader, contentType)
		cancel()
		closeErr := file.Close()
		if lastErr == nil {
			logger.LogInfo(ctx, fmt.Sprintf(
				"generated media upload success key=%s attempt=%d/%d size_bytes=%d duration_ms=%d",
				key, attempt+1, attempts, sizeBytes, time.Since(startedAt).Milliseconds(),
			))
			return closeErr
		}
		logger.LogWarn(ctx, fmt.Sprintf(
			"generated media upload failed key=%s attempt=%d/%d size_bytes=%d duration_ms=%d error=%v",
			key, attempt+1, attempts, sizeBytes, time.Since(startedAt).Milliseconds(), lastErr,
		))
		if ctx != nil && ctx.Err() != nil {
			return lastErr
		}
	}
	return lastErr
}

func generatedMediaUploadAttemptContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	sec := generatedMediaUploadAttemptTimeoutSec()
	if sec <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, time.Duration(sec)*time.Second)
}

func generatedMediaUploadAttemptTimeoutSec() int {
	return common.GetEnvOrDefault("GENERATED_MEDIA_UPLOAD_ATTEMPT_TIMEOUT_SEC", 600)
}

func generatedMediaUploadFileSize(localPath string) int64 {
	info, err := os.Stat(localPath)
	if err != nil {
		return 0
	}
	return info.Size()
}

func isGeneratedMediaMissingLocalFileErr(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "generated media local file missing")
}

type generatedMediaUploadProgressReader struct {
	ctx      context.Context
	reader   io.Reader
	key      string
	attempt  int
	total    int64
	read     int64
	interval time.Duration
	nextLog  time.Time
}

func newGeneratedMediaUploadProgressReader(ctx context.Context, reader io.Reader, key string, attempt int, total int64) io.Reader {
	intervalSec := common.GetEnvOrDefault("GENERATED_MEDIA_UPLOAD_PROGRESS_LOG_INTERVAL_SEC", 30)
	if intervalSec <= 0 {
		return reader
	}
	now := time.Now()
	return &generatedMediaUploadProgressReader{
		ctx:      ctx,
		reader:   reader,
		key:      key,
		attempt:  attempt,
		total:    total,
		interval: time.Duration(intervalSec) * time.Second,
		nextLog:  now.Add(time.Duration(intervalSec) * time.Second),
	}
}

func (r *generatedMediaUploadProgressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.read += int64(n)
		now := time.Now()
		if !now.Before(r.nextLog) {
			percent := float64(r.read) * 100 / float64(r.total)
			logger.LogInfo(r.ctx, fmt.Sprintf(
				"generated media upload progress key=%s attempt=%d uploaded_bytes=%d total_bytes=%d percent=%.2f",
				r.key, r.attempt, r.read, r.total, percent,
			))
			r.nextLog = now.Add(r.interval)
		}
	}
	return n, err
}

func ExtractGeneratedMediaBase64(body []byte) []GeneratedMediaCandidate {
	if len(body) == 0 || !json.Valid(body) {
		return nil
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	var out []GeneratedMediaCandidate
	seen := map[string]bool{}
	collectGeneratedMediaBase64(root, "", "$", seen, &out)
	return out
}

func collectGeneratedMediaBase64(v any, key, path string, seen map[string]bool, out *[]GeneratedMediaCandidate) {
	switch value := v.(type) {
	case map[string]any:
		for k, child := range value {
			childPath := path + "." + k
			collectGeneratedMediaBase64(child, k, childPath, seen, out)
		}
	case []any:
		for i, child := range value {
			collectGeneratedMediaBase64(child, key, fmt.Sprintf("%s[%d]", path, i), seen, out)
		}
	case string:
		candidate, ok := decodeGeneratedMediaString(key, path, value)
		if !ok {
			return
		}
		seenKey := candidate.MediaKind + ":" + candidate.Sha256 + ":" + candidate.FieldPath
		if seen[seenKey] {
			return
		}
		seen[seenKey] = true
		*out = append(*out, candidate)
	}
}

func decodeGeneratedMediaString(key, path, value string) (GeneratedMediaCandidate, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return GeneratedMediaCandidate{}, false
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "data:") {
		contentType, encoded, ok := parseDataURI(value)
		if !ok || !isGeneratedMediaContentType(contentType) {
			return GeneratedMediaCandidate{}, false
		}
		data, ok := decodeBase64WithLimit(encoded)
		if !ok {
			return GeneratedMediaCandidate{}, false
		}
		return buildGeneratedMediaCandidate(data, contentType, mediaKindFromContentType(contentType, ""), path), true
	}
	if !isGeneratedMediaBase64Field(key) {
		return GeneratedMediaCandidate{}, false
	}
	data, ok := decodeBase64WithLimit(value)
	if !ok {
		return GeneratedMediaCandidate{}, false
	}
	contentType := http.DetectContentType(data)
	fallbackKind := model.GeneratedMediaKindImage
	if strings.Contains(strings.ToLower(path), "video") {
		fallbackKind = model.GeneratedMediaKindVideo
	}
	kind := mediaKindFromContentType(contentType, fallbackKind)
	if kind == "" {
		return GeneratedMediaCandidate{}, false
	}
	if contentType == "application/octet-stream" && kind == model.GeneratedMediaKindImage {
		contentType = "image/png"
	}
	return buildGeneratedMediaCandidate(data, contentType, kind, path), true
}

func buildGeneratedMediaCandidate(data []byte, contentType, mediaKind, path string) GeneratedMediaCandidate {
	sha := common.Sha256Hex(data)
	return GeneratedMediaCandidate{
		MediaKind:   mediaKind,
		ContentType: normalizeContentType(contentType),
		Data:        data,
		Sha256:      sha,
		FieldPath:   path,
	}
}

func parseDataURI(value string) (contentType, encoded string, ok bool) {
	comma := strings.Index(value, ",")
	if comma <= 5 {
		return "", "", false
	}
	meta := value[5:comma]
	parts := strings.Split(meta, ";")
	if len(parts) == 0 || !strings.EqualFold(parts[len(parts)-1], "base64") {
		return "", "", false
	}
	contentType = "application/octet-stream"
	if strings.TrimSpace(parts[0]) != "" {
		contentType = strings.TrimSpace(parts[0])
	}
	return contentType, value[comma+1:], true
}

func decodeBase64WithLimit(encoded string) ([]byte, bool) {
	encoded = stripBase64Whitespace(encoded)
	if encoded == "" {
		return nil, false
	}
	if base64.StdEncoding.DecodedLen(len(encoded)) > GeneratedMediaCaptureMaxBytes() {
		return nil, false
	}
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	if err != nil || len(data) == 0 {
		return nil, false
	}
	return data, true
}

func stripBase64Whitespace(s string) string {
	if !strings.ContainsAny(s, " \n\r\t") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case ' ', '\n', '\r', '\t':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isGeneratedMediaBase64Field(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	switch k {
	case "b64_json", "b64_image", "bytesbase64encoded", "image_base64", "imagebase64", "video_base64", "videobase64":
		return true
	default:
		return false
	}
}

func isGeneratedMediaContentType(contentType string) bool {
	ct := normalizeContentType(contentType)
	return strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "video/")
}

func mediaKindFromContentType(contentType, fallback string) string {
	ct := normalizeContentType(contentType)
	switch {
	case strings.HasPrefix(ct, "image/"):
		return model.GeneratedMediaKindImage
	case strings.HasPrefix(ct, "video/"):
		return model.GeneratedMediaKindVideo
	case fallback == model.GeneratedMediaKindImage || fallback == model.GeneratedMediaKindVideo:
		return fallback
	default:
		return ""
	}
}

func normalizeContentType(contentType string) string {
	ct := strings.TrimSpace(strings.ToLower(contentType))
	if semi := strings.Index(ct, ";"); semi >= 0 {
		ct = strings.TrimSpace(ct[:semi])
	}
	if ct == "" {
		return "application/octet-stream"
	}
	return ct
}

func chooseTaskGeneratedMediaURL(task *model.Task, taskResult *relaycommon.TaskInfo) string {
	candidates := []string{}
	if taskResult != nil {
		candidates = append(candidates, taskResult.Url, taskResult.RemoteUrl)
	}
	candidates = append(candidates, task.PrivateData.ResultURL)
	for _, candidate := range extractGeneratedMediaURLsFromJSON(task.Data) {
		candidates = append(candidates, candidate)
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if !isHTTPURL(candidate) {
			continue
		}
		if isTaskClientOutputUploadURL(task, candidate) {
			continue
		}
		if strings.Contains(candidate, "/v1/videos/"+task.TaskID+"/content") {
			continue
		}
		return candidate
	}
	return ""
}

func isTaskClientOutputUploadURL(task *model.Task, candidate string) bool {
	if task == nil || task.PrivateData.XAI == nil {
		return false
	}
	uploadURL := strings.TrimSpace(task.PrivateData.XAI.OutputUploadURL)
	return uploadURL != "" && uploadURL == strings.TrimSpace(candidate)
}

func extractGeneratedMediaURLsFromJSON(raw []byte) []string {
	if len(raw) == 0 || !json.Valid(raw) {
		return nil
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil
	}
	var out []string
	collectGeneratedMediaURLs(root, "", &out)
	return out
}

func collectGeneratedMediaURLs(v any, key string, out *[]string) {
	switch value := v.(type) {
	case map[string]any:
		for k, child := range value {
			collectGeneratedMediaURLs(child, k, out)
		}
	case []any:
		for _, child := range value {
			collectGeneratedMediaURLs(child, key, out)
		}
	case string:
		k := strings.ToLower(strings.TrimSpace(key))
		if k == "url" || k == "result_url" || k == "video_url" || k == "download_url" {
			if isHTTPURL(value) {
				*out = append(*out, value)
			}
		}
	}
}

func isHTTPURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func downloadGeneratedMediaToSpool(rawURL, mediaKind string) (localPath, contentType, shaHex string, sizeBytes int64, err error) {
	if !isHTTPURL(rawURL) {
		return "", "", "", 0, fmt.Errorf("invalid generated media url")
	}
	resp, err := DoDownloadRequest(rawURL, "generated_media_oss")
	if err != nil {
		return "", "", "", 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", "", "", 0, fmt.Errorf("download generated media failed, status=%d", resp.StatusCode)
	}
	maxBytes := generatedMediaMaxDownloadBytes()
	if resp.ContentLength > maxBytes {
		return "", "", "", 0, fmt.Errorf("generated media too large: %d > %d", resp.ContentLength, maxBytes)
	}
	dir, err := ensureGeneratedMediaSpoolDir(mediaKind)
	if err != nil {
		return "", "", "", 0, err
	}
	tmp, err := os.CreateTemp(dir, "download-*.tmp")
	if err != nil {
		return "", "", "", 0, err
	}
	tmpPath := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpPath)
		}
	}()
	limitedHead := &limitedBufferWriter{max: 512}
	hasher := sha256.New()
	written, copyErr := copyWithHashAndLimit(tmp, resp.Body, hasher, limitedHead, maxBytes)
	closeErr := tmp.Close()
	if copyErr != nil {
		return "", "", "", 0, copyErr
	}
	if closeErr != nil {
		return "", "", "", 0, closeErr
	}
	headerCT := normalizeContentType(resp.Header.Get("Content-Type"))
	detectedCT := normalizeContentType(http.DetectContentType(limitedHead.buf.Bytes()))
	headerCT, err = resolveGeneratedMediaContentType(mediaKind, headerCT, detectedCT)
	if err != nil {
		return "", "", "", 0, err
	}
	shaHex = hex.EncodeToString(hasher.Sum(nil))
	ext := extensionForContentType(headerCT, rawURL)
	finalPath := filepath.Join(dir, shaHex+ext)
	if _, statErr := os.Stat(finalPath); statErr == nil {
		_ = os.Remove(tmpPath)
	} else if renameErr := os.Rename(tmpPath, finalPath); renameErr != nil {
		return "", "", "", 0, renameErr
	}
	return finalPath, headerCT, shaHex, written, nil
}

func resolveGeneratedMediaContentType(mediaKind, headerCT, detectedCT string) (string, error) {
	if headerCT == "" || headerCT == "application/octet-stream" {
		headerCT = detectedCT
	}
	if mediaKind == model.GeneratedMediaKindVideo {
		if headerCT == "application/octet-stream" {
			headerCT = "video/mp4"
		}
		if !strings.HasPrefix(headerCT, "video/") {
			return "", fmt.Errorf("generated video URL returned unexpected content type %q", headerCT)
		}
	}
	if mediaKind == model.GeneratedMediaKindImage && !strings.HasPrefix(headerCT, "image/") && headerCT == "application/octet-stream" {
		headerCT = "image/png"
	}
	return headerCT, nil
}

func copyWithHashAndLimit(dst io.Writer, src io.Reader, hasher hash.Hash, head io.Writer, maxBytes int64) (int64, error) {
	w := io.MultiWriter(dst, hasher, head)
	limited := io.LimitReader(src, maxBytes+1)
	written, err := io.Copy(w, limited)
	if err != nil {
		return written, err
	}
	if written > maxBytes {
		return written, fmt.Errorf("generated media exceeds max download size: %d > %d", written, maxBytes)
	}
	return written, nil
}

func inspectGeneratedMediaFile(path, existingContentType string) (shaHex string, sizeBytes int64, contentType string, err error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, "", err
	}
	head := &limitedBufferWriter{max: 512}
	hasher := sha256.New()
	if _, err = io.Copy(io.MultiWriter(hasher, head), file); err != nil {
		return "", 0, "", err
	}
	contentType = normalizeContentType(existingContentType)
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = normalizeContentType(http.DetectContentType(head.buf.Bytes()))
	}
	return hex.EncodeToString(hasher.Sum(nil)), info.Size(), contentType, nil
}

func writeGeneratedMediaSpool(mediaKind, contentType, shaHex string, data []byte) (string, error) {
	dir, err := ensureGeneratedMediaSpoolDir(mediaKind)
	if err != nil {
		return "", err
	}
	if shaHex == "" {
		shaHex = common.Sha256Hex(data)
	}
	path := filepath.Join(dir, shaHex+extensionForContentType(contentType, ""))
	if _, err = os.Stat(path); err == nil {
		return path, nil
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		return "", err
	}
	return path, nil
}

func ensureGeneratedMediaSpoolDir(mediaKind string) (string, error) {
	base := common.GetAuditSpoolDir()
	dir := filepath.Join(base, "generated-media", safePathPart(mediaKind))
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	return dir, nil
}

func buildGeneratedMediaOSSKey(mediaKind, contentType, shaHex string) string {
	prefix := strings.Trim(common.GetEnvOrDefaultString("GENERATED_MEDIA_OSS_KEY_PREFIX", ""), "/")
	if prefix == "" {
		auditPrefix := strings.Trim(common.AuditOSSKeyPrefix, "/")
		if auditPrefix != "" {
			prefix = auditPrefix + "/generated-media"
		} else {
			prefix = "generated-media"
		}
	}
	now := time.Now()
	ext := extensionForContentType(contentType, "")
	if shaHex == "" {
		shaHex = fmt.Sprintf("%d", now.UnixNano())
	}
	return fmt.Sprintf("%s/%04d/%02d/%02d/%s/%s%s",
		prefix, now.Year(), now.Month(), now.Day(), safePathPart(mediaKind), shaHex, ext)
}

func generatedMediaPublicURL(key string) string {
	key = strings.TrimLeft(strings.TrimSpace(key), "/")
	if key == "" {
		return ""
	}
	base := strings.TrimSpace(os.Getenv("GENERATED_MEDIA_PUBLIC_BASE_URL"))
	if base == "" {
		base = strings.TrimSpace(os.Getenv("AUDIT_OSS_PUBLIC_BASE_URL"))
	}
	if base != "" {
		return strings.TrimRight(base, "/") + "/" + key
	}
	endpoint := strings.TrimSpace(common.AuditOSSEndpoint)
	if endpoint == "" {
		return ""
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return strings.TrimRight(endpoint, "/") + "/" + strings.TrimLeft(common.AuditOSSBucket+"/"+key, "/")
	}
	if common.AuditOSSForcePathStyle {
		u.Path = strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(common.AuditOSSBucket+"/"+key, "/")
		return u.String()
	}
	if common.AuditOSSBucket != "" {
		u.Host = common.AuditOSSBucket + "." + u.Host
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + key
	return u.String()
}

func extensionForContentType(contentType, rawURL string) string {
	ct := normalizeContentType(contentType)
	switch ct {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "video/mp4":
		return ".mp4"
	case "video/webm":
		return ".webm"
	case "video/quicktime":
		return ".mov"
	}
	if rawURL != "" {
		if u, err := url.Parse(rawURL); err == nil {
			ext := strings.ToLower(filepath.Ext(u.Path))
			if ext != "" && len(ext) <= 8 {
				return ext
			}
		}
	}
	if exts, err := mime.ExtensionsByType(ct); err == nil && len(exts) > 0 {
		return exts[0]
	}
	return ".bin"
}

func billingIDFromAuditLog(row model.ApiAuditLog) string {
	if row.Other != "" {
		if m, err := common.StrToMap(row.Other); err == nil && m != nil {
			if v, ok := m["billing_id"].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return row.RequestId
}

func generatedMediaMaxDownloadBytes() int64 {
	mb := common.GetEnvOrDefault("GENERATED_MEDIA_MAX_DOWNLOAD_MB", defaultGeneratedMediaDownloadMaxMB)
	if mb <= 0 {
		mb = defaultGeneratedMediaDownloadMaxMB
	}
	return int64(mb) * 1024 * 1024
}

func generatedMediaMaxRetry() int {
	retry := common.GetEnvOrDefault("GENERATED_MEDIA_MAX_RETRY", common.AuditUploadMaxRetry)
	if retry <= 0 {
		retry = 8
	}
	return retry
}

func generatedMediaUploadRetryCount() int {
	retry := common.GetEnvOrDefault("GENERATED_MEDIA_UPLOAD_RETRY_COUNT", 1)
	if retry < 0 {
		return 0
	}
	if retry > 5 {
		return 5
	}
	return retry
}

func sleepGeneratedMediaUploadRetry(ctx context.Context) error {
	delayMs := common.GetEnvOrDefault("GENERATED_MEDIA_UPLOAD_RETRY_DELAY_MS", 1000)
	if delayMs <= 0 {
		return nil
	}
	if ctx == nil {
		time.Sleep(time.Duration(delayMs) * time.Millisecond)
		return nil
	}
	timer := time.NewTimer(time.Duration(delayMs) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func generatedMediaBackoff(retryCount int) int64 {
	base := int64(60)
	delay := base << retryCount
	if delay > 3600 {
		delay = 3600
	}
	return common.GetTimestamp() + delay
}

func wakeGeneratedMediaWorkers() {
	if generatedMediaWakeCh == nil {
		return
	}
	select {
	case generatedMediaWakeCh <- struct{}{}:
	default:
	}
}

func safePathPart(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return "unknown"
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
