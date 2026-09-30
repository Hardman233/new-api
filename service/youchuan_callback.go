package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

var ErrYouchuanFinalizationBusy = errors.New("Youchuan terminal task is being finalized by another worker")

// EnsureYouchuanTaskOSSURLs uploads every non-empty task output and rewrites
// the matching position in the official urls array. Empty audited positions
// remain empty and keep their original index.
func EnsureYouchuanTaskOSSURLs(ctx context.Context, task *model.Task) error {
	if task == nil || task.PrivateData.Youchuan == nil {
		return nil
	}
	if !GeneratedMediaEnabled() {
		return errors.New("Youchuan output requires generated media OSS to be enabled")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	params := GeneratedMediaUploadParamsFromTask(task)
	var firstErr error
	var response map[string]any
	_ = json.Unmarshal(task.Data, &response)
	urls, _ := response["urls"].([]any)

	for index := range task.PrivateData.Youchuan.Outputs {
		output := &task.PrivateData.Youchuan.Outputs[index]
		remoteURL := strings.TrimSpace(output.RemoteURL)
		if remoteURL == "" {
			continue
		}
		if strings.TrimSpace(output.URL) != "" && output.UploadStatus == model.GeneratedMediaStatusUploaded {
			if index < len(urls) {
				urls[index] = output.URL
			}
			continue
		}
		mediaKind := strings.TrimSpace(output.MediaKind)
		if mediaKind == "" {
			mediaKind = model.GeneratedMediaKindImage
		}
		ossURL, row, err := UploadGeneratedMediaURL(ctx, params, remoteURL, mediaKind, fmt.Sprintf("$.urls[%d]", index))
		if err != nil {
			output.UploadStatus = model.GeneratedMediaStatusFailed
			output.UploadError = err.Error()
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		output.URL = strings.TrimSpace(ossURL)
		output.OSSKey = ""
		output.SizeBytes = 0
		output.UploadStatus = model.GeneratedMediaStatusUploaded
		output.UploadError = ""
		if row != nil {
			output.OSSKey = strings.TrimSpace(row.OssKey)
			output.SizeBytes = row.SizeBytes
		}
		if index < len(urls) {
			urls[index] = output.URL
		}
	}
	if len(urls) > 0 {
		response["urls"] = urls
		if encoded, err := json.Marshal(response); err == nil {
			task.Data = encoded
		} else if firstErr == nil {
			firstErr = err
		}
	}
	if _, err := backfillYouchuanResponseMediaSizes(task); err != nil && firstErr == nil {
		firstErr = err
	}
	if encoded, err := model.ApplyYouchuanResponseMediaSizes(task.Data, task.PrivateData.Youchuan.Outputs); err == nil {
		task.Data = encoded
	} else if firstErr == nil {
		firstErr = err
	}
	if err := model.UpdateTaskDataAndPrivateData(task); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// ProcessYouchuanTaskPostprocess performs durable, at-least-once terminal
// processing. Billing is intentionally outside this function so callers can
// choose the existing exact or actual-cost settlement path before delivery.
func ProcessYouchuanTaskPostprocess(ctx context.Context, task *model.Task) error {
	if task == nil || task.PrivateData.Youchuan == nil {
		return nil
	}
	callbackURL := strings.TrimSpace(task.PrivateData.Youchuan.ClientCallbackURL)
	if callbackURL != "" && !isHTTPURL(callbackURL) {
		return errors.New("invalid Youchuan callback URL")
	}
	if len(task.Data) == 0 || !json.Valid(task.Data) {
		return errors.New("Youchuan callback task data is not valid JSON")
	}
	if err := ensureYouchuanResponseMediaSizes(task); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	claimToken, claimed, row, err := model.ClaimYouchuanTaskFinalization(task.TaskID, callbackURL != "", 90*time.Second)
	if err != nil {
		return err
	}
	if !claimed {
		if row != nil && row.Completed {
			return nil
		}
		return ErrYouchuanFinalizationBusy
	}
	stopLease, leaseErrors := keepYouchuanFinalizationLease(task.TaskID, claimToken, 90*time.Second)
	defer stopLease()
	checkLease := func() error {
		select {
		case leaseErr := <-leaseErrors:
			return leaseErr
		default:
			return nil
		}
	}

	release := func(processErr error) error {
		stopLease()
		message := ""
		if processErr != nil {
			message = processErr.Error()
		}
		_ = model.ReleaseYouchuanTaskFinalization(task.TaskID, claimToken, message)
		return processErr
	}
	ossCompletedAt := int64(0)
	if row != nil {
		ossCompletedAt = row.OSSCompletedAt
	}
	if task.Status == model.TaskStatusSuccess && ossCompletedAt == 0 {
		if err := EnsureYouchuanTaskOSSURLs(ctx, task); err != nil {
			return release(err)
		}
		if err := checkLease(); err != nil {
			return release(err)
		}
		ossCompletedAt = time.Now().Unix()
		if err := model.MarkYouchuanTaskOSSCompleted(task.TaskID, claimToken, ossCompletedAt); err != nil {
			return release(err)
		}
	}

	callbackDeliveredAt := int64(0)
	if row != nil {
		callbackDeliveredAt = row.CallbackDeliveredAt
	}
	if task.PrivateData.Youchuan.CallbackDeliveredAt > callbackDeliveredAt {
		callbackDeliveredAt = task.PrivateData.Youchuan.CallbackDeliveredAt
	}
	if callbackURL != "" && callbackDeliveredAt == 0 && task.PrivateData.Youchuan.CallbackDeliveredAt == 0 {
		if err := sendYouchuanClientCallback(ctx, task, callbackURL); err != nil {
			return release(err)
		}
		if err := checkLease(); err != nil {
			return release(err)
		}
		callbackDeliveredAt = time.Now().Unix()
	}
	if callbackDeliveredAt > 0 && task.PrivateData.Youchuan.CallbackDeliveredAt < callbackDeliveredAt {
		if err := model.MarkYouchuanCallbackDelivered(task.TaskID, callbackDeliveredAt); err != nil {
			return release(err)
		}
	}
	stopLease()
	if err := checkLease(); err != nil {
		return release(err)
	}
	if err := model.CompleteYouchuanTaskFinalization(task.TaskID, claimToken, callbackDeliveredAt); err != nil {
		return release(err)
	}
	return nil
}

func ensureYouchuanResponseMediaSizes(task *model.Task) error {
	if task == nil || task.PrivateData.Youchuan == nil {
		return nil
	}
	privateChanged, err := backfillYouchuanResponseMediaSizes(task)
	if err != nil {
		return err
	}
	encoded, err := model.ApplyYouchuanResponseMediaSizes(task.Data, task.PrivateData.Youchuan.Outputs)
	if err != nil {
		return err
	}
	if bytes.Equal(encoded, task.Data) && !privateChanged {
		return nil
	}
	task.Data = encoded
	return model.UpdateTaskDataAndPrivateData(task)
}

// backfillYouchuanResponseMediaSizes repairs tasks that were already uploaded
// before output sizes were persisted in Youchuan private data. Use the OSS
// object identity instead of task ID so multi-output jobs cannot pick another
// output's size.
func backfillYouchuanResponseMediaSizes(task *model.Task) (bool, error) {
	return backfillYouchuanResponseMediaSizesWithLookup(task, model.GetGeneratedMediaSizeBytesByOSSRef)
}

type youchuanMediaSizeLookup func(mediaKind, taskID, ossKey, ossURL string) (int64, error)

func backfillYouchuanResponseMediaSizesWithLookup(task *model.Task, lookup youchuanMediaSizeLookup) (bool, error) {
	if task == nil || task.PrivateData.Youchuan == nil {
		return false, nil
	}
	if lookup == nil {
		return false, nil
	}
	changed := false
	for index := range task.PrivateData.Youchuan.Outputs {
		output := &task.PrivateData.Youchuan.Outputs[index]
		if output.SizeBytes > 0 {
			continue
		}
		ossKey := strings.TrimSpace(output.OSSKey)
		ossURL := strings.TrimSpace(output.URL)
		if ossKey == "" && ossURL == "" {
			continue
		}
		mediaKind := strings.TrimSpace(output.MediaKind)
		if mediaKind == "" {
			mediaKind = model.GeneratedMediaKindImage
		}
		sizeBytes, err := lookup(mediaKind, "", ossKey, ossURL)
		if err != nil {
			return changed, err
		}
		if sizeBytes <= 0 {
			continue
		}
		output.SizeBytes = sizeBytes
		changed = true
	}
	return changed, nil
}

func keepYouchuanFinalizationLease(taskID, claimToken string, lease time.Duration) (func(), <-chan error) {
	if lease <= 0 {
		lease = 90 * time.Second
	}
	interval := lease / 3
	if interval < time.Second {
		interval = time.Second
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	errorsCh := make(chan error, 1)
	var stopOnce sync.Once
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				if err := model.RenewYouchuanTaskFinalization(taskID, claimToken, lease); err != nil {
					select {
					case errorsCh <- err:
					default:
					}
					return
				}
			}
		}
	}()
	return func() {
		stopOnce.Do(func() {
			close(stop)
			<-done
		})
	}, errorsCh
}

// DeliverYouchuanClientCallback is kept as a compatibility wrapper for
// callers outside the task poller. It now uses the durable finalization lease.
func DeliverYouchuanClientCallback(ctx context.Context, task *model.Task) error {
	return ProcessYouchuanTaskPostprocess(ctx, task)
}

func sendYouchuanClientCallback(ctx context.Context, task *model.Task, callbackURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, strings.NewReader(string(task.Data)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-New-Api-Callback-Id", task.TaskID)
	req.Header.Set("X-New-Api-Task-Id", task.TaskID)
	req.Header.Set("X-New-Api-Upstream-Task-Id", task.GetUpstreamTaskID())
	req.Header.Set("X-New-Api-Billing-Id", task.TaskID)
	fetchSetting := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(
		callbackURL,
		fetchSetting.EnableSSRFProtection,
		fetchSetting.AllowPrivateIp,
		fetchSetting.DomainFilterMode,
		fetchSetting.IpFilterMode,
		fetchSetting.DomainList,
		fetchSetting.IpList,
		fetchSetting.AllowedPorts,
		fetchSetting.ApplyIPFilterForDomain,
	); err != nil {
		return fmt.Errorf("Youchuan callback URL rejected: %w", err)
	}
	client := GetHttpClient()
	if client == nil {
		client = &http.Client{CheckRedirect: checkRedirect}
	}
	clientCopy := *client
	clientCopy.Timeout = 30 * time.Second
	client = &clientCopy
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("Youchuan client callback returned HTTP %d", resp.StatusCode)
	}
	return nil
}
