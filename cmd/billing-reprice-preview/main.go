package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
)

type ledgerRow struct {
	ID               uint64
	BillingID        string
	BillingType      string
	SourceRequestID  string
	SourceTaskID     string
	UserID           int
	Username         string
	UserGroup        string
	ChannelID        int
	ChannelName      string
	ModelName        string
	Currency         string
	OldQuota         int64 `gorm:"column:quota"`
	OldAmount        decimal.Decimal `gorm:"column:amount"`
	PromptTokens     int64
	CompletionTokens int64
	UsageSource      string
	CreatedAt        int64
	Other            string
}

type calculatedRow struct {
	Ledger         ledgerRow
	NewQuota       int64
	NewAmount      decimal.Decimal
	Mode           string
	OldPricing     string
	Pricing        string
	RequestSummary string
	Reason         string
}

type cliOptions struct {
	Start, Before int64
	ChannelID     int
	ModelName     string
	Output        string
}

func main() {
	opts := parseFlags()
	if opts.Before <= opts.Start {
		fatal("--before must be greater than --start")
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

	ready, skipped, err := calculateAll(opts)
	if err != nil {
		fatal(err.Error())
	}
	book := buildWorkbook(opts, ready, skipped)
	if err = writeXLSX(opts.Output, book); err != nil {
		fatal("write Excel: " + err.Error())
	}
	fmt.Printf("generated %s (repriced=%d, skipped=%d); database was not changed\n", opts.Output, len(ready), len(skipped))
}

func parseFlags() cliOptions {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).Unix()
	var opts cliOptions
	flag.Int64Var(&opts.Start, "start", 0, "inclusive Unix timestamp")
	flag.Int64Var(&opts.Before, "before", today, "exclusive Unix timestamp")
	flag.IntVar(&opts.ChannelID, "channel", 0, "optional channel_id")
	flag.StringVar(&opts.ModelName, "model", "", "optional exact model_name")
	flag.StringVar(&opts.Output, "output", "billing-reprice-output/reprice-preview.xlsx", "xlsx output path")
	flag.Parse()
	return opts
}

func calculateAll(opts cliOptions) ([]calculatedRow, []calculatedRow, error) {
	query := model.DB.Table("billing_ledger").
		Select("id, billing_id, billing_type, source_request_id, source_task_id, user_id, username, user_group, channel_id, channel_name, model_name, currency, quota, amount, prompt_tokens, completion_tokens, usage_source, created_at, other").
		Where("created_at >= ? AND created_at < ?", opts.Start, opts.Before).
		Where("status = ?", model.BillingLedgerStatusSettled).
		Where("billing_type IN ?", []string{model.BillingLedgerTypeRequest, model.BillingLedgerTypeTask}).
		Order("id")
	if opts.ChannelID > 0 {
		query = query.Where("channel_id = ?", opts.ChannelID)
	}
	if opts.ModelName != "" {
		query = query.Where("model_name = ?", opts.ModelName)
	}
	rows, err := query.Rows()
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	accountGroups, err := loadAccountGroups()
	if err != nil {
		return nil, nil, err
	}
	var ready, skipped []calculatedRow
	for rows.Next() {
		var row ledgerRow
		if err = model.DB.ScanRows(rows, &row); err != nil {
			return nil, nil, err
		}
		result := calculateOne(row, accountGroups[row.UserID])
		if result.Reason == "" {
			ready = append(ready, result)
		} else {
			skipped = append(skipped, result)
		}
	}
	return ready, skipped, rows.Err()
}

func loadAccountGroups() (map[int]string, error) {
	type userGroup struct {
		ID    int
		Group string
	}
	var users []userGroup
	if err := model.DB.Model(&model.User{}).Find(&users).Error; err != nil {
		return nil, err
	}
	result := make(map[int]string, len(users))
	for _, user := range users {
		result[user.ID] = user.Group
	}
	return result, nil
}

func calculateOne(row ledgerRow, accountGroup string) calculatedRow {
	result := calculatedRow{Ledger: row}
	other := map[string]interface{}{}
	if strings.TrimSpace(row.Other) != "" {
		if err := common.UnmarshalJsonStr(row.Other, &other); err != nil {
			result.Reason = "invalid_other_json"
			return result
		}
	}
	result.OldPricing = oldPricingSummary(other)
	if row.PromptTokens+row.CompletionTokens+usageDetailTotal(other) <= 0 {
		result.Reason = "missing_usage"
		return result
	}
	if hasSurcharge(other) {
		result.Reason = "unsupported_tool_or_request_surcharge"
		return result
	}
	if accountGroup == "" {
		accountGroup = row.UserGroup
	}
	groupRatio := ratio_setting.GetGroupRatio(row.UserGroup)
	if ratio, ok := ratio_setting.GetGroupGroupRatio(accountGroup, row.UserGroup); ok {
		groupRatio = ratio
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
	requestInput, summary, err := historicalRequestInput(result.Ledger, used["param"])
	result.RequestSummary = summary
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	params, err := tokenParams(result.Ledger, other, used)
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	cost, _, err := billingexpr.RunExprWithRequest(expression, params, requestInput)
	if err != nil {
		result.Reason = "expression_error: " + err.Error()
		return result
	}
	result.NewAmount = decimal.NewFromFloat(cost).
		Div(decimal.NewFromInt(1_000_000)).
		Mul(decimal.NewFromFloat(groupRatio)).Round(8)
	result.NewQuota = result.NewAmount.Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Round(0).IntPart()
	return result
}

func calculateRatio(result calculatedRow, other map[string]interface{}, groupRatio float64) calculatedRow {
	modelName := result.Ledger.ModelName
	if price, ok := ratio_setting.GetModelPrice(modelName, false); ok {
		result.Mode = "fixed_price"
		result.Pricing = fmt.Sprintf("price=%g group_ratio=%g", price, groupRatio)
		result.NewAmount = decimal.NewFromFloat(price).Mul(decimal.NewFromFloat(groupRatio)).Round(8)
		result.NewQuota = result.NewAmount.Mul(decimal.NewFromFloat(common.QuotaPerUnit)).Round(0).IntPart()
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
		if audioPrice > 0 { prompt = prompt.Sub(audioInput) }
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
		if remaining.IsNegative() { remaining = decimal.Zero }
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
		if textInput.IsZero() { textInput = prompt.Sub(audioInput); if textInput.IsNegative() { textInput = decimal.Zero } }
		if textOutput.IsZero() { textOutput = completion.Sub(audioOutput); if textOutput.IsNegative() { textOutput = decimal.Zero } }
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
	result.NewAmount = decimal.NewFromInt(result.NewQuota).Div(decimal.NewFromFloat(common.QuotaPerUnit)).Round(8)
	return result
}

func historicalRequestInput(row ledgerRow, required bool) (billingexpr.RequestInput, string, error) {
	if !required {
		return billingexpr.RequestInput{}, "", nil
	}
	if row.BillingType == model.BillingLedgerTypeTask && row.SourceTaskID != "" {
		var task model.Task
		if err := model.DB.Where("task_id = ?", row.SourceTaskID).Order("id desc").First(&task).Error; err == nil {
			if context := task.PrivateData.BillingContext; context != nil && context.BillingRequestInput != nil && len(context.BillingRequestInput.Body) > 0 {
				body := context.BillingRequestInput.Body
				return *context.BillingRequestInput, truncate(string(body), 2000), nil
			}
		}
	}
	requestID := row.SourceRequestID
	if requestID == "" {
		requestID = row.BillingID
	}
	var audit model.ApiAuditLog
	if model.LOG_DB != nil {
		if err := model.LOG_DB.Select("request_body, request_truncated, body_storage").Where("request_id = ?", requestID).Order("id desc").First(&audit).Error; err == nil {
			if audit.RequestBody != "" && !audit.RequestTruncated {
				return billingexpr.RequestInput{Body: []byte(audit.RequestBody)}, truncate(audit.RequestBody, 2000), nil
			}
		}
	}
	return billingexpr.RequestInput{}, "", fmt.Errorf("skipped_missing_request_params")
}

func tokenParams(row ledgerRow, other map[string]interface{}, used map[string]bool) (billingexpr.TokenParams, error) {
	p := float64(row.PromptTokens)
	c := float64(row.CompletionTokens)
	cr := float64(cacheReadTokens(other))
	cc := float64(intFromMap(other, "cache_creation_tokens"))
	cc5 := float64(intFromMap(other, "cache_creation_tokens_5m"))
	cc1 := float64(intFromMap(other, "cache_creation_tokens_1h"))
	image := float64(imageTokens(other))
	img, imgO := image, float64(0)
	if used["img_o"] { img, imgO = 0, image }
	ai := float64(audioInputTokens(other))
	ao := float64(audioOutputTokens(other))
	claude := strings.EqualFold(stringFromMap(other, "usage_semantic"), "anthropic")
	length := p
	if claude {
		length = p + cr + cc5 + cc1
	} else {
		cc5 = cc
		if used["cr"] { p -= cr }
		if used["cc"] { p -= cc }
		if used["cc1h"] { p -= cc1 }
		if used["img"] { p -= img }
		if used["ai"] { p -= ai }
		if used["img_o"] { c -= imgO }
		if used["ao"] { c -= ao }
	}
	if p < 0 || c < 0 {
		return billingexpr.TokenParams{}, fmt.Errorf("token_details_exceed_total")
	}
	return billingexpr.TokenParams{P: p, C: c, Len: length, CR: cr, CC: cc5, CC1h: cc1, Img: img, ImgO: imgO, AI: ai, AO: ao}, nil
}

func buildWorkbook(opts cliOptions, ready, skipped []calculatedRow) workbook {
	oldQuota, newQuota := int64(0), int64(0)
	oldAmount, newAmount := decimal.Zero, decimal.Zero
	for _, row := range ready {
		oldQuota += row.Ledger.OldQuota
		newQuota += row.NewQuota
		oldAmount = oldAmount.Add(row.Ledger.OldAmount)
		newAmount = newAmount.Add(row.NewAmount)
	}
	summary := [][]string{
		{"项目", "值"},
		{"生成时间", time.Now().Format(time.RFC3339)},
		{"开始时间戳(含)", strconv.FormatInt(opts.Start, 10)},
		{"截止时间戳(不含)", strconv.FormatInt(opts.Before, 10)},
		{"可重算笔数", strconv.Itoa(len(ready))},
		{"跳过笔数", strconv.Itoa(len(skipped))},
		{"旧 quota 合计", strconv.FormatInt(oldQuota, 10)},
		{"新 quota 合计", strconv.FormatInt(newQuota, 10)},
		{"quota 差额", strconv.FormatInt(newQuota-oldQuota, 10)},
		{"旧金额合计", oldAmount.StringFixed(8)},
		{"新金额合计", newAmount.StringFixed(8)},
		{"金额差额", newAmount.Sub(oldAmount).StringFixed(8)},
		{"数据库写入", "无（只读预览）"},
	}
	return workbook{Sheets: []sheet{
		{Name: "汇总", Rows: summary},
		{Name: "按天汇总", Rows: groupedRows(ready, "date")},
		{Name: "按模型汇总", Rows: groupedRows(ready, "model")},
		{Name: "按用户汇总", Rows: groupedRows(ready, "user")},
		{Name: "按渠道汇总", Rows: groupedRows(ready, "channel")},
		{Name: "可重算明细", Rows: detailRows(ready)},
		{Name: "跳过明细", Rows: detailRows(skipped)},
		{Name: "配置快照", Rows: pricingSnapshot()},
	}}
}

func detailRows(rows []calculatedRow) [][]string {
	data := [][]string{{"ledger_id", "billing_id", "类型", "用户ID", "用户名", "组", "渠道ID", "渠道", "模型", "币种", "创建时间", "输入tokens", "输出tokens", "缓存读取tokens", "缓存写入tokens", "缓存写入5m", "缓存写入1h", "图片tokens", "音频输入tokens", "音频输出tokens", "旧quota", "新quota", "quota差额", "旧金额", "新金额", "金额差额", "计价模式", "历史计价快照", "今日定价", "请求参数摘要", "跳过原因"}}
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
			strconv.Itoa(l.ChannelID), l.ChannelName, l.ModelName, l.Currency, beijingTime(l.CreatedAt).Format(time.RFC3339),
			strconv.FormatInt(l.PromptTokens, 10), strconv.FormatInt(l.CompletionTokens, 10), strconv.FormatInt(cacheReadTokensFromJSON(l.Other), 10), strconv.FormatInt(cacheWriteTokensFromJSON(l.Other), 10), strconv.FormatInt(detailTokenFromJSON(l.Other, "cache_creation_tokens_5m"), 10), strconv.FormatInt(detailTokenFromJSON(l.Other, "cache_creation_tokens_1h"), 10), strconv.FormatInt(imageTokensFromJSON(l.Other), 10), strconv.FormatInt(audioInputTokensFromJSON(l.Other), 10), strconv.FormatInt(audioOutputTokensFromJSON(l.Other), 10), strconv.FormatInt(l.OldQuota, 10),
			newQuota, quotaDelta, l.OldAmount.StringFixed(8), newAmount, amountDelta, row.Mode, row.OldPricing, row.Pricing, row.RequestSummary, row.Reason,
		})
	}
	return data
}

func groupedRows(rows []calculatedRow, mode string) [][]string {
	type totals struct { Count int; OldQuota, NewQuota int64; OldAmount, NewAmount decimal.Decimal }
	groups := map[string]totals{}
	for _, row := range rows {
		date := beijingTime(row.Ledger.CreatedAt).Format("2006-01-02")
		groupName := "全部"
		if mode == "model" { groupName = row.Ledger.ModelName }
		if mode == "user" { groupName = fmt.Sprintf("%d / %s", row.Ledger.UserID, row.Ledger.Username) }
		if mode == "channel" { groupName = fmt.Sprintf("%d / %s", row.Ledger.ChannelID, row.Ledger.ChannelName) }
		key := date + "\x00" + groupName
		item := groups[key]
		item.Count++; item.OldQuota += row.Ledger.OldQuota; item.NewQuota += row.NewQuota
		item.OldAmount = item.OldAmount.Add(row.Ledger.OldAmount); item.NewAmount = item.NewAmount.Add(row.NewAmount)
		groups[key] = item
	}
	data := [][]string{{"日期(北京时间)", "分组", "笔数", "旧quota", "新quota", "quota差额", "旧金额", "新金额", "金额差额"}}
	keys := make([]string, 0, len(groups))
	for key := range groups { keys = append(keys, key) }
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
	if err != nil { location = time.FixedZone("Asia/Shanghai", 8*60*60) }
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
	for _, item := range items { rows = append(rows, []string{item.name, item.value}) }
	for modelName, expression := range billing_setting.GetBillingExprCopy() {
		rows = append(rows, []string{"tiered_expr:" + modelName, expression})
	}
	return rows
}

func hasSurcharge(other map[string]interface{}) bool {
	for _, key := range []string{"web_search_call_count", "file_search_call_count", "image_generation_call_price", "tool_call_surcharge_quota", "other_ratios"} {
		if value, ok := other[key]; ok {
			s := fmt.Sprint(value)
			if s != "" && s != "0" && s != "[]" && s != "false" { return true }
		}
	}
	return false
}

func oldPricingSummary(other map[string]interface{}) string {
	keys := []string{"billing_mode", "matched_tier", "model_ratio", "model_price", "completion_ratio", "cache_ratio", "cache_creation_ratio", "image_ratio", "group_ratio", "user_group_ratio", "expr_b64"}
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
	if value == nil { return 0 }
	n, _ := strconv.ParseFloat(fmt.Sprint(value), 64)
	return int64(n)
}

func firstPositive(values ...int64) int64 {
	for _, value := range values { if value > 0 { return value } }
	return 0
}

func cacheReadTokens(values map[string]interface{}) int64 {
	return firstPositive(intFromMap(values, "cache_tokens"), intFromMap(values, "prompt_cache_hit_tokens"), intFromMap(values, "cached_tokens"))
}

func cacheWriteTokens(values map[string]interface{}) int64 {
	if value := intFromMap(values, "cache_write_tokens"); value > 0 { return value }
	total := intFromMap(values, "cache_creation_tokens")
	split := intFromMap(values, "cache_creation_tokens_5m") + intFromMap(values, "cache_creation_tokens_1h")
	if total > 0 { return total }
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
	if strings.TrimSpace(raw) != "" { _ = common.UnmarshalJsonStr(raw, &values) }
	return values
}

func detailTokenFromJSON(raw, key string) int64 { return intFromMap(detailMapFromJSON(raw), key) }
func cacheReadTokensFromJSON(raw string) int64 { return cacheReadTokens(detailMapFromJSON(raw)) }
func cacheWriteTokensFromJSON(raw string) int64 { return cacheWriteTokens(detailMapFromJSON(raw)) }
func imageTokensFromJSON(raw string) int64 { return imageTokens(detailMapFromJSON(raw)) }
func audioInputTokensFromJSON(raw string) int64 { return audioInputTokens(detailMapFromJSON(raw)) }
func audioOutputTokensFromJSON(raw string) int64 { return audioOutputTokens(detailMapFromJSON(raw)) }

func stringFromMap(values map[string]interface{}, key string) string {
	if values[key] == nil { return "" }
	return fmt.Sprint(values[key])
}

func truncate(value string, limit int) string {
	if len(value) <= limit { return value }
	return value[:limit] + "..."
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, "billing reprice preview:", message)
	os.Exit(1)
}

func ensureOutputDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." { return nil }
	return os.MkdirAll(dir, 0o755)
}
