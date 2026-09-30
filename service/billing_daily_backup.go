package service

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

const (
	defaultDailyBillingBackupDir     = "/data/billing-backups"
	defaultDailyBillingBackupRunTime = "00:15"
	dailyBillingBackupVersion        = 1
)

var dailyBillingBackupOnce sync.Once

type dailyBillingBackupSummary struct {
	Version             int                       `json:"version"`
	Date                string                    `json:"date"`
	StartAt             int64                     `json:"start_at"`
	EndAt               int64                     `json:"end_at"`
	StartTime           string                    `json:"start_time"`
	EndTime             string                    `json:"end_time"`
	GeneratedAt         int64                     `json:"generated_at"`
	GeneratedTime       string                    `json:"generated_time"`
	Timezone            string                    `json:"timezone"`
	ServerRegion        string                    `json:"server_region"`
	NodeName            string                    `json:"node_name"`
	LedgerDetailFile    string                    `json:"ledger_detail_file"`
	LogDetailFile       string                    `json:"log_detail_file"`
	LedgerRowCount      int64                     `json:"ledger_row_count"`
	LogRowCount         int64                     `json:"log_row_count"`
	LedgerTotals        dailyBillingBackupTotals  `json:"ledger_totals"`
	LogTotals           dailyBillingBackupTotals  `json:"log_totals"`
	ByCurrency          []dailyBillingBackupGroup `json:"by_currency"`
	ByStatus            []dailyBillingBackupGroup `json:"by_status"`
	ByUsageSource       []dailyBillingBackupGroup `json:"by_usage_source"`
	ByServerRegion      []dailyBillingBackupGroup `json:"by_server_region"`
	BySupplier          []dailyBillingBackupGroup `json:"by_supplier"`
	ByChannel           []dailyBillingBackupGroup `json:"by_channel"`
	ByModel             []dailyBillingBackupGroup `json:"by_model"`
	ByModelChannel      []dailyBillingBackupGroup `json:"by_model_channel"`
	ByUser              []dailyBillingBackupGroup `json:"by_user"`
	ByToken             []dailyBillingBackupGroup `json:"by_token"`
	LogByModel          []dailyBillingBackupGroup `json:"log_by_model"`
	LogByChannel        []dailyBillingBackupGroup `json:"log_by_channel"`
	LogByUser           []dailyBillingBackupGroup `json:"log_by_user"`
	LogByToken          []dailyBillingBackupGroup `json:"log_by_token"`
	LogByUsageSource    []dailyBillingBackupGroup `json:"log_by_usage_source"`
	UsageSourceLegend   map[string]string         `json:"usage_source_legend"`
	BackupFiles         []string                  `json:"backup_files"`
	GenerationDuration  string                    `json:"generation_duration"`
	GenerationDurationM int64                     `json:"generation_duration_ms"`
}

type dailyBillingBackupTotals struct {
	Rows             int64             `json:"rows"`
	SettledRows      int64             `json:"settled_rows"`
	PendingRows      int64             `json:"pending_rows"`
	FailedRows       int64             `json:"failed_rows"`
	RefundedRows     int64             `json:"refunded_rows"`
	FinalRows        int64             `json:"final_rows"`
	NetQuota         int64             `json:"net_quota"`
	SettledQuota     int64             `json:"settled_quota"`
	RefundedQuota    int64             `json:"refunded_quota"`
	AmountByCurrency map[string]string `json:"amount_by_currency"`
	Tokens           backupTokenTotals `json:"tokens"`
}

type backupTokenTotals struct {
	PromptTokens          int64 `json:"prompt_tokens"`
	CompletionTokens      int64 `json:"completion_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
	InputTokensTotal      int64 `json:"input_tokens_total"`
	CacheHitTokens        int64 `json:"cache_hit_tokens"`
	PromptCacheHitTokens  int64 `json:"prompt_cache_hit_tokens"`
	CacheWriteTokens      int64 `json:"cache_write_tokens"`
	CacheCreationTokens   int64 `json:"cache_creation_tokens"`
	CacheCreation5mTokens int64 `json:"cache_creation_5m_tokens"`
	CacheCreation1hTokens int64 `json:"cache_creation_1h_tokens"`
	ImageTokens           int64 `json:"image_tokens"`
	AudioInputTokens      int64 `json:"audio_input_tokens"`
	AudioOutputTokens     int64 `json:"audio_output_tokens"`
	TextInputTokens       int64 `json:"text_input_tokens"`
	TextOutputTokens      int64 `json:"text_output_tokens"`
}

type dailyBillingBackupGroup struct {
	Key              string            `json:"key"`
	Currency         string            `json:"currency,omitempty"`
	ServerRegion     string            `json:"server_region,omitempty"`
	SupplierName     string            `json:"supplier_name,omitempty"`
	ChannelID        int               `json:"channel_id,omitempty"`
	ChannelName      string            `json:"channel_name,omitempty"`
	ModelName        string            `json:"model_name,omitempty"`
	UserID           int               `json:"user_id,omitempty"`
	Username         string            `json:"username,omitempty"`
	TokenID          int               `json:"token_id,omitempty"`
	TokenName        string            `json:"token_name,omitempty"`
	Status           string            `json:"status,omitempty"`
	UsageSource      string            `json:"usage_source,omitempty"`
	Rows             int64             `json:"rows"`
	SettledRows      int64             `json:"settled_rows"`
	PendingRows      int64             `json:"pending_rows"`
	FailedRows       int64             `json:"failed_rows"`
	RefundedRows     int64             `json:"refunded_rows"`
	FinalRows        int64             `json:"final_rows"`
	NetQuota         int64             `json:"net_quota"`
	SettledQuota     int64             `json:"settled_quota"`
	RefundedQuota    int64             `json:"refunded_quota"`
	AmountByCurrency map[string]string `json:"amount_by_currency"`
	Tokens           backupTokenTotals `json:"tokens"`
}

type dailyBillingBackupLedgerDetail struct {
	ID                 uint64                 `json:"id"`
	BillingID          string                 `json:"billing_id"`
	BillingType        string                 `json:"billing_type"`
	SourceRequestID    string                 `json:"source_request_id"`
	SourceTaskID       string                 `json:"source_task_id"`
	UserID             int                    `json:"user_id"`
	Username           string                 `json:"username"`
	TokenID            int                    `json:"token_id"`
	TokenName          string                 `json:"token_name"`
	UserGroup          string                 `json:"user_group"`
	ChannelID          int                    `json:"channel_id"`
	ChannelName        string                 `json:"channel_name"`
	SupplierName       string                 `json:"supplier_name"`
	ServerRegion       string                 `json:"server_region"`
	ModelName          string                 `json:"model_name"`
	UpstreamModelName  string                 `json:"upstream_model_name"`
	Quota              int64                  `json:"quota"`
	Currency           string                 `json:"currency"`
	Amount             string                 `json:"amount"`
	AmountText         string                 `json:"amount_text"`
	AmountCNYReference string                 `json:"amount_cny_reference"`
	ExchangeRate       string                 `json:"exchange_rate"`
	Status             string                 `json:"status"`
	UsageSource        string                 `json:"usage_source"`
	IsStream           bool                   `json:"is_stream"`
	PromptTokens       int64                  `json:"prompt_tokens"`
	CompletionTokens   int64                  `json:"completion_tokens"`
	TotalTokens        int64                  `json:"total_tokens"`
	TokenDetails       backupTokenTotals      `json:"token_details"`
	RequestHash        string                 `json:"request_hash"`
	ResponseHash       string                 `json:"response_hash"`
	CreatedAt          int64                  `json:"created_at"`
	CreatedTime        string                 `json:"created_time"`
	SettledAt          int64                  `json:"settled_at"`
	SettledTime        string                 `json:"settled_time,omitempty"`
	Other              map[string]interface{} `json:"other,omitempty"`
}

type dailyBillingBackupLogDetail struct {
	ID                int                    `json:"id"`
	UserID            int                    `json:"user_id"`
	Username          string                 `json:"username"`
	Type              int                    `json:"type"`
	TokenID           int                    `json:"token_id"`
	TokenName         string                 `json:"token_name"`
	UserGroup         string                 `json:"user_group"`
	ChannelID         int                    `json:"channel_id"`
	ChannelName       string                 `json:"channel_name,omitempty"`
	ModelName         string                 `json:"model_name"`
	Quota             int                    `json:"quota"`
	PromptTokens      int                    `json:"prompt_tokens"`
	CompletionTokens  int                    `json:"completion_tokens"`
	TotalTokens       int                    `json:"total_tokens"`
	TokenDetails      backupTokenTotals      `json:"token_details"`
	UseTime           int                    `json:"use_time"`
	IsStream          bool                   `json:"is_stream"`
	RequestID         string                 `json:"request_id"`
	UpstreamRequestID string                 `json:"upstream_request_id"`
	BillingID         string                 `json:"billing_id,omitempty"`
	BillingType       string                 `json:"billing_type,omitempty"`
	UsageSource       string                 `json:"usage_source,omitempty"`
	CreatedAt         int64                  `json:"created_at"`
	CreatedTime       string                 `json:"created_time"`
	Other             map[string]interface{} `json:"other,omitempty"`
}

type billingBackupAccumulator struct {
	groups map[string]map[string]*dailyBillingBackupGroup
}

func StartDailyBillingBackupTask() {
	dailyBillingBackupOnce.Do(func() {
		if !common.GetEnvOrDefaultBool("DAILY_BILLING_BACKUP_ENABLED", true) {
			return
		}
		if !common.IsMasterNode {
			common.SysLog("daily billing backup disabled on slave node")
			return
		}
		gopool.Go(func() {
			ctx := context.Background()
			runAt := dailyBillingBackupRunAt()
			logger.LogInfo(ctx, fmt.Sprintf("daily billing backup task started: run_at=%s dir=%s", runAt, dailyBillingBackupDir()))
			if common.GetEnvOrDefaultBool("DAILY_BILLING_BACKUP_RUN_ON_STARTUP", false) {
				runDailyBillingBackupSweep(ctx, time.Now())
			}
			for {
				next := nextDailyBillingBackupTime(time.Now(), runAt)
				timer := time.NewTimer(time.Until(next))
				<-timer.C
				runDailyBillingBackupSweep(ctx, time.Now())
			}
		})
	})
}

func runDailyBillingBackupSweep(ctx context.Context, now time.Time) {
	lookbackDays := common.GetEnvOrDefault("DAILY_BILLING_BACKUP_LOOKBACK_DAYS", 1)
	if lookbackDays <= 0 {
		lookbackDays = 1
	}
	if lookbackDays > 31 {
		lookbackDays = 31
	}
	for i := lookbackDays; i >= 1; i-- {
		target := now.AddDate(0, 0, -i)
		if err := RunDailyBillingBackup(ctx, target); err != nil {
			logger.LogError(ctx, fmt.Sprintf("daily billing backup failed date=%s: %s", target.Format("2006-01-02"), err.Error()))
		}
	}
	cleanupOldDailyBillingBackups(ctx)
}

func RunDailyBillingBackup(ctx context.Context, day time.Time) error {
	startWall := time.Now()
	loc := day.Location()
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	end := start.AddDate(0, 0, 1)
	date := start.Format("2006-01-02")

	root := dailyBillingBackupDir()
	monthDir := filepath.Join(root, start.Format("2006-01"))
	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		return err
	}

	ledgerFile := filepath.Join(monthDir, fmt.Sprintf("billing-ledger-%s.jsonl.gz", date))
	logFile := filepath.Join(monthDir, fmt.Sprintf("consume-logs-%s.jsonl.gz", date))
	summaryFile := filepath.Join(monthDir, fmt.Sprintf("summary-%s.json", date))

	acc := newBillingBackupAccumulator()
	summary := dailyBillingBackupSummary{
		Version:          dailyBillingBackupVersion,
		Date:             date,
		StartAt:          start.Unix(),
		EndAt:            end.Unix(),
		StartTime:        start.Format(time.RFC3339),
		EndTime:          end.Format(time.RFC3339),
		GeneratedAt:      time.Now().Unix(),
		GeneratedTime:    time.Now().Format(time.RFC3339),
		Timezone:         loc.String(),
		ServerRegion:     common.GetServerRegion(),
		NodeName:         common.NodeName,
		LedgerDetailFile: filepath.Base(ledgerFile),
		LogDetailFile:    filepath.Base(logFile),
		UsageSourceLegend: map[string]string{
			model.BillingUsageSourceUpstream:   "按上游返回 usage/tokens 计费",
			model.BillingUsageSourceEstimated:  "上游未返回 usage，本地估算 token 计费",
			model.BillingUsageSourcePerCall:    "按次/固定价/任务预扣计费",
			model.BillingUsageSourceTaskActual: "任务完成后按实际结果结算",
			model.BillingUsageSourceNone:       "失败/退款/0 消耗，不计费",
		},
		BackupFiles: []string{filepath.Base(ledgerFile), filepath.Base(logFile), filepath.Base(summaryFile)},
	}

	ledgerRows, ledgerTotals, err := exportDailyBillingLedger(ctx, ledgerFile, start.Unix(), end.Unix(), acc)
	if err != nil {
		return err
	}
	logRows, logTotals, err := exportDailyConsumeLogs(ctx, logFile, start.Unix(), end.Unix(), acc)
	if err != nil {
		return err
	}

	summary.LedgerRowCount = ledgerRows
	summary.LogRowCount = logRows
	summary.LedgerTotals = ledgerTotals
	summary.LogTotals = logTotals
	summary.ByCurrency = acc.sorted("currency")
	summary.ByStatus = acc.sorted("status")
	summary.ByUsageSource = acc.sorted("usage_source")
	summary.ByServerRegion = acc.sorted("server_region")
	summary.BySupplier = acc.sorted("supplier")
	summary.ByChannel = acc.sorted("channel")
	summary.ByModel = acc.sorted("model")
	summary.ByModelChannel = acc.sorted("model_channel")
	summary.ByUser = acc.sorted("user")
	summary.ByToken = acc.sorted("token")
	summary.LogByModel = acc.sorted("log_model")
	summary.LogByChannel = acc.sorted("log_channel")
	summary.LogByUser = acc.sorted("log_user")
	summary.LogByToken = acc.sorted("log_token")
	summary.LogByUsageSource = acc.sorted("log_usage_source")
	summary.GenerationDuration = time.Since(startWall).String()
	summary.GenerationDurationM = time.Since(startWall).Milliseconds()

	if err := writeJSONFileAtomic(summaryFile, summary); err != nil {
		return err
	}
	logger.LogInfo(ctx, fmt.Sprintf("daily billing backup generated date=%s ledger_rows=%d log_rows=%d dir=%s", date, ledgerRows, logRows, monthDir))
	return nil
}

func exportDailyBillingLedger(ctx context.Context, path string, startTs, endTs int64, acc *billingBackupAccumulator) (int64, dailyBillingBackupTotals, error) {
	writer, encoder, closeFn, err := newGzipJSONLWriter(path)
	if err != nil {
		return 0, dailyBillingBackupTotals{}, err
	}

	totals := newDailyBackupTotals()
	var rows int64
	var batchRows []model.BillingLedger
	err = model.DB.WithContext(ctx).
		Where("created_at >= ? AND created_at < ?", startTs, endTs).
		Order("id asc").
		FindInBatches(&batchRows, 1000, func(tx *gorm.DB, batch int) error {
			for _, ledger := range batchRows {
				other := parseJSONMap(ledger.Other)
				detail := buildLedgerBackupDetail(ledger, other)
				if err := encoder.Encode(detail); err != nil {
					return err
				}
				rows++
				addLedgerToTotals(&totals, detail)
				acc.addLedger(detail)
			}
			return nil
		}).Error
	if err != nil {
		_ = closeFn(false)
		return 0, dailyBillingBackupTotals{}, err
	}
	if err := writer.Flush(); err != nil {
		_ = closeFn(false)
		return 0, dailyBillingBackupTotals{}, err
	}
	if err := closeFn(true); err != nil {
		return 0, dailyBillingBackupTotals{}, err
	}
	return rows, totals.finish(), nil
}

func exportDailyConsumeLogs(ctx context.Context, path string, startTs, endTs int64, acc *billingBackupAccumulator) (int64, dailyBillingBackupTotals, error) {
	writer, encoder, closeFn, err := newGzipJSONLWriter(path)
	if err != nil {
		return 0, dailyBillingBackupTotals{}, err
	}

	totals := newDailyBackupTotals()
	var rows int64
	var batchRows []model.Log
	err = model.LOG_DB.WithContext(ctx).
		Where("created_at >= ? AND created_at < ? AND type IN ?", startTs, endTs, []int{model.LogTypeConsume, model.LogTypeRefund}).
		Order("id asc").
		FindInBatches(&batchRows, 1000, func(tx *gorm.DB, batch int) error {
			for _, logRow := range batchRows {
				other := parseJSONMap(logRow.Other)
				detail := buildLogBackupDetail(logRow, other)
				if err := encoder.Encode(detail); err != nil {
					return err
				}
				rows++
				addLogToTotals(&totals, detail)
				acc.addLog(detail)
			}
			return nil
		}).Error
	if err != nil {
		_ = closeFn(false)
		return 0, dailyBillingBackupTotals{}, err
	}
	if err := writer.Flush(); err != nil {
		_ = closeFn(false)
		return 0, dailyBillingBackupTotals{}, err
	}
	if err := closeFn(true); err != nil {
		return 0, dailyBillingBackupTotals{}, err
	}
	return rows, totals.finish(), nil
}

func buildLedgerBackupDetail(ledger model.BillingLedger, other map[string]interface{}) dailyBillingBackupLedgerDetail {
	tokens := tokenTotalsFromColumnsAndOther(ledger.PromptTokens, ledger.CompletionTokens, other)
	detail := dailyBillingBackupLedgerDetail{
		ID:                 ledger.Id,
		BillingID:          ledger.BillingID,
		BillingType:        ledger.BillingType,
		SourceRequestID:    ledger.SourceRequestID,
		SourceTaskID:       ledger.SourceTaskID,
		UserID:             ledger.UserID,
		Username:           ledger.Username,
		TokenID:            ledger.TokenID,
		TokenName:          ledger.TokenName,
		UserGroup:          ledger.UserGroup,
		ChannelID:          ledger.ChannelID,
		ChannelName:        ledger.ChannelName,
		SupplierName:       ledger.SupplierName,
		ServerRegion:       ledger.ServerRegion,
		ModelName:          ledger.ModelName,
		UpstreamModelName:  ledger.UpstreamModelName,
		Quota:              ledger.Quota,
		Currency:           strings.ToUpper(strings.TrimSpace(ledger.Currency)),
		Amount:             ledger.Amount.StringFixed(8),
		AmountText:         ledger.AmountText,
		AmountCNYReference: ledger.AmountCNYReference.StringFixed(8),
		ExchangeRate:       ledger.ExchangeRate.StringFixed(8),
		Status:             strings.TrimSpace(ledger.Status),
		UsageSource:        strings.TrimSpace(ledger.UsageSource),
		IsStream:           ledger.IsStream,
		PromptTokens:       ledger.PromptTokens,
		CompletionTokens:   ledger.CompletionTokens,
		TotalTokens:        ledger.PromptTokens + ledger.CompletionTokens,
		TokenDetails:       tokens,
		RequestHash:        ledger.RequestHash,
		ResponseHash:       ledger.ResponseHash,
		CreatedAt:          ledger.CreatedAt,
		CreatedTime:        formatUnixTime(ledger.CreatedAt),
		SettledAt:          ledger.SettledAt,
		Other:              other,
	}
	if ledger.SettledAt > 0 {
		detail.SettledTime = formatUnixTime(ledger.SettledAt)
	}
	if detail.UsageSource == "" {
		detail.UsageSource = stringFromMap(other, "usage_source")
	}
	return detail
}

func buildLogBackupDetail(logRow model.Log, other map[string]interface{}) dailyBillingBackupLogDetail {
	tokens := tokenTotalsFromColumnsAndOther(int64(logRow.PromptTokens), int64(logRow.CompletionTokens), other)
	return dailyBillingBackupLogDetail{
		ID:                logRow.Id,
		UserID:            logRow.UserId,
		Username:          logRow.Username,
		Type:              logRow.Type,
		TokenID:           logRow.TokenId,
		TokenName:         logRow.TokenName,
		UserGroup:         logRow.Group,
		ChannelID:         logRow.ChannelId,
		ModelName:         logRow.ModelName,
		Quota:             logRow.Quota,
		PromptTokens:      logRow.PromptTokens,
		CompletionTokens:  logRow.CompletionTokens,
		TotalTokens:       logRow.PromptTokens + logRow.CompletionTokens,
		TokenDetails:      tokens,
		UseTime:           logRow.UseTime,
		IsStream:          logRow.IsStream,
		RequestID:         logRow.RequestId,
		UpstreamRequestID: logRow.UpstreamRequestId,
		BillingID:         stringFromMap(other, "billing_id"),
		BillingType:       stringFromMap(other, "billing_type"),
		UsageSource:       stringFromMap(other, "usage_source"),
		CreatedAt:         logRow.CreatedAt,
		CreatedTime:       formatUnixTime(logRow.CreatedAt),
		Other:             other,
	}
}

func newBillingBackupAccumulator() *billingBackupAccumulator {
	return &billingBackupAccumulator{groups: map[string]map[string]*dailyBillingBackupGroup{}}
}

func (a *billingBackupAccumulator) addLedger(detail dailyBillingBackupLedgerDetail) {
	a.add("currency", detail.Currency, dailyBillingBackupGroup{Key: detail.Currency, Currency: detail.Currency}, detail.Status, int64(detail.Quota), detail.Currency, decimalFromString(detail.Amount), detail.TokenDetails)
	a.add("status", detail.Status, dailyBillingBackupGroup{Key: detail.Status, Status: detail.Status}, detail.Status, int64(detail.Quota), detail.Currency, decimalFromString(detail.Amount), detail.TokenDetails)
	a.add("usage_source", detail.UsageSource, dailyBillingBackupGroup{Key: detail.UsageSource, UsageSource: detail.UsageSource}, detail.Status, int64(detail.Quota), detail.Currency, decimalFromString(detail.Amount), detail.TokenDetails)
	a.add("server_region", detail.ServerRegion, dailyBillingBackupGroup{Key: detail.ServerRegion, ServerRegion: detail.ServerRegion}, detail.Status, int64(detail.Quota), detail.Currency, decimalFromString(detail.Amount), detail.TokenDetails)
	a.add("supplier", detail.SupplierName, dailyBillingBackupGroup{Key: detail.SupplierName, SupplierName: detail.SupplierName}, detail.Status, int64(detail.Quota), detail.Currency, decimalFromString(detail.Amount), detail.TokenDetails)
	a.add("channel", strconv.Itoa(detail.ChannelID)+"|"+detail.ChannelName, dailyBillingBackupGroup{Key: strconv.Itoa(detail.ChannelID) + "|" + detail.ChannelName, ChannelID: detail.ChannelID, ChannelName: detail.ChannelName, SupplierName: detail.SupplierName}, detail.Status, int64(detail.Quota), detail.Currency, decimalFromString(detail.Amount), detail.TokenDetails)
	a.add("model", detail.ModelName, dailyBillingBackupGroup{Key: detail.ModelName, ModelName: detail.ModelName}, detail.Status, int64(detail.Quota), detail.Currency, decimalFromString(detail.Amount), detail.TokenDetails)
	modelChannelKey := strconv.Itoa(detail.ChannelID) + "|" + detail.ChannelName + "|" + detail.ModelName
	a.add("model_channel", modelChannelKey, dailyBillingBackupGroup{Key: modelChannelKey, ChannelID: detail.ChannelID, ChannelName: detail.ChannelName, SupplierName: detail.SupplierName, ModelName: detail.ModelName}, detail.Status, int64(detail.Quota), detail.Currency, decimalFromString(detail.Amount), detail.TokenDetails)
	userKey := strconv.Itoa(detail.UserID) + "|" + detail.Username
	a.add("user", userKey, dailyBillingBackupGroup{Key: userKey, UserID: detail.UserID, Username: detail.Username}, detail.Status, int64(detail.Quota), detail.Currency, decimalFromString(detail.Amount), detail.TokenDetails)
	tokenKey := strconv.Itoa(detail.TokenID) + "|" + detail.TokenName
	a.add("token", tokenKey, dailyBillingBackupGroup{Key: tokenKey, TokenID: detail.TokenID, TokenName: detail.TokenName, UserID: detail.UserID, Username: detail.Username}, detail.Status, int64(detail.Quota), detail.Currency, decimalFromString(detail.Amount), detail.TokenDetails)
}

func (a *billingBackupAccumulator) addLog(detail dailyBillingBackupLogDetail) {
	status := "consume"
	quota := int64(detail.Quota)
	if detail.Type == model.LogTypeRefund {
		status = model.BillingLedgerStatusRefunded
	}
	a.add("log_model", detail.ModelName, dailyBillingBackupGroup{Key: detail.ModelName, ModelName: detail.ModelName}, status, quota, "", decimal.Zero, detail.TokenDetails)
	channelKey := strconv.Itoa(detail.ChannelID) + "|" + detail.ChannelName
	a.add("log_channel", channelKey, dailyBillingBackupGroup{Key: channelKey, ChannelID: detail.ChannelID, ChannelName: detail.ChannelName}, status, quota, "", decimal.Zero, detail.TokenDetails)
	userKey := strconv.Itoa(detail.UserID) + "|" + detail.Username
	a.add("log_user", userKey, dailyBillingBackupGroup{Key: userKey, UserID: detail.UserID, Username: detail.Username}, status, quota, "", decimal.Zero, detail.TokenDetails)
	tokenKey := strconv.Itoa(detail.TokenID) + "|" + detail.TokenName
	a.add("log_token", tokenKey, dailyBillingBackupGroup{Key: tokenKey, TokenID: detail.TokenID, TokenName: detail.TokenName, UserID: detail.UserID, Username: detail.Username}, status, quota, "", decimal.Zero, detail.TokenDetails)
	a.add("log_usage_source", detail.UsageSource, dailyBillingBackupGroup{Key: detail.UsageSource, UsageSource: detail.UsageSource}, status, quota, "", decimal.Zero, detail.TokenDetails)
}

func (a *billingBackupAccumulator) add(bucket, key string, seed dailyBillingBackupGroup, status string, quota int64, currency string, amount decimal.Decimal, tokens backupTokenTotals) {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "(empty)"
		seed.Key = key
	}
	if a.groups[bucket] == nil {
		a.groups[bucket] = map[string]*dailyBillingBackupGroup{}
	}
	group := a.groups[bucket][key]
	if group == nil {
		seed.AmountByCurrency = map[string]string{}
		group = &seed
		a.groups[bucket][key] = group
	}
	addGroupMetrics(group, status, quota, currency, amount, tokens)
}

func (a *billingBackupAccumulator) sorted(bucket string) []dailyBillingBackupGroup {
	groups := a.groups[bucket]
	if len(groups) == 0 {
		return []dailyBillingBackupGroup{}
	}
	out := make([]dailyBillingBackupGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NetQuota != out[j].NetQuota {
			return out[i].NetQuota > out[j].NetQuota
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func newDailyBackupTotals() dailyBackupTotalsBuilder {
	return dailyBackupTotalsBuilder{amounts: map[string]decimal.Decimal{}}
}

type dailyBackupTotalsBuilder struct {
	totals  dailyBillingBackupTotals
	amounts map[string]decimal.Decimal
}

func addLedgerToTotals(builder *dailyBackupTotalsBuilder, detail dailyBillingBackupLedgerDetail) {
	addTotalsMetrics(builder, detail.Status, detail.Quota, detail.Currency, decimalFromString(detail.Amount), detail.TokenDetails)
}

func addLogToTotals(builder *dailyBackupTotalsBuilder, detail dailyBillingBackupLogDetail) {
	status := "consume"
	if detail.Type == model.LogTypeRefund {
		status = model.BillingLedgerStatusRefunded
	}
	addTotalsMetrics(builder, status, int64(detail.Quota), "", decimal.Zero, detail.TokenDetails)
}

func addTotalsMetrics(builder *dailyBackupTotalsBuilder, status string, quota int64, currency string, amount decimal.Decimal, tokens backupTokenTotals) {
	builder.totals.Rows++
	switch status {
	case model.BillingLedgerStatusSettled, "consume":
		builder.totals.SettledRows++
		builder.totals.FinalRows++
		builder.totals.SettledQuota += quota
		builder.totals.NetQuota += quota
		if currency != "" {
			builder.amounts[currency] = builder.amounts[currency].Add(amount)
		}
	case model.BillingLedgerStatusRefunded:
		builder.totals.RefundedRows++
		builder.totals.FinalRows++
		builder.totals.RefundedQuota += quota
		builder.totals.NetQuota -= quota
		if currency != "" {
			builder.amounts[currency] = builder.amounts[currency].Sub(amount)
		}
	case model.BillingLedgerStatusFailed:
		builder.totals.FailedRows++
	case model.BillingLedgerStatusPending, model.BillingLedgerStatusFinalizing:
		builder.totals.PendingRows++
	}
	addTokenTotals(&builder.totals.Tokens, tokens)
}

func (b dailyBackupTotalsBuilder) finish() dailyBillingBackupTotals {
	b.totals.AmountByCurrency = formatDecimalMap(b.amounts)
	if b.totals.AmountByCurrency == nil {
		b.totals.AmountByCurrency = map[string]string{}
	}
	return b.totals
}

func addGroupMetrics(group *dailyBillingBackupGroup, status string, quota int64, currency string, amount decimal.Decimal, tokens backupTokenTotals) {
	group.Rows++
	switch status {
	case model.BillingLedgerStatusSettled, "consume":
		group.SettledRows++
		group.FinalRows++
		group.SettledQuota += quota
		group.NetQuota += quota
		addAmount(group, currency, amount)
	case model.BillingLedgerStatusRefunded:
		group.RefundedRows++
		group.FinalRows++
		group.RefundedQuota += quota
		group.NetQuota -= quota
		addAmount(group, currency, amount.Neg())
	case model.BillingLedgerStatusFailed:
		group.FailedRows++
	case model.BillingLedgerStatusPending, model.BillingLedgerStatusFinalizing:
		group.PendingRows++
	}
	addTokenTotals(&group.Tokens, tokens)
}

func addAmount(group *dailyBillingBackupGroup, currency string, amount decimal.Decimal) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return
	}
	if group.AmountByCurrency == nil {
		group.AmountByCurrency = map[string]string{}
	}
	current := decimalFromString(group.AmountByCurrency[currency])
	group.AmountByCurrency[currency] = current.Add(amount).StringFixed(8)
}

func addTokenTotals(target *backupTokenTotals, add backupTokenTotals) {
	target.PromptTokens += add.PromptTokens
	target.CompletionTokens += add.CompletionTokens
	target.TotalTokens += add.TotalTokens
	target.InputTokensTotal += add.InputTokensTotal
	target.CacheHitTokens += add.CacheHitTokens
	target.PromptCacheHitTokens += add.PromptCacheHitTokens
	target.CacheWriteTokens += add.CacheWriteTokens
	target.CacheCreationTokens += add.CacheCreationTokens
	target.CacheCreation5mTokens += add.CacheCreation5mTokens
	target.CacheCreation1hTokens += add.CacheCreation1hTokens
	target.ImageTokens += add.ImageTokens
	target.AudioInputTokens += add.AudioInputTokens
	target.AudioOutputTokens += add.AudioOutputTokens
	target.TextInputTokens += add.TextInputTokens
	target.TextOutputTokens += add.TextOutputTokens
}

func tokenTotalsFromColumnsAndOther(promptTokens, completionTokens int64, other map[string]interface{}) backupTokenTotals {
	inputTotal := int64FromMap(other, "input_tokens_total")
	if inputTotal == 0 {
		inputTotal = int64FromMap(other, "input_tokens")
	}
	if inputTotal == 0 {
		inputTotal = promptTokens
	}
	cacheHit := firstPositiveInt64(
		int64FromMap(other, "cache_tokens"),
		int64FromMap(other, "prompt_cache_hit_tokens"),
		int64FromMap(other, "cached_tokens"),
	)
	cacheCreation := int64FromMap(other, "cache_creation_tokens")
	cacheCreation5m := int64FromMap(other, "cache_creation_tokens_5m")
	cacheCreation1h := int64FromMap(other, "cache_creation_tokens_1h")
	cacheWrite := int64FromMap(other, "cache_write_tokens")
	if cacheWrite == 0 {
		cacheWrite = cacheCreation + cacheCreation5m + cacheCreation1h
	}
	imageTokens := firstPositiveInt64(
		int64FromMap(other, "image_output"),
		int64FromMap(other, "image_tokens"),
	)
	return backupTokenTotals{
		PromptTokens:          promptTokens,
		CompletionTokens:      completionTokens,
		TotalTokens:           promptTokens + completionTokens,
		InputTokensTotal:      inputTotal,
		CacheHitTokens:        cacheHit,
		PromptCacheHitTokens:  int64FromMap(other, "prompt_cache_hit_tokens"),
		CacheWriteTokens:      cacheWrite,
		CacheCreationTokens:   cacheCreation,
		CacheCreation5mTokens: cacheCreation5m,
		CacheCreation1hTokens: cacheCreation1h,
		ImageTokens:           imageTokens,
		AudioInputTokens:      int64FromMap(other, "audio_input"),
		AudioOutputTokens:     int64FromMap(other, "audio_output"),
		TextInputTokens:       int64FromMap(other, "text_input"),
		TextOutputTokens:      int64FromMap(other, "text_output"),
	}
}

func dailyBillingBackupDir() string {
	dir := strings.TrimSpace(os.Getenv("DAILY_BILLING_BACKUP_DIR"))
	if dir == "" {
		dir = defaultDailyBillingBackupDir
	}
	return dir
}

func dailyBillingBackupRunAt() string {
	runAt := strings.TrimSpace(os.Getenv("DAILY_BILLING_BACKUP_RUN_AT"))
	if runAt == "" {
		runAt = defaultDailyBillingBackupRunTime
	}
	return runAt
}

func nextDailyBillingBackupTime(now time.Time, runAt string) time.Time {
	hour, minute := parseHourMinute(runAt)
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func parseHourMinute(value string) (int, int) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, 15
	}
	hour, hErr := strconv.Atoi(parts[0])
	minute, mErr := strconv.Atoi(parts[1])
	if hErr != nil || mErr != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 15
	}
	return hour, minute
}

func cleanupOldDailyBillingBackups(ctx context.Context) {
	retentionDays := common.GetEnvOrDefault("DAILY_BILLING_BACKUP_RETENTION_DAYS", 0)
	if retentionDays <= 0 {
		return
	}
	cutoff := time.Now().AddDate(0, 0, -retentionDays)
	root := dailyBillingBackupDir()
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		if info.ModTime().Before(cutoff) {
			if removeErr := os.Remove(path); removeErr != nil {
				logger.LogWarn(ctx, fmt.Sprintf("daily billing backup cleanup failed path=%s: %s", path, removeErr.Error()))
			}
		}
		return nil
	})
}

func newGzipJSONLWriter(path string) (*gzip.Writer, *json.Encoder, func(bool) error, error) {
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, nil, err
	}
	file, err := os.Create(tmp)
	if err != nil {
		return nil, nil, nil, err
	}
	gzipWriter := gzip.NewWriter(file)
	encoder := json.NewEncoder(gzipWriter)
	closed := false
	closeFn := func(success bool) error {
		if closed {
			return nil
		}
		closed = true
		gzipErr := gzipWriter.Close()
		fileErr := file.Close()
		if !success || gzipErr != nil || fileErr != nil {
			_ = os.Remove(tmp)
			if gzipErr != nil {
				return gzipErr
			}
			return fileErr
		}
		return os.Rename(tmp, path)
	}
	return gzipWriter, encoder, closeFn, nil
}

func writeJSONFileAtomic(path string, value interface{}) error {
	tmp := path + ".tmp"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func parseJSONMap(raw string) map[string]interface{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]interface{}{}
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return map[string]interface{}{}
	}
	return out
}

func int64FromMap(m map[string]interface{}, key string) int64 {
	if m == nil {
		return 0
	}
	value, ok := m[key]
	if !ok || value == nil {
		return 0
	}
	switch v := value.(type) {
	case int:
		return int64(v)
	case int64:
		return v
	case int32:
		return int64(v)
	case float64:
		return int64(v)
	case float32:
		return int64(v)
	case json.Number:
		n, _ := v.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n
	default:
		return 0
	}
}

func stringFromMap(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	value, ok := m[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func firstPositiveInt64(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func decimalFromString(value string) decimal.Decimal {
	d, err := decimal.NewFromString(strings.TrimSpace(value))
	if err != nil {
		return decimal.Zero
	}
	return d
}

func formatDecimalMap(in map[string]decimal.Decimal) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for currency, amount := range in {
		out[currency] = amount.StringFixed(8)
	}
	return out
}

func formatUnixTime(ts int64) string {
	if ts <= 0 {
		return ""
	}
	return time.Unix(ts, 0).Format(time.RFC3339)
}
