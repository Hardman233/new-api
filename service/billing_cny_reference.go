package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

const (
	defaultExchangeRateProviderURL = "https://hq.sinajs.cn/list=fx_susdcny"
	defaultExchangeRateTarget      = "CNY"
	defaultExchangeRateIntervalSec = 600
	defaultExchangeRateTimeoutSec  = 5
	billingReferencePrecision      = 8
)

var sinaExchangeRatePattern = regexp.MustCompile(`var\s+hq_str_[^=]+="([^"]*)"`)

type ExchangeRateSnapshot struct {
	Rate      float64
	Base      string
	Target    string
	UpdatedAt int64
	Source    string
}

var (
	billingCNYReferenceOnce sync.Once
	exchangeRateMu          sync.RWMutex
	exchangeRateSnapshot    *ExchangeRateSnapshot
)

func init() {
	model.SetBillingCNYReferenceProvider(func() (model.BillingCNYReferenceSnapshot, bool) {
		if !BillingCNYReferenceEnabled() {
			return model.BillingCNYReferenceSnapshot{}, false
		}
		snapshot, ok := currentPersistableExchangeRateSnapshot()
		if !ok {
			return model.BillingCNYReferenceSnapshot{}, false
		}
		return model.BillingCNYReferenceSnapshot{
			Rate:       snapshot.Rate,
			Base:       snapshot.Base,
			Target:     snapshot.Target,
			UpdatedAt:  snapshot.UpdatedAt,
			Source:     snapshot.Source,
			Stale:      exchangeRateSnapshotStale(snapshot),
			CapturedAt: common.GetTimestamp(),
		}, true
	})
}

func BillingCNYReferenceEnabled() bool {
	return common.GetEnvOrDefaultBool("BILLING_CNY_REFERENCE_ENABLED", false)
}

func StartBillingCNYReferenceTask() {
	if !BillingCNYReferenceEnabled() {
		return
	}
	billingCNYReferenceOnce.Do(func() {
		interval := exchangeRateRefreshInterval()
		logger.LogInfo(context.Background(), fmt.Sprintf("billing CNY reference exchange-rate task started: interval=%s", interval))
		refreshBillingCNYExchangeRate()
		gopool.Go(func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for range ticker.C {
				refreshBillingCNYExchangeRate()
			}
		})
	})
}

func BuildBillingCNYReferenceFields(displayCurrency string, amountUSD float64) map[string]any {
	if !BillingCNYReferenceEnabled() {
		return nil
	}
	if !strings.EqualFold(strings.TrimSpace(displayCurrency), operation_setting.QuotaDisplayTypeUSD) {
		return nil
	}
	if amountUSD < 0 {
		return nil
	}

	snapshot := CurrentExchangeRateSnapshot()
	amountCNY := roundFloat(amountUSD*snapshot.Rate, billingReferencePrecision)
	return map[string]any{
		"amount_usd":               roundFloat(amountUSD, billingReferencePrecision),
		"amount_cny":               amountCNY,
		"amount_cny_text":          formatCNYAmountText(amountCNY),
		"exchange_rate":            snapshot.Rate,
		"exchange_rate_base":       snapshot.Base,
		"exchange_rate_target":     snapshot.Target,
		"exchange_rate_updated_at": snapshot.UpdatedAt,
		"exchange_rate_source":     snapshot.Source,
		"exchange_rate_stale":      exchangeRateSnapshotStale(snapshot),
	}
}

func FormatBillingCNYAmountText(amountCNY float64) string {
	return formatCNYAmountText(amountCNY)
}

func CurrentExchangeRateSnapshot() ExchangeRateSnapshot {
	exchangeRateMu.RLock()
	snapshot := exchangeRateSnapshot
	exchangeRateMu.RUnlock()
	if snapshot == nil || snapshot.Rate <= 0 {
		return fallbackExchangeRateSnapshot()
	}
	return *snapshot
}

func currentPersistableExchangeRateSnapshot() (ExchangeRateSnapshot, bool) {
	exchangeRateMu.RLock()
	snapshot := exchangeRateSnapshot
	exchangeRateMu.RUnlock()
	if snapshot == nil || snapshot.Rate <= 0 || exchangeRateSnapshotStale(*snapshot) {
		return ExchangeRateSnapshot{}, false
	}
	return *snapshot, true
}

func refreshBillingCNYExchangeRate() {
	if _, err := RefreshBillingCNYExchangeRateNow(); err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("billing CNY reference exchange-rate refresh failed: %v", err))
	}
}

func RefreshBillingCNYExchangeRateNow() (ExchangeRateSnapshot, error) {
	providerURL := strings.TrimSpace(os.Getenv("EXCHANGE_RATE_PROVIDER_URL"))
	if providerURL == "" {
		providerURL = defaultExchangeRateProviderURL
	}
	target := exchangeRateTarget()
	timeout := time.Duration(common.GetEnvOrDefault("EXCHANGE_RATE_HTTP_TIMEOUT_SEC", defaultExchangeRateTimeoutSec)) * time.Second
	if timeout <= 0 {
		timeout = defaultExchangeRateTimeoutSec * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := &http.Client{Timeout: timeout}
	snapshot, err := fetchExchangeRate(ctx, client, providerURL, target)
	if err != nil {
		return ExchangeRateSnapshot{}, err
	}
	setExchangeRateSnapshot(snapshot)
	return snapshot, nil
}

func fetchExchangeRate(ctx context.Context, client *http.Client, providerURL, target string) (ExchangeRateSnapshot, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, providerURL, nil)
	if err != nil {
		return ExchangeRateSnapshot{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 New-API ExchangeRate/1.0")
	if isSinaExchangeRateURL(providerURL) {
		req.Header.Set("Referer", "https://finance.sina.com.cn/")
	}
	resp, err := client.Do(req)
	if err != nil {
		return ExchangeRateSnapshot{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return ExchangeRateSnapshot{}, fmt.Errorf("provider returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ExchangeRateSnapshot{}, err
	}
	if snapshot, err := parseJSONExchangeRate(body, target); err == nil {
		return snapshot, nil
	}
	return parseSinaExchangeRate(body, providerURL, target)
}

func parseJSONExchangeRate(body []byte, target string) (ExchangeRateSnapshot, error) {
	var payload struct {
		Result          string             `json:"result"`
		BaseCode        string             `json:"base_code"`
		Base            string             `json:"base"`
		TimeLastUpdate  int64              `json:"time_last_update_unix"`
		Rates           map[string]float64 `json:"rates"`
		ConversionRates map[string]float64 `json:"conversion_rates"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ExchangeRateSnapshot{}, err
	}
	if payload.Result != "" && !strings.EqualFold(payload.Result, "success") {
		return ExchangeRateSnapshot{}, fmt.Errorf("provider result is %s", payload.Result)
	}

	target = strings.ToUpper(strings.TrimSpace(target))
	rate := payload.Rates[target]
	if rate == 0 && payload.ConversionRates != nil {
		rate = payload.ConversionRates[target]
	}
	if rate <= 0 {
		return ExchangeRateSnapshot{}, fmt.Errorf("target rate %s not found", target)
	}

	base := strings.ToUpper(strings.TrimSpace(payload.BaseCode))
	if base == "" {
		base = strings.ToUpper(strings.TrimSpace(payload.Base))
	}
	if base == "" {
		base = operation_setting.QuotaDisplayTypeUSD
	}

	return ExchangeRateSnapshot{
		Rate:      roundFloat(rate, 6),
		Base:      base,
		Target:    target,
		UpdatedAt: time.Now().Unix(),
		Source:    "provider",
	}, nil
}

func parseSinaExchangeRate(body []byte, providerURL, target string) (ExchangeRateSnapshot, error) {
	if !isSinaExchangeRateURL(providerURL) {
		return ExchangeRateSnapshot{}, fmt.Errorf("unsupported exchange-rate response")
	}
	if !strings.EqualFold(strings.TrimSpace(target), defaultExchangeRateTarget) {
		return ExchangeRateSnapshot{}, fmt.Errorf("sina provider only supports USD to CNY, target=%s", target)
	}

	matches := sinaExchangeRatePattern.FindSubmatch(body)
	if len(matches) < 2 {
		return ExchangeRateSnapshot{}, fmt.Errorf("sina exchange-rate payload not recognized")
	}
	fields := strings.Split(string(matches[1]), ",")
	if len(fields) < 2 {
		return ExchangeRateSnapshot{}, fmt.Errorf("sina exchange-rate payload has no price")
	}
	rate, err := strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
	if err != nil || rate <= 0 {
		return ExchangeRateSnapshot{}, fmt.Errorf("invalid sina exchange rate %q", fields[1])
	}
	return ExchangeRateSnapshot{
		Rate:      roundFloat(rate, 6),
		Base:      operation_setting.QuotaDisplayTypeUSD,
		Target:    defaultExchangeRateTarget,
		UpdatedAt: time.Now().Unix(),
		Source:    "sina",
	}, nil
}

func isSinaExchangeRateURL(providerURL string) bool {
	return strings.Contains(strings.ToLower(providerURL), "hq.sinajs.cn")
}

func setExchangeRateSnapshot(snapshot ExchangeRateSnapshot) {
	exchangeRateMu.Lock()
	exchangeRateSnapshot = &snapshot
	exchangeRateMu.Unlock()
}

func fallbackExchangeRateSnapshot() ExchangeRateSnapshot {
	rate := operation_setting.USDExchangeRate
	if rate <= 0 {
		rate = 7.3
	}
	return ExchangeRateSnapshot{
		Rate:      roundFloat(rate, 6),
		Base:      operation_setting.QuotaDisplayTypeUSD,
		Target:    exchangeRateTarget(),
		UpdatedAt: 0,
		Source:    "fallback",
	}
}

func exchangeRateTarget() string {
	target := strings.ToUpper(strings.TrimSpace(os.Getenv("EXCHANGE_RATE_TARGET")))
	if target == "" {
		target = defaultExchangeRateTarget
	}
	return target
}

func exchangeRateRefreshInterval() time.Duration {
	intervalSec := common.GetEnvOrDefault("EXCHANGE_RATE_REFRESH_INTERVAL_SEC", defaultExchangeRateIntervalSec)
	if intervalSec <= 0 {
		intervalSec = defaultExchangeRateIntervalSec
	}
	return time.Duration(intervalSec) * time.Second
}

func exchangeRateSnapshotStale(snapshot ExchangeRateSnapshot) bool {
	if (snapshot.Source != "provider" && snapshot.Source != "sina") || snapshot.UpdatedAt <= 0 {
		return true
	}
	return time.Since(time.Unix(snapshot.UpdatedAt, 0)) > exchangeRateRefreshInterval()*2
}

func roundFloat(v float64, precision int) float64 {
	pow := math.Pow10(precision)
	return math.Round(v*pow) / pow
}

func formatCNYAmountText(amount float64) string {
	text := strconv.FormatFloat(roundFloat(amount, billingReferencePrecision), 'f', billingReferencePrecision, 64)
	text = strings.TrimRight(text, "0")
	text = strings.TrimRight(text, ".")
	if !strings.Contains(text, ".") {
		text += ".00"
	} else if decimalPlaces := len(text) - strings.LastIndex(text, ".") - 1; decimalPlaces < 2 {
		text += strings.Repeat("0", 2-decimalPlaces)
	}
	return "\u00a5" + text
}
