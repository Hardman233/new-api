package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

var billingSyncOnce sync.Once

func StartBillingSyncTask() {
	billingSyncOnce.Do(func() {
		if !common.GetEnvOrDefaultBool("BILLING_SYNC_ENABLED", false) {
			return
		}
		if common.GetServerRegion() == common.ServerRegionDomestic {
			common.SysLog("billing sync disabled on domestic server")
			return
		}
		if !common.IsMasterNode {
			common.SysLog("billing sync disabled on slave node")
			return
		}
		if strings.TrimSpace(os.Getenv("BILLING_SYNC_TARGET_URL")) == "" || strings.TrimSpace(os.Getenv("BILLING_SYNC_API_KEY")) == "" {
			common.SysLog("billing sync enabled but BILLING_SYNC_TARGET_URL or BILLING_SYNC_API_KEY is empty")
			return
		}
		interval := common.GetEnvOrDefault("BILLING_SYNC_INTERVAL_SEC", 60)
		if interval <= 0 {
			interval = 60
		}
		common.SysLog(fmt.Sprintf("billing sync task started, interval=%ds", interval))
		go func() {
			runBillingSyncOnce()
			ticker := time.NewTicker(time.Duration(interval) * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				runBillingSyncOnce()
			}
		}()
	})
}

func runBillingSyncOnce() {
	batchSize := common.GetEnvOrDefault("BILLING_SYNC_BATCH_SIZE", 100)
	if batchSize <= 0 {
		batchSize = 100
	}
	if err := model.SeedBillingSyncOutboxFromLedger(batchSize); err != nil {
		common.SysLog("billing sync seed failed: " + err.Error())
		return
	}
	items, err := model.ListReadyBillingSyncOutboxes(batchSize)
	if err != nil {
		common.SysLog("billing sync list failed: " + err.Error())
		return
	}
	for _, item := range items {
		if err := syncOneBillingLedger(item); err != nil {
			nextRetry := nextBillingSyncRetryAt(item.RetryCount)
			_ = model.MarkBillingSyncFailed(item.Id, err.Error(), nextRetry)
			common.SysLog(fmt.Sprintf("billing sync failed billing_id=%s: %s", item.BillingID, err.Error()))
			continue
		}
		_ = model.MarkBillingSyncSynced(item.Id)
	}
}

func syncOneBillingLedger(item model.BillingSyncOutbox) error {
	ledger, err := model.GetBillingLedgerByBillingID(item.BillingID)
	if err != nil {
		return err
	}
	if ledger == nil || !model.IsBillingLedgerFinalStatus(ledger.Status) {
		return nil
	}
	payload := buildBillingImportPayload(ledger)
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	targetURL := strings.TrimRight(strings.TrimSpace(os.Getenv("BILLING_SYNC_TARGET_URL")), "/") + "/api/internal/billing/import"
	req, err := http.NewRequest(http.MethodPost, targetURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Api-Key", strings.TrimSpace(os.Getenv("BILLING_SYNC_API_KEY")))

	timeout := common.GetEnvOrDefault("BILLING_SYNC_HTTP_TIMEOUT_SEC", 10)
	if timeout <= 0 {
		timeout = 10
	}
	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("import endpoint status %d", resp.StatusCode)
	}
	var apiResp internalAPIResponse[map[string]interface{}]
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return err
	}
	if !apiResp.Success {
		if strings.TrimSpace(apiResp.Message) == "" {
			return fmt.Errorf("import endpoint returned success=false")
		}
		return fmt.Errorf("%s", apiResp.Message)
	}
	return nil
}

func buildBillingImportPayload(ledger *model.BillingLedger) dto.InternalBillingLedgerImportRequest {
	sourceRegion := strings.TrimSpace(ledger.ServerRegion)
	if sourceRegion == "" {
		sourceRegion = common.GetServerRegion()
	}
	sourceNode := strings.TrimSpace(os.Getenv("SERVER_NODE_NAME"))
	if sourceNode == "" {
		sourceNode, _ = os.Hostname()
	}
	other := map[string]interface{}{}
	if strings.TrimSpace(ledger.Other) != "" {
		if parsed, err := common.StrToMap(ledger.Other); err == nil && parsed != nil {
			other = parsed
		}
	}

	sourceUserID := ledger.UserID
	domesticUserID := 0
	if ledger.UserID > 0 {
		if user, err := model.GetUserById(ledger.UserID, false); err == nil && user != nil {
			sourceUserID = user.Id
			if user.SourceRegion == common.ServerRegionDomestic && user.SourceUserID > 0 {
				domesticUserID = user.SourceUserID
			}
		}
	}

	return dto.InternalBillingLedgerImportRequest{
		SourceRegion:       sourceRegion,
		SourceNode:         sourceNode,
		SourceBillingID:    ledger.BillingID,
		SourceUserID:       sourceUserID,
		DomesticUserID:     domesticUserID,
		BillingType:        ledger.BillingType,
		SourceRequestID:    ledger.SourceRequestID,
		SourceTaskID:       ledger.SourceTaskID,
		Username:           ledger.Username,
		TokenID:            ledger.TokenID,
		TokenName:          ledger.TokenName,
		UserGroup:          ledger.UserGroup,
		ChannelID:          ledger.ChannelID,
		ChannelName:        ledger.ChannelName,
		SupplierName:       ledger.SupplierName,
		ModelName:          ledger.ModelName,
		UpstreamModel:      ledger.UpstreamModelName,
		Quota:              ledger.Quota,
		Currency:           ledger.Currency,
		ExchangeRate:       ledger.ExchangeRate.StringFixed(8),
		AmountCNYReference: ledger.AmountCNYReference.StringFixed(8),
		Status:             ledger.Status,
		UsageSource:        ledger.UsageSource,
		IsStream:           ledger.IsStream,
		PromptTokens:       ledger.PromptTokens,
		CompletionTokens:   ledger.CompletionTokens,
		RequestHash:        ledger.RequestHash,
		ResponseHash:       ledger.ResponseHash,
		CreatedAt:          ledger.CreatedAt,
		SettledAt:          ledger.SettledAt,
		Other:              other,
	}
}

func nextBillingSyncRetryAt(retryCount int) int64 {
	delay := common.GetEnvOrDefault("BILLING_SYNC_RETRY_BASE_SEC", 60)
	if delay <= 0 {
		delay = 60
	}
	for i := 0; i < retryCount && delay < 3600; i++ {
		delay *= 2
	}
	if delay > 3600 {
		delay = 3600
	}
	return common.GetTimestamp() + int64(delay)
}
