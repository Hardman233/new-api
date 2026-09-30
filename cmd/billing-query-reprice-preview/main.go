package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
	"github.com/tidwall/sjson"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ledgerRow struct {
	ID                uint64
	BillingID         string
	BillingType       string
	SourceRequestID   string
	SourceTaskID      string
	UserID            int
	Username          string
	UserGroup         string
	ChannelID         int
	ChannelName       string
	ModelName         string
	ServerRegion      string
	Currency          string
	OldQuota          int64           `gorm:"column:quota"`
	OldAmount         decimal.Decimal `gorm:"column:amount"`
	PromptTokens      int64
	CompletionTokens  int64
	UsageSource       string
	CreatedAt         int64
	Other             string
	BillingMatchInfo  string
	BillingCalcDetail string
}

type calculatedRow struct {
	Ledger         ledgerRow
	GroupRatio     float64
	NewQuota       int64
	NewAmount      decimal.Decimal
	Mode           string
	OldPricing     string
	Pricing        string
	UsageEvidence  string
	RequestSummary string
	Reason         string
}

type batchTarget struct {
	ChannelID  int     `json:"channel_id"`
	UserGroup  string  `json:"user_group"`
	ModelName  string  `json:"model_name"`
	GroupRatio float64 `json:"group_ratio"`
}

type batchConfig struct {
	BatchID      string        `json:"batch_id"`
	ServerRegion string        `json:"server_region"`
	Currency     string        `json:"currency"`
	Start        int64         `json:"start"`
	Before       int64         `json:"before"`
	Targets      []batchTarget `json:"targets"`
	ApplyGuard   applyGuard    `json:"apply_guard"`
}

type applyGuard struct {
	BackupLedgerTable  string `json:"backup_ledger_table"`
	BackupOutboxTable  string `json:"backup_outbox_table"`
	ExpectedRows       int64  `json:"expected_rows"`
	ExpectedOutboxRows int64  `json:"expected_outbox_rows"`
	ExpectedOldQuota   int64  `json:"expected_old_quota"`
	ExpectedOldAmount  string `json:"expected_old_amount"`
	ExpectedNewQuota   int64  `json:"expected_new_quota"`
	ExpectedNewAmount  string `json:"expected_new_amount"`
}

type cliOptions struct {
	Config       string
	Output       string
	Apply        bool
	ConfirmBatch string
}

func main() {
	opts := parseFlags()
	config, err := loadBatchConfig(opts.Config)
	if err != nil {
		fatal(err.Error())
	}
	_ = godotenv.Load(".env")
	common.InitEnv()
	ratio_setting.InitRatioSettings()
	common.IsMasterNode = false
	if err := model.InitDB(); err != nil {
		fatal("connect database: " + err.Error())
	}
	if err := model.InitLogDB(); err != nil {
		fatal("connect log database: " + err.Error())
	}
	model.InitOptionMap()

	ready, skipped, err := calculateAll(config)
	if err != nil {
		fatal(err.Error())
	}
	book := buildWorkbook(config, ready, skipped, opts.Apply)
	if err = writeXLSX(opts.Output, book); err != nil {
		fatal("write Excel: " + err.Error())
	}
	totals := summarizeRows(ready)
	fmt.Printf("generated=%s\n", opts.Output)
	fmt.Printf("matched=%d repriced=%d skipped=%d\n", len(ready)+len(skipped), len(ready), len(skipped))
	fmt.Printf("old_quota=%d new_quota=%d quota_delta=%d\n", totals.OldQuota, totals.NewQuota, totals.NewQuota-totals.OldQuota)
	fmt.Printf("old_amount=%s new_amount=%s amount_delta=%s currency=%s\n", totals.OldAmount.StringFixed(8), totals.NewAmount.StringFixed(8), totals.NewAmount.Sub(totals.OldAmount).StringFixed(8), config.Currency)
	printCountSummary("usage_evidence", ready, func(row calculatedRow) string { return row.UsageEvidence })
	printCountSummary("skip_reason", skipped, func(row calculatedRow) string { return row.Reason })
	databaseChanged := false
	if opts.Apply {
		if err = validateApplyRequest(opts, config, ready, skipped); err != nil {
			fatal("apply guard: " + err.Error())
		}
		if err = applyBatch(config); err != nil {
			fatal("apply transaction rolled back: " + err.Error())
		}
		fmt.Printf("applied=%d outbox_reset=%d batch_id=%s\n", len(ready), config.ApplyGuard.ExpectedOutboxRows, config.BatchID)
		databaseChanged = true
	}
	fmt.Printf("database_changed=%t\n", databaseChanged)
}

func printCountSummary(prefix string, rows []calculatedRow, key func(calculatedRow) string) {
	counts := make(map[string]int)
	for _, row := range rows {
		value := strings.TrimSpace(key(row))
		if value == "" {
			value = "none"
		}
		counts[value]++
	}
	keys := make([]string, 0, len(counts))
	for value := range counts {
		keys = append(keys, value)
	}
	sort.Strings(keys)
	for _, value := range keys {
		fmt.Printf("%s[%s]=%d\n", prefix, value, counts[value])
	}
}

func parseFlags() cliOptions {
	var opts cliOptions
	flag.StringVar(&opts.Config, "config", "", "required batch JSON config")
	flag.StringVar(&opts.Output, "output", "billing-reprice-output/reprice-preview.xlsx", "xlsx output path")
	flag.BoolVar(&opts.Apply, "apply", false, "apply the guarded display-only reprice transaction")
	flag.StringVar(&opts.ConfirmBatch, "confirm-batch", "", "must exactly match config batch_id when --apply is used")
	flag.Parse()
	if strings.TrimSpace(opts.Config) == "" {
		fatal("--config is required")
	}
	if !opts.Apply && strings.TrimSpace(opts.ConfirmBatch) != "" {
		fatal("--confirm-batch may only be used with --apply")
	}
	return opts
}

func loadBatchConfig(path string) (batchConfig, error) {
	var config batchConfig
	data, err := os.ReadFile(path)
	if err != nil {
		return config, fmt.Errorf("read config: %w", err)
	}
	if err = common.Unmarshal(data, &config); err != nil {
		return config, fmt.Errorf("decode config: %w", err)
	}
	config.BatchID = strings.TrimSpace(config.BatchID)
	config.ServerRegion = strings.ToLower(strings.TrimSpace(config.ServerRegion))
	config.Currency = strings.ToUpper(strings.TrimSpace(config.Currency))
	if config.BatchID == "" {
		return config, fmt.Errorf("config batch_id is required")
	}
	if config.ServerRegion != common.ServerRegionOverseas {
		return config, fmt.Errorf("config server_region must be overseas")
	}
	if config.Currency != common.BillingCurrencyUSD {
		return config, fmt.Errorf("config currency must be USD")
	}
	if config.Before <= config.Start {
		return config, fmt.Errorf("config before must be greater than start")
	}
	if len(config.Targets) == 0 {
		return config, fmt.Errorf("config targets must not be empty")
	}
	seen := make(map[string]struct{}, len(config.Targets))
	for i := range config.Targets {
		target := &config.Targets[i]
		target.UserGroup = strings.TrimSpace(target.UserGroup)
		target.ModelName = strings.TrimSpace(target.ModelName)
		if target.ChannelID <= 0 || target.UserGroup == "" || target.ModelName == "" || target.GroupRatio <= 0 {
			return config, fmt.Errorf("invalid target at index %d", i)
		}
		key := targetKey(target.ChannelID, target.UserGroup, target.ModelName)
		if _, ok := seen[key]; ok {
			return config, fmt.Errorf("duplicate target at index %d", i)
		}
		seen[key] = struct{}{}
	}
	return config, nil
}

func targetKey(channelID int, userGroup, modelName string) string {
	return strconv.Itoa(channelID) + "\x00" + userGroup + "\x00" + modelName
}

func calculateAll(config batchConfig) ([]calculatedRow, []calculatedRow, error) {
	return calculateAllWithDB(config, model.DB, false)
}

func calculateAllWithDB(config batchConfig, db *gorm.DB, lockRows bool) ([]calculatedRow, []calculatedRow, error) {
	targets := make(map[string]batchTarget, len(config.Targets))
	for _, target := range config.Targets {
		targets[targetKey(target.ChannelID, target.UserGroup, target.ModelName)] = target
	}

	targetSQL, targetArgs := targetPredicate(config, "")
	query := db.Table("billing_ledger").
		Select("id, billing_id, billing_type, source_request_id, source_task_id, user_id, username, user_group, channel_id, channel_name, model_name, server_region, currency, quota, amount, prompt_tokens, completion_tokens, usage_source, created_at, other, billing_match_info, billing_calc_detail").
		Where("created_at >= ? AND created_at < ?", config.Start, config.Before).
		Where("server_region = ?", config.ServerRegion).
		Where("currency = ?", config.Currency).
		Where(targetSQL, targetArgs...).
		Where("status = ?", model.BillingLedgerStatusSettled).
		Where("billing_type = ?", model.BillingLedgerTypeRequest).
		Order("id")
	if lockRows {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	rows, err := query.Rows()
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var ready, skipped []calculatedRow
	for rows.Next() {
		var row ledgerRow
		if err = db.ScanRows(rows, &row); err != nil {
			return nil, nil, err
		}
		target, ok := targets[targetKey(row.ChannelID, row.UserGroup, row.ModelName)]
		if !ok {
			continue
		}
		result := calculateOne(row, target.GroupRatio)
		if result.Reason == "" {
			ready = append(ready, result)
		} else {
			skipped = append(skipped, result)
		}
	}
	return ready, skipped, rows.Err()
}

func calculateOne(row ledgerRow, groupRatio float64) calculatedRow {
	result := calculatedRow{Ledger: row, GroupRatio: groupRatio}
	other := map[string]interface{}{}
	if strings.TrimSpace(row.Other) != "" {
		if err := common.UnmarshalJsonStr(row.Other, &other); err != nil {
			result.Reason = "invalid_other_json"
			return result
		}
	}
	result.OldPricing = oldPricingSummary(other)
	if row.PromptTokens+row.CompletionTokens+usageDetailTotal(other) <= 0 && !hasStoredTieredParams(other) {
		result.Reason = "missing_usage"
		return result
	}
	if hasSurcharge(other) {
		result.Reason = "unsupported_tool_or_request_surcharge"
		return result
	}
	if billing_setting.GetBillingMode(row.ModelName) == billing_setting.BillingModeTieredExpr {
		return calculateTiered(result, other, groupRatio)
	}
	if row.BillingType == model.BillingLedgerTypeTask {
		result.Reason = "task_without_current_tiered_template"
		return result
	}
	return calculateRatio(result, other, groupRatio)
}

func calculateTiered(result calculatedRow, other map[string]interface{}, groupRatio float64) calculatedRow {
	expression, ok := billing_setting.GetBillingExpr(result.Ledger.ModelName)
	result.Mode = billing_setting.BillingModeTieredExpr
	result.Pricing = expression
	if !ok || strings.TrimSpace(expression) == "" {
		result.Reason = "missing_current_tiered_expression"
		return result
	}
	used := billingexpr.UsedVars(expression)
	for _, name := range []string{"header", "hour", "minute", "weekday", "month", "day"} {
		if used[name] || strings.Contains(expression, "|||") {
			result.Reason = "expression_requires_unavailable_header_or_time_context"
			return result
		}
	}
	requestInput, summary, err := historicalRequestInput(result.Ledger, other, used["param"])
	result.RequestSummary = summary
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	params, evidence, err := tokenParams(result.Ledger, other, used)
	result.UsageEvidence = evidence
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	cost, _, err := billingexpr.RunExprWithRequest(expression, params, requestInput)
	if err != nil {
		result.Reason = "expression_error: " + err.Error()
		return result
	}
	rawAmount := decimal.NewFromFloat(cost).
		Div(decimal.NewFromInt(1_000_000)).
		Mul(decimal.NewFromFloat(groupRatio)).Round(8)
	result.NewQuota = rawAmount.Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Round(0).IntPart()
	result.NewAmount = displayAmount(result.NewQuota)
	return result
}

func calculateRatio(result calculatedRow, other map[string]interface{}, groupRatio float64) calculatedRow {
	modelName := result.Ledger.ModelName
	if price, ok := ratio_setting.GetModelPrice(modelName, false); ok {
		result.Mode = "fixed_price"
		result.Pricing = fmt.Sprintf("price=%g group_ratio=%g", price, groupRatio)
		rawAmount := decimal.NewFromFloat(price).Mul(decimal.NewFromFloat(groupRatio)).Round(8)
		result.NewQuota = rawAmount.Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Round(0).IntPart()
		result.NewAmount = displayAmount(result.NewQuota)
		return result
	}
	modelRatio, ok, _ := ratio_setting.GetModelRatio(modelName)
	if !ok {
		result.Reason = "missing_current_model_ratio"
		return result
	}
	prompt := decimal.NewFromInt(result.Ledger.PromptTokens)
	completion := decimal.NewFromInt(result.Ledger.CompletionTokens)
	cache := decimal.NewFromInt(cacheReadTokens(other))
	cacheCreate := decimal.NewFromInt(cacheWriteTokens(other))
	cacheCreate5m := decimal.NewFromInt(intFromMap(other, "cache_creation_tokens_5m"))
	cacheCreate1h := decimal.NewFromInt(intFromMap(other, "cache_creation_tokens_1h"))
	image := decimal.NewFromInt(imageTokens(other))
	audioInput := decimal.NewFromInt(audioInputTokens(other))
	audioOutput := decimal.NewFromInt(audioOutputTokens(other))
	audioPrice := float64(0)
	if !boolFromMap(other, "audio") && !boolFromMap(other, "ws") && !audioInput.IsZero() {
		audioPrice = operation_setting.GetGeminiInputAudioPricePerMillionTokens(modelName)
		if audioPrice <= 0 {
			result.Reason = "audio_input_price_missing"
			return result
		}
	}
	if !strings.EqualFold(stringFromMap(other, "usage_semantic"), "anthropic") {
		prompt = prompt.Sub(cache).Sub(cacheCreate).Sub(image)
		if audioPrice > 0 {
			prompt = prompt.Sub(audioInput)
		}
		if prompt.IsNegative() {
			result.Reason = "token_details_exceed_prompt_tokens"
			return result
		}
	}
	completionRatio := ratio_setting.GetCompletionRatio(modelName)
	cacheRatio, _ := ratio_setting.GetCacheRatio(modelName)
	createRatio, _ := ratio_setting.GetCreateCacheRatio(modelName)
	imageRatio, _ := ratio_setting.GetImageRatio(modelName)
	audioRatio := ratio_setting.GetAudioRatio(modelName)
	audioCompletionRatio := ratio_setting.GetAudioCompletionRatio(modelName)
	createCost := cacheCreate.Mul(decimal.NewFromFloat(createRatio))
	if !cacheCreate5m.IsZero() || !cacheCreate1h.IsZero() {
		remaining := cacheCreate.Sub(cacheCreate5m).Sub(cacheCreate1h)
		if remaining.IsNegative() {
			remaining = decimal.Zero
		}
		createCost = remaining.Mul(decimal.NewFromFloat(createRatio)).
			Add(cacheCreate5m.Mul(decimal.NewFromFloat(createRatio))).
			Add(cacheCreate1h.Mul(decimal.NewFromFloat(createRatio * 1.6)))
	}
	weighted := prompt.
		Add(completion.Mul(decimal.NewFromFloat(completionRatio))).
		Add(cache.Mul(decimal.NewFromFloat(cacheRatio))).
		Add(createCost).
		Add(image.Mul(decimal.NewFromFloat(imageRatio)))
	if boolFromMap(other, "audio") || boolFromMap(other, "ws") {
		textInput := decimal.NewFromInt(intFromMap(other, "text_input"))
		textOutput := decimal.NewFromInt(intFromMap(other, "text_output"))
		if textInput.IsZero() {
			textInput = prompt.Sub(audioInput)
			if textInput.IsNegative() {
				textInput = decimal.Zero
			}
		}
		if textOutput.IsZero() {
			textOutput = completion.Sub(audioOutput)
			if textOutput.IsNegative() {
				textOutput = decimal.Zero
			}
		}
		weighted = textInput.
			Add(textOutput.Mul(decimal.NewFromFloat(completionRatio))).
			Add(audioInput.Mul(decimal.NewFromFloat(audioRatio))).
			Add(audioOutput.Mul(decimal.NewFromFloat(audioRatio * audioCompletionRatio)))
	}
	result.Mode = "ratio"
	result.Pricing = fmt.Sprintf("model=%g completion=%g cache=%g cache_create=%g image=%g audio=%g audio_output=%g group=%g", modelRatio, completionRatio, cacheRatio, createRatio, imageRatio, audioRatio, audioCompletionRatio, groupRatio)
	quota := weighted.Mul(decimal.NewFromFloat(modelRatio)).Mul(decimal.NewFromFloat(groupRatio))
	if audioPrice > 0 {
		quota = quota.Add(audioInput.Mul(decimal.NewFromFloat(audioPrice)).Div(decimal.NewFromInt(1_000_000)).Mul(decimal.NewFromFloat(groupRatio)).Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
	}
	result.NewQuota = quota.Round(0).IntPart()
	result.NewAmount = displayAmount(result.NewQuota)
	return result
}

func displayAmount(quota int64) decimal.Decimal {
	return decimal.NewFromInt(quota).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Round(8)
}

func historicalRequestInput(row ledgerRow, other map[string]interface{}, required bool) (billingexpr.RequestInput, string, error) {
	if !required {
		return billingexpr.RequestInput{}, "", nil
	}
	if row.BillingType == model.BillingLedgerTypeTask && row.SourceTaskID != "" {
		var task model.Task
		if err := model.DB.Where("task_id = ?", row.SourceTaskID).Order("id desc").First(&task).Error; err == nil {
			if context := task.PrivateData.BillingContext; context != nil && context.BillingRequestInput != nil && len(context.BillingRequestInput.Body) > 0 {
				input, _, err := applySavedRequestParams(*context.BillingRequestInput, other)
				if err != nil {
					return billingexpr.RequestInput{}, "", err
				}
				return input, truncate(string(input.Body), 2000), nil
			}
		}
	}
	requestID := row.SourceRequestID
	if requestID == "" {
		requestID = row.BillingID
	}
	var audit model.ApiAuditLog
	if model.LOG_DB != nil {
		query := model.LOG_DB.Select("request_body, request_truncated, body_storage").
			Where("request_id = ?", requestID).
			Order("id desc").
			Limit(1).
			Find(&audit)
		if query.Error == nil && query.RowsAffected > 0 {
			if audit.RequestBody != "" && !audit.RequestTruncated {
				input, _, err := applySavedRequestParams(billingexpr.RequestInput{Body: []byte(audit.RequestBody)}, other)
				if err != nil {
					return billingexpr.RequestInput{}, "", err
				}
				return input, truncate(string(input.Body), 2000), nil
			}
		}
	}
	input, applied, err := applySavedRequestParams(billingexpr.RequestInput{}, other)
	if err != nil {
		return billingexpr.RequestInput{}, "", err
	}
	if applied {
		return input, truncate(string(input.Body), 2000), nil
	}
	return billingexpr.RequestInput{}, "", fmt.Errorf("skipped_missing_request_params")
}

func applySavedRequestParams(input billingexpr.RequestInput, other map[string]interface{}) (billingexpr.RequestInput, bool, error) {
	serviceTier := strings.TrimSpace(stringFromMap(other, "service_tier"))
	if serviceTier == "" {
		return input, false, nil
	}
	body := bytes.TrimSpace(input.Body)
	if len(body) == 0 {
		body = []byte("{}")
	}
	updated, err := sjson.SetBytes(body, "service_tier", serviceTier)
	if err != nil {
		return billingexpr.RequestInput{}, false, fmt.Errorf("invalid_historical_request_body: %w", err)
	}
	input.Body = updated
	return input, true, nil
}

func tokenParams(row ledgerRow, other map[string]interface{}, used map[string]bool) (billingexpr.TokenParams, string, error) {
	if params, found, err := storedTieredTokenParams(other, used); found || err != nil {
		return params, "tiered_expr_params", err
	}
	if params, found, err := upstreamTieredTokenParams(row, other, used); found || err != nil {
		return params, "upstream_response_logs.usage_json", err
	}
	p := float64(row.PromptTokens)
	c := float64(row.CompletionTokens)
	cr := float64(cacheReadTokens(other))
	cc := float64(intFromMap(other, "cache_creation_tokens"))
	cc5 := float64(intFromMap(other, "cache_creation_tokens_5m"))
	cc1 := float64(intFromMap(other, "cache_creation_tokens_1h"))
	image := float64(imageTokens(other))
	img, imgO := image, float64(0)
	if used["img_o"] {
		img, imgO = 0, image
	}
	ai := float64(audioInputTokens(other))
	ao := float64(audioOutputTokens(other))
	claude := strings.EqualFold(stringFromMap(other, "usage_semantic"), "anthropic")
	length := p
	if claude {
		length = p + cr + cc5 + cc1
	} else {
		cc5 = cc
		if used["cr"] {
			p -= cr
		}
		if used["cc"] {
			p -= cc
		}
		if used["cc1h"] {
			p -= cc1
		}
		if used["img"] {
			p -= img
		}
		if used["ai"] {
			p -= ai
		}
		if used["img_o"] {
			c -= imgO
		}
		if used["ao"] {
			c -= ao
		}
	}
	if p < 0 || c < 0 {
		return billingexpr.TokenParams{}, "billing_ledger", fmt.Errorf("token_details_exceed_total")
	}
	return billingexpr.TokenParams{P: p, C: c, Len: length, CR: cr, CC: cc5, CC1h: cc1, Img: img, ImgO: imgO, AI: ai, AO: ao}, "billing_ledger", nil
}

func upstreamTieredTokenParams(row ledgerRow, other map[string]interface{}, used map[string]bool) (billingexpr.TokenParams, bool, error) {
	if model.LOG_DB == nil {
		return billingexpr.TokenParams{}, false, nil
	}
	requestID := strings.TrimSpace(row.SourceRequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(row.BillingID)
	}
	var upstream model.UpstreamResponseLog
	query := model.LOG_DB.Select("usage_json").
		Where("billing_id = ? OR request_id = ?", row.BillingID, requestID).
		Order("id desc").
		Limit(1).
		Find(&upstream)
	if query.Error != nil {
		return billingexpr.TokenParams{}, true, fmt.Errorf("load_upstream_usage: %w", query.Error)
	}
	if query.RowsAffected == 0 || strings.TrimSpace(upstream.UsageJSON) == "" {
		return billingexpr.TokenParams{}, false, nil
	}
	params, usable, err := tokenParamsFromUsageJSON(upstream.UsageJSON, other, used)
	if err != nil {
		return billingexpr.TokenParams{}, true, err
	}
	if !usable {
		return billingexpr.TokenParams{}, false, nil
	}
	return params, true, nil
}

func tokenParamsFromUsageJSON(raw string, other map[string]interface{}, used map[string]bool) (billingexpr.TokenParams, bool, error) {
	var usage dto.Usage
	if err := common.UnmarshalJsonStr(raw, &usage); err != nil {
		return billingexpr.TokenParams{}, false, fmt.Errorf("invalid_upstream_usage_json: %w", err)
	}
	usage.NormalizeTokenAliases()
	if usage.PromptTokens+usage.CompletionTokens <= 0 {
		return billingexpr.TokenParams{}, false, nil
	}
	isClaude := strings.EqualFold(usage.UsageSemantic, "anthropic") ||
		strings.EqualFold(stringFromMap(other, "usage_semantic"), "anthropic")
	return service.BuildTieredTokenParams(&usage, isClaude, used), true, nil
}

func storedTieredTokenParams(other map[string]interface{}, used map[string]bool) (billingexpr.TokenParams, bool, error) {
	raw, exists := other["tiered_expr_params"]
	if !exists || raw == nil {
		return billingexpr.TokenParams{}, false, nil
	}
	values, ok := valueMap(raw)
	if !ok || len(values) == 0 {
		return billingexpr.TokenParams{}, true, fmt.Errorf("invalid_tiered_expr_params")
	}
	fields := []struct {
		name string
		set  func(float64)
	}{
		{"p", func(v float64) {}},
		{"c", func(v float64) {}},
		{"len", func(v float64) {}},
		{"cr", func(v float64) {}},
		{"cc", func(v float64) {}},
		{"cc1h", func(v float64) {}},
		{"img", func(v float64) {}},
		{"img_o", func(v float64) {}},
		{"ai", func(v float64) {}},
		{"ao", func(v float64) {}},
	}
	params := billingexpr.TokenParams{}
	fields[0].set = func(v float64) { params.P = v }
	fields[1].set = func(v float64) { params.C = v }
	fields[2].set = func(v float64) { params.Len = v }
	fields[3].set = func(v float64) { params.CR = v }
	fields[4].set = func(v float64) { params.CC = v }
	fields[5].set = func(v float64) { params.CC1h = v }
	fields[6].set = func(v float64) { params.Img = v }
	fields[7].set = func(v float64) { params.ImgO = v }
	fields[8].set = func(v float64) { params.AI = v }
	fields[9].set = func(v float64) { params.AO = v }
	for _, field := range fields {
		rawValue, present := values[field.name]
		if !present {
			if used[field.name] {
				return billingexpr.TokenParams{}, true, fmt.Errorf("tiered_expr_params_missing_%s", field.name)
			}
			continue
		}
		value, err := numericValue(rawValue)
		if err != nil {
			return billingexpr.TokenParams{}, true, fmt.Errorf("invalid_tiered_expr_params_%s", field.name)
		}
		field.set(value)
	}
	return params, true, nil
}

func hasStoredTieredParams(other map[string]interface{}) bool {
	values, ok := valueMap(other["tiered_expr_params"])
	return ok && len(values) > 0
}

func valueMap(value interface{}) (map[string]interface{}, bool) {
	if values, ok := value.(map[string]interface{}); ok {
		return values, true
	}
	if encoded, ok := value.(string); ok {
		values := map[string]interface{}{}
		if common.UnmarshalJsonStr(encoded, &values) == nil {
			return values, true
		}
	}
	return nil, false
}

func numericValue(value interface{}) (float64, error) {
	number, err := strconv.ParseFloat(strings.TrimSpace(fmt.Sprint(value)), 64)
	if err != nil {
		return 0, err
	}
	return number, nil
}

func targetPredicate(config batchConfig, prefix string) (string, []interface{}) {
	parts := make([]string, 0, len(config.Targets))
	args := make([]interface{}, 0, len(config.Targets)*3)
	for _, target := range config.Targets {
		parts = append(parts, fmt.Sprintf("(%schannel_id = ? AND %suser_group = ? AND %smodel_name = ?)", prefix, prefix, prefix))
		args = append(args, target.ChannelID, target.UserGroup, target.ModelName)
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func validateApplyRequest(opts cliOptions, config batchConfig, ready, skipped []calculatedRow) error {
	if !opts.Apply {
		return fmt.Errorf("--apply is required")
	}
	if strings.TrimSpace(opts.ConfirmBatch) != config.BatchID {
		return fmt.Errorf("--confirm-batch must exactly equal %q", config.BatchID)
	}
	if err := validateApplyGuard(config.ApplyGuard); err != nil {
		return err
	}
	return validateCalculatedRows(config.ApplyGuard, ready, skipped)
}

func validateApplyGuard(guard applyGuard) error {
	if !validTableIdentifier(guard.BackupLedgerTable) || !validTableIdentifier(guard.BackupOutboxTable) {
		return fmt.Errorf("apply_guard backup table names must be simple SQL identifiers")
	}
	if guard.ExpectedRows <= 0 || guard.ExpectedOutboxRows != guard.ExpectedRows {
		return fmt.Errorf("apply_guard expected row counts are invalid")
	}
	if guard.ExpectedOldQuota <= 0 || guard.ExpectedNewQuota <= 0 {
		return fmt.Errorf("apply_guard expected quota totals are invalid")
	}
	if _, err := decimal.NewFromString(guard.ExpectedOldAmount); err != nil {
		return fmt.Errorf("apply_guard expected_old_amount is invalid: %w", err)
	}
	if _, err := decimal.NewFromString(guard.ExpectedNewAmount); err != nil {
		return fmt.Errorf("apply_guard expected_new_amount is invalid: %w", err)
	}
	return nil
}

func validTableIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r == '_' || (i > 0 && r >= '0' && r <= '9') {
			continue
		}
		return false
	}
	return true
}

func validateCalculatedRows(guard applyGuard, ready, skipped []calculatedRow) error {
	if len(skipped) != 0 {
		return fmt.Errorf("expected skipped=0, got %d", len(skipped))
	}
	if int64(len(ready)) != guard.ExpectedRows {
		return fmt.Errorf("expected ready=%d, got %d", guard.ExpectedRows, len(ready))
	}
	totals := summarizeRows(ready)
	expectedOldAmount, _ := decimal.NewFromString(guard.ExpectedOldAmount)
	expectedNewAmount, _ := decimal.NewFromString(guard.ExpectedNewAmount)
	if totals.OldQuota != guard.ExpectedOldQuota || !totals.OldAmount.Equal(expectedOldAmount) {
		return fmt.Errorf("old totals changed: got quota=%d amount=%s", totals.OldQuota, totals.OldAmount.StringFixed(8))
	}
	if totals.NewQuota != guard.ExpectedNewQuota || !totals.NewAmount.Equal(expectedNewAmount) {
		return fmt.Errorf("new totals changed: got quota=%d amount=%s", totals.NewQuota, totals.NewAmount.StringFixed(8))
	}
	return nil
}

type backupTotals struct {
	Rows   int64           `gorm:"column:rows"`
	Quota  int64           `gorm:"column:quota"`
	Amount decimal.Decimal `gorm:"column:amount"`
}

func applyBatch(config batchConfig) error {
	guard := config.ApplyGuard
	return model.DB.Transaction(func(tx *gorm.DB) error {
		if err := verifyBackupTables(tx, guard); err != nil {
			return err
		}

		ready, skipped, err := calculateAllWithDB(config, tx, true)
		if err != nil {
			return fmt.Errorf("lock and recalculate target ledgers: %w", err)
		}
		if err = validateCalculatedRows(guard, ready, skipped); err != nil {
			return fmt.Errorf("locked target validation: %w", err)
		}
		if err = verifyCurrentLedgersMatchBackup(tx, config); err != nil {
			return err
		}
		if err = lockAndVerifyOutboxes(tx, guard); err != nil {
			return err
		}

		appliedAt := time.Now().UTC().Format(time.RFC3339)
		for _, row := range ready {
			other, matchInfo, calcDetail, amountText, patchErr := buildRepricedEvidence(config.BatchID, row, appliedAt)
			if patchErr != nil {
				return fmt.Errorf("ledger id=%d evidence: %w", row.Ledger.ID, patchErr)
			}
			result := tx.Table("billing_ledger").
				Where("id = ? AND quota = ? AND amount = ?", row.Ledger.ID, row.Ledger.OldQuota, row.Ledger.OldAmount).
				Updates(map[string]interface{}{
					"quota":               row.NewQuota,
					"amount":              row.NewAmount,
					"amount_text":         amountText,
					"billing_match_info":  matchInfo,
					"billing_calc_detail": calcDetail,
					"other":               other,
				})
			if result.Error != nil {
				return fmt.Errorf("update ledger id=%d: %w", row.Ledger.ID, result.Error)
			}
			if result.RowsAffected != 1 {
				return fmt.Errorf("optimistic update failed for ledger id=%d", row.Ledger.ID)
			}
		}

		now := time.Now().Unix()
		outboxResult := tx.Exec(fmt.Sprintf(`
UPDATE billing_sync_outbox AS o
SET status = 'pending',
    retry_count = 0,
    next_retry_at = 0,
    last_error = '',
    last_attempt_at = 0,
    synced_at = 0,
    updated_at = ?
WHERE EXISTS (
    SELECT 1 FROM %s AS b WHERE b.billing_id = o.billing_id
)`, guard.BackupLedgerTable), now)
		if outboxResult.Error != nil {
			return fmt.Errorf("reset billing sync outbox: %w", outboxResult.Error)
		}
		if outboxResult.RowsAffected != guard.ExpectedOutboxRows {
			return fmt.Errorf("expected to reset %d outboxes, reset %d", guard.ExpectedOutboxRows, outboxResult.RowsAffected)
		}

		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
}

func verifyBackupTables(tx *gorm.DB, guard applyGuard) error {
	for _, table := range []string{guard.BackupLedgerTable, guard.BackupOutboxTable} {
		var exists bool
		if err := tx.Raw("SELECT to_regclass(?) IS NOT NULL", "public."+table).Scan(&exists).Error; err != nil {
			return fmt.Errorf("check backup table %s: %w", table, err)
		}
		if !exists {
			return fmt.Errorf("required backup table %s does not exist", table)
		}
	}

	var totals backupTotals
	if err := tx.Table(guard.BackupLedgerTable).
		Select("count(*) AS rows, COALESCE(sum(quota), 0) AS quota, COALESCE(sum(amount), 0) AS amount").
		Scan(&totals).Error; err != nil {
		return fmt.Errorf("read ledger backup totals: %w", err)
	}
	expectedAmount, _ := decimal.NewFromString(guard.ExpectedOldAmount)
	if totals.Rows != guard.ExpectedRows || totals.Quota != guard.ExpectedOldQuota || !totals.Amount.Equal(expectedAmount) {
		return fmt.Errorf("ledger backup totals mismatch: rows=%d quota=%d amount=%s", totals.Rows, totals.Quota, totals.Amount.StringFixed(8))
	}

	var outboxRows int64
	if err := tx.Table(guard.BackupOutboxTable).Count(&outboxRows).Error; err != nil {
		return fmt.Errorf("read outbox backup count: %w", err)
	}
	if outboxRows != guard.ExpectedOutboxRows {
		return fmt.Errorf("outbox backup count mismatch: expected=%d got=%d", guard.ExpectedOutboxRows, outboxRows)
	}
	var backupMismatch int64
	backupQuery := fmt.Sprintf(`
SELECT count(*)
FROM %s AS l
FULL JOIN %s AS o ON o.billing_id = l.billing_id
WHERE l.id IS NULL OR o.id IS NULL`, guard.BackupLedgerTable, guard.BackupOutboxTable)
	if err := tx.Raw(backupQuery).Scan(&backupMismatch).Error; err != nil {
		return fmt.Errorf("compare ledger and outbox backups: %w", err)
	}
	if backupMismatch != 0 {
		return fmt.Errorf("ledger and outbox backups contain different billing IDs: mismatched_rows=%d", backupMismatch)
	}
	return nil
}

func verifyCurrentLedgersMatchBackup(tx *gorm.DB, config batchConfig) error {
	guard := config.ApplyGuard
	targetSQL, targetArgs := targetPredicate(config, "l.")
	query := fmt.Sprintf(`
WITH target AS (
    SELECT l.*
    FROM billing_ledger AS l
    WHERE l.created_at >= ? AND l.created_at < ?
      AND l.server_region = ? AND l.currency = ?
      AND l.status = ? AND l.billing_type = ?
      AND %s
)
SELECT count(*)
FROM target AS l
FULL JOIN %s AS b ON b.id = l.id
WHERE l.id IS NULL OR b.id IS NULL OR to_jsonb(l) IS DISTINCT FROM to_jsonb(b)`, targetSQL, guard.BackupLedgerTable)
	args := []interface{}{config.Start, config.Before, config.ServerRegion, config.Currency, model.BillingLedgerStatusSettled, model.BillingLedgerTypeRequest}
	args = append(args, targetArgs...)
	var mismatch int64
	if err := tx.Raw(query, args...).Scan(&mismatch).Error; err != nil {
		return fmt.Errorf("compare current ledgers with backup: %w", err)
	}
	if mismatch != 0 {
		return fmt.Errorf("current target ledgers differ from full backup: mismatched_rows=%d", mismatch)
	}
	return nil
}

func lockAndVerifyOutboxes(tx *gorm.DB, guard applyGuard) error {
	query := fmt.Sprintf(`
SELECT o.id, o.status
FROM billing_sync_outbox AS o
JOIN %s AS b ON b.billing_id = o.billing_id
ORDER BY o.id
FOR UPDATE OF o`, guard.BackupLedgerTable)
	var rows []struct {
		ID     uint64
		Status string
	}
	if err := tx.Raw(query).Scan(&rows).Error; err != nil {
		return fmt.Errorf("lock billing sync outboxes: %w", err)
	}
	if int64(len(rows)) != guard.ExpectedOutboxRows {
		return fmt.Errorf("current outbox count mismatch: expected=%d got=%d", guard.ExpectedOutboxRows, len(rows))
	}
	for _, row := range rows {
		if row.Status != model.BillingSyncStatusSynced {
			return fmt.Errorf("outbox id=%d must be synced before apply, got status=%s", row.ID, row.Status)
		}
	}
	return nil
}

func buildRepricedEvidence(batchID string, row calculatedRow, appliedAt string) (string, string, string, string, error) {
	other, err := decodeObject(row.Ledger.Other)
	if err != nil {
		return "", "", "", "", fmt.Errorf("decode other: %w", err)
	}
	matchInfo, err := decodeObject(row.Ledger.BillingMatchInfo)
	if err != nil {
		return "", "", "", "", fmt.Errorf("decode billing_match_info: %w", err)
	}
	calcDetail, err := decodeObject(row.Ledger.BillingCalcDetail)
	if err != nil {
		return "", "", "", "", fmt.Errorf("decode billing_calc_detail: %w", err)
	}

	amountText := common.FormatBillingAmountText(row.NewAmount.InexactFloat64(), row.Ledger.Currency)
	pricingSnapshot := map[string]interface{}{
		"mode":        row.Mode,
		"current":     row.Pricing,
		"group_ratio": row.GroupRatio,
	}
	reprice := map[string]interface{}{
		"batch_id":                     batchID,
		"applied_at":                   appliedAt,
		"old_quota":                    row.Ledger.OldQuota,
		"new_quota":                    row.NewQuota,
		"old_amount":                   row.Ledger.OldAmount.StringFixed(8),
		"new_amount":                   row.NewAmount.StringFixed(8),
		"currency":                     row.Ledger.Currency,
		"group_ratio":                  row.GroupRatio,
		"pricing_mode":                 row.Mode,
		"pricing_snapshot":             pricingSnapshot,
		"previous_group_ratio":         other["group_ratio"],
		"previous_pricing_summary":     row.OldPricing,
		"usage_evidence_source":        row.UsageEvidence,
		"previous_billing_match_info":  cloneMap(matchInfo),
		"previous_billing_calc_detail": cloneMap(calcDetail),
	}

	matchInfo["schema_version"] = 1
	matchInfo["mode"] = row.Mode
	matchInfo["matched"] = true
	matchInfo["status"] = model.BillingLedgerStatusSettled
	matchInfo["billing_type"] = row.Ledger.BillingType
	matchInfo["billing_id"] = row.Ledger.BillingID
	matchInfo["model_name"] = row.Ledger.ModelName
	matchInfo["channel_id"] = row.Ledger.ChannelID
	matchInfo["channel_name"] = row.Ledger.ChannelName
	matchInfo["user_group"] = row.Ledger.UserGroup
	matchInfo["currency"] = row.Ledger.Currency
	matchInfo["quota"] = row.NewQuota
	matchInfo["quota_per_unit"] = common.QuotaPerUnit
	matchInfo["match_source"] = "display_reprice"
	matchInfo["display_reprice"] = map[string]interface{}{
		"batch_id": batchID, "group_ratio": row.GroupRatio, "pricing": row.Pricing,
	}

	calcDetail["schema_version"] = 1
	calcDetail["mode"] = row.Mode
	calcDetail["formula"] = "historical usage * current pricing * configured group ratio"
	pricing, _ := valueMap(calcDetail["pricing"])
	if pricing == nil {
		pricing = map[string]interface{}{}
	}
	pricing["display_reprice"] = pricingSnapshot
	calcDetail["pricing"] = pricing
	calcDetail["result"] = map[string]interface{}{
		"quota": row.NewQuota, "amount": row.NewAmount.StringFixed(8), "amount_text": amountText,
		"currency": row.Ledger.Currency, "quota_per_unit": common.QuotaPerUnit,
	}
	patchCalculationSteps(calcDetail, row, amountText, pricingSnapshot)
	calcDetail["display_reprice"] = map[string]interface{}{
		"batch_id": batchID, "applied_at": appliedAt, "usage_evidence_source": row.UsageEvidence,
	}

	other["billing_mode"] = row.Mode
	other["group_ratio"] = row.GroupRatio
	if row.Mode == billing_setting.BillingModeTieredExpr {
		reprice["pricing_snapshot"] = map[string]interface{}{
			"mode":              row.Mode,
			"current":           row.Pricing,
			"current_expr_hash": billingexpr.ExprHashString(row.Pricing),
			"current_expr_b64":  base64.StdEncoding.EncodeToString([]byte(row.Pricing)),
			"group_ratio":       row.GroupRatio,
		}
	}
	other["billing_match_info"] = matchInfo
	other["billing_calc_detail"] = calcDetail
	other["display_reprice"] = reprice

	otherJSON, err := json.Marshal(other)
	if err != nil {
		return "", "", "", "", err
	}
	matchJSON, err := json.Marshal(matchInfo)
	if err != nil {
		return "", "", "", "", err
	}
	calcJSON, err := json.Marshal(calcDetail)
	if err != nil {
		return "", "", "", "", err
	}
	return string(otherJSON), string(matchJSON), string(calcJSON), amountText, nil
}

func decodeObject(raw string) (map[string]interface{}, error) {
	result := map[string]interface{}{}
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	return result, nil
}

func cloneMap(value map[string]interface{}) map[string]interface{} {
	data, _ := json.Marshal(value)
	cloned := map[string]interface{}{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	_ = decoder.Decode(&cloned)
	return cloned
}

func patchCalculationSteps(calcDetail map[string]interface{}, row calculatedRow, amountText string, pricingSnapshot map[string]interface{}) {
	steps, ok := calcDetail["steps"].([]interface{})
	if !ok {
		return
	}
	for _, raw := range steps {
		step, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		switch strings.TrimSpace(fmt.Sprint(step["name"])) {
		case "match_pricing_rule":
			step["mode"] = row.Mode
			step["matched"] = true
		case "apply_pricing":
			pricing, _ := valueMap(step["pricing"])
			if pricing == nil {
				pricing = map[string]interface{}{}
			}
			pricing["display_reprice"] = pricingSnapshot
			step["pricing"] = pricing
			step["formula"] = "historical usage * current pricing * configured group ratio"
		case "quota_to_amount":
			step["quota"] = row.NewQuota
			step["quota_per_unit"] = common.QuotaPerUnit
			step["amount"] = row.NewAmount.StringFixed(8)
			step["amount_text"] = amountText
			step["currency"] = row.Ledger.Currency
		}
	}
}

type rowTotals struct {
	OldQuota  int64
	NewQuota  int64
	OldAmount decimal.Decimal
	NewAmount decimal.Decimal
}

func summarizeRows(rows []calculatedRow) rowTotals {
	totals := rowTotals{OldAmount: decimal.Zero, NewAmount: decimal.Zero}
	for _, row := range rows {
		totals.OldQuota += row.Ledger.OldQuota
		totals.NewQuota += row.NewQuota
		totals.OldAmount = totals.OldAmount.Add(row.Ledger.OldAmount)
		totals.NewAmount = totals.NewAmount.Add(row.NewAmount)
	}
	return totals
}

func buildWorkbook(config batchConfig, ready, skipped []calculatedRow, applyRequested bool) workbook {
	totals := summarizeRows(ready)
	summary := [][]string{
		{"项目", "值"},
		{"批次 ID", config.BatchID},
		{"生成时间", time.Now().Format(time.RFC3339)},
		{"区域", config.ServerRegion},
		{"币种", config.Currency},
		{"开始时间戳(含)", strconv.FormatInt(config.Start, 10)},
		{"开始时间(北京时间)", beijingTime(config.Start).Format(time.RFC3339)},
		{"截止时间戳(不含)", strconv.FormatInt(config.Before, 10)},
		{"截止时间(北京时间)", beijingTime(config.Before).Format(time.RFC3339)},
		{"精确目标数", strconv.Itoa(len(config.Targets))},
		{"匹配账单数", strconv.Itoa(len(ready) + len(skipped))},
		{"可重算笔数", strconv.Itoa(len(ready))},
		{"跳过笔数", strconv.Itoa(len(skipped))},
		{"旧 quota 合计", strconv.FormatInt(totals.OldQuota, 10)},
		{"新 quota 合计", strconv.FormatInt(totals.NewQuota, 10)},
		{"quota 差额", strconv.FormatInt(totals.NewQuota-totals.OldQuota, 10)},
		{"旧金额合计", totals.OldAmount.StringFixed(8)},
		{"新金额合计", totals.NewAmount.StringFixed(8)},
		{"金额差额", totals.NewAmount.Sub(totals.OldAmount).StringFixed(8)},
		{"数据库写入", "无（只读预览）"},
	}
	if applyRequested {
		summary = append(summary, []string{"Apply mode", "requested; see run log for committed result"})
	}
	return workbook{Sheets: []sheet{
		{Name: "汇总", Rows: summary},
		{Name: "按天汇总", Rows: groupedRows(ready, "date")},
		{Name: "按目标汇总", Rows: groupedRows(ready, "target")},
		{Name: "按模型汇总", Rows: groupedRows(ready, "model")},
		{Name: "按用户汇总", Rows: groupedRows(ready, "user")},
		{Name: "按渠道汇总", Rows: groupedRows(ready, "channel")},
		{Name: "可重算明细", Rows: detailRows(ready)},
		{Name: "跳过明细", Rows: detailRows(skipped)},
		{Name: "批次目标", Rows: targetRows(config)},
		{Name: "配置快照", Rows: pricingSnapshot()},
	}}
}

func detailRows(rows []calculatedRow) [][]string {
	data := [][]string{{"ledger_id", "billing_id", "类型", "用户ID", "用户名", "组", "渠道ID", "渠道", "模型", "币种", "创建时间", "新分组折扣", "输入tokens", "输出tokens", "缓存读取tokens", "缓存写入tokens", "缓存写入5m", "缓存写入1h", "图片tokens", "音频输入tokens", "音频输出tokens", "旧quota", "新quota", "quota差额", "旧金额", "新金额", "金额差额", "计价模式", "用量证据来源", "历史计价快照", "今日定价", "请求参数摘要", "跳过原因"}}
	for _, row := range rows {
		l := row.Ledger
		newQuota, quotaDelta, newAmount, amountDelta := "", "", "", ""
		if row.Reason == "" {
			newQuota = strconv.FormatInt(row.NewQuota, 10)
			quotaDelta = strconv.FormatInt(row.NewQuota-l.OldQuota, 10)
			newAmount = row.NewAmount.StringFixed(8)
			amountDelta = row.NewAmount.Sub(l.OldAmount).StringFixed(8)
		}
		data = append(data, []string{
			strconv.FormatUint(l.ID, 10), l.BillingID, l.BillingType, strconv.Itoa(l.UserID), l.Username, l.UserGroup,
			strconv.Itoa(l.ChannelID), l.ChannelName, l.ModelName, l.Currency, beijingTime(l.CreatedAt).Format(time.RFC3339), strconv.FormatFloat(row.GroupRatio, 'f', -1, 64),
			strconv.FormatInt(l.PromptTokens, 10), strconv.FormatInt(l.CompletionTokens, 10), strconv.FormatInt(cacheReadTokensFromJSON(l.Other), 10), strconv.FormatInt(cacheWriteTokensFromJSON(l.Other), 10), strconv.FormatInt(detailTokenFromJSON(l.Other, "cache_creation_tokens_5m"), 10), strconv.FormatInt(detailTokenFromJSON(l.Other, "cache_creation_tokens_1h"), 10), strconv.FormatInt(imageTokensFromJSON(l.Other), 10), strconv.FormatInt(audioInputTokensFromJSON(l.Other), 10), strconv.FormatInt(audioOutputTokensFromJSON(l.Other), 10), strconv.FormatInt(l.OldQuota, 10),
			newQuota, quotaDelta, l.OldAmount.StringFixed(8), newAmount, amountDelta, row.Mode, row.UsageEvidence, row.OldPricing, row.Pricing, row.RequestSummary, row.Reason,
		})
	}
	return data
}

func groupedRows(rows []calculatedRow, mode string) [][]string {
	type totals struct {
		Count                int
		OldQuota, NewQuota   int64
		OldAmount, NewAmount decimal.Decimal
	}
	groups := map[string]totals{}
	for _, row := range rows {
		date := beijingTime(row.Ledger.CreatedAt).Format("2006-01-02")
		groupName := "全部"
		if mode == "model" {
			groupName = row.Ledger.ModelName
		}
		if mode == "user" {
			groupName = fmt.Sprintf("%d / %s", row.Ledger.UserID, row.Ledger.Username)
		}
		if mode == "channel" {
			groupName = fmt.Sprintf("%d / %s", row.Ledger.ChannelID, row.Ledger.ChannelName)
		}
		if mode == "target" {
			groupName = fmt.Sprintf("%d / %s / %s / %s / ratio=%g", row.Ledger.ChannelID, row.Ledger.ChannelName, row.Ledger.UserGroup, row.Ledger.ModelName, row.GroupRatio)
		}
		key := date + "\x00" + groupName
		item := groups[key]
		item.Count++
		item.OldQuota += row.Ledger.OldQuota
		item.NewQuota += row.NewQuota
		item.OldAmount = item.OldAmount.Add(row.Ledger.OldAmount)
		item.NewAmount = item.NewAmount.Add(row.NewAmount)
		groups[key] = item
	}
	data := [][]string{{"日期(北京时间)", "分组", "笔数", "旧quota", "新quota", "quota差额", "旧金额", "新金额", "金额差额"}}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		item := groups[key]
		parts := strings.SplitN(key, "\x00", 2)
		data = append(data, []string{parts[0], parts[1], strconv.Itoa(item.Count), strconv.FormatInt(item.OldQuota, 10), strconv.FormatInt(item.NewQuota, 10), strconv.FormatInt(item.NewQuota-item.OldQuota, 10), item.OldAmount.StringFixed(8), item.NewAmount.StringFixed(8), item.NewAmount.Sub(item.OldAmount).StringFixed(8)})
	}
	return data
}

func beijingTime(timestamp int64) time.Time {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		location = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	return time.Unix(timestamp, 0).In(location)
}

func pricingSnapshot() [][]string {
	rows := [][]string{{"配置项", "当前值(JSON)"}}
	items := []struct{ name, value string }{
		{"ModelRatio", ratio_setting.ModelRatio2JSONString()},
		{"ModelPrice", ratio_setting.ModelPrice2JSONString()},
		{"CompletionRatio", ratio_setting.CompletionRatio2JSONString()},
		{"CacheRatio", ratio_setting.CacheRatio2JSONString()},
		{"CreateCacheRatio", ratio_setting.CreateCacheRatio2JSONString()},
		{"ImageRatio", ratio_setting.ImageRatio2JSONString()},
		{"AudioRatio", ratio_setting.AudioRatio2JSONString()},
		{"AudioCompletionRatio", ratio_setting.AudioCompletionRatio2JSONString()},
		{"GroupRatio", ratio_setting.GroupRatio2JSONString()},
	}
	for _, item := range items {
		rows = append(rows, []string{item.name, truncate(item.value, 30000)})
	}
	expressions := billing_setting.GetBillingExprCopy()
	modelNames := make([]string, 0, len(expressions))
	for modelName := range expressions {
		modelNames = append(modelNames, modelName)
	}
	sort.Strings(modelNames)
	for _, modelName := range modelNames {
		expression := expressions[modelName]
		rows = append(rows, []string{"tiered_expr:" + modelName, expression})
	}
	return rows
}

func targetRows(config batchConfig) [][]string {
	targets := append([]batchTarget(nil), config.Targets...)
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].ChannelID != targets[j].ChannelID {
			return targets[i].ChannelID < targets[j].ChannelID
		}
		if targets[i].UserGroup != targets[j].UserGroup {
			return targets[i].UserGroup < targets[j].UserGroup
		}
		return targets[i].ModelName < targets[j].ModelName
	})
	rows := [][]string{{"批次ID", "渠道ID", "用户分组", "模型", "新分组折扣", "当前计价模式", "当前表达式"}}
	for _, target := range targets {
		expression, _ := billing_setting.GetBillingExpr(target.ModelName)
		rows = append(rows, []string{
			config.BatchID,
			strconv.Itoa(target.ChannelID),
			target.UserGroup,
			target.ModelName,
			strconv.FormatFloat(target.GroupRatio, 'f', -1, 64),
			billing_setting.GetBillingMode(target.ModelName),
			expression,
		})
	}
	return rows
}

func hasSurcharge(other map[string]interface{}) bool {
	for _, key := range []string{"web_search_call_count", "file_search_call_count", "image_generation_call_price", "tool_call_surcharge_quota", "other_ratios"} {
		if value, ok := other[key]; ok {
			s := fmt.Sprint(value)
			if s != "" && s != "0" && s != "[]" && s != "false" {
				return true
			}
		}
	}
	return false
}

func oldPricingSummary(other map[string]interface{}) string {
	keys := []string{"billing_mode", "matched_tier", "model_ratio", "model_price", "completion_ratio", "cache_ratio", "cache_creation_ratio", "image_ratio", "group_ratio", "user_group_ratio", "service_tier", "tiered_expr_params", "expr_b64"}
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		if value, ok := other[key]; ok && value != nil && fmt.Sprint(value) != "" {
			parts = append(parts, key+"="+truncate(fmt.Sprint(value), 500))
		}
	}
	return strings.Join(parts, "; ")
}

func intFromMap(values map[string]interface{}, key string) int64 {
	value := values[key]
	if value == nil {
		return 0
	}
	n, _ := strconv.ParseFloat(fmt.Sprint(value), 64)
	return int64(n)
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func cacheReadTokens(values map[string]interface{}) int64 {
	return firstPositive(intFromMap(values, "cache_tokens"), intFromMap(values, "prompt_cache_hit_tokens"), intFromMap(values, "cached_tokens"))
}

func cacheWriteTokens(values map[string]interface{}) int64 {
	if value := intFromMap(values, "cache_write_tokens"); value > 0 {
		return value
	}
	total := intFromMap(values, "cache_creation_tokens")
	split := intFromMap(values, "cache_creation_tokens_5m") + intFromMap(values, "cache_creation_tokens_1h")
	if total > 0 {
		return total
	}
	return split
}

func imageTokens(values map[string]interface{}) int64 {
	return firstPositive(intFromMap(values, "image_output"), intFromMap(values, "image_tokens"))
}

func audioInputTokens(values map[string]interface{}) int64 {
	return firstPositive(intFromMap(values, "audio_input"), intFromMap(values, "audio_input_token_count"), intFromMap(values, "audio_input_tokens"))
}

func audioOutputTokens(values map[string]interface{}) int64 {
	return firstPositive(intFromMap(values, "audio_output"), intFromMap(values, "audio_output_tokens"))
}

func usageDetailTotal(values map[string]interface{}) int64 {
	return cacheReadTokens(values) + cacheWriteTokens(values) + imageTokens(values) + audioInputTokens(values) + audioOutputTokens(values)
}

func boolFromMap(values map[string]interface{}, key string) bool {
	value := strings.ToLower(strings.TrimSpace(stringFromMap(values, key)))
	return value == "true" || value == "1"
}

func detailMapFromJSON(raw string) map[string]interface{} {
	values := map[string]interface{}{}
	if strings.TrimSpace(raw) != "" {
		_ = common.UnmarshalJsonStr(raw, &values)
	}
	return values
}

func detailTokenFromJSON(raw, key string) int64  { return intFromMap(detailMapFromJSON(raw), key) }
func cacheReadTokensFromJSON(raw string) int64   { return cacheReadTokens(detailMapFromJSON(raw)) }
func cacheWriteTokensFromJSON(raw string) int64  { return cacheWriteTokens(detailMapFromJSON(raw)) }
func imageTokensFromJSON(raw string) int64       { return imageTokens(detailMapFromJSON(raw)) }
func audioInputTokensFromJSON(raw string) int64  { return audioInputTokens(detailMapFromJSON(raw)) }
func audioOutputTokensFromJSON(raw string) int64 { return audioOutputTokens(detailMapFromJSON(raw)) }

func stringFromMap(values map[string]interface{}, key string) string {
	if values[key] == nil {
		return ""
	}
	return fmt.Sprint(values[key])
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, "billing reprice preview:", message)
	os.Exit(1)
}

func ensureOutputDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}
