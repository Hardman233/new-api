package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func TestBuildBillingCNYReferenceFieldsDisabled(t *testing.T) {
	t.Setenv("BILLING_CNY_REFERENCE_ENABLED", "false")
	resetExchangeRateSnapshotForTest(t)

	fields := BuildBillingCNYReferenceFields("USD", 5)
	if fields != nil {
		t.Fatalf("expected nil fields when disabled, got %#v", fields)
	}
}

func TestBuildBillingCNYReferenceFieldsSkipsNonUSD(t *testing.T) {
	t.Setenv("BILLING_CNY_REFERENCE_ENABLED", "true")
	resetExchangeRateSnapshotForTest(t)

	fields := BuildBillingCNYReferenceFields("CNY", 5)
	if fields != nil {
		t.Fatalf("expected nil fields for non-USD billing, got %#v", fields)
	}
}

func TestBuildBillingCNYReferenceFieldsUsesCachedRate(t *testing.T) {
	t.Setenv("BILLING_CNY_REFERENCE_ENABLED", "true")
	t.Setenv("EXCHANGE_RATE_REFRESH_INTERVAL_SEC", "600")
	resetExchangeRateSnapshotForTest(t)
	setExchangeRateSnapshot(ExchangeRateSnapshot{
		Rate:      7.3,
		Base:      "USD",
		Target:    "CNY",
		UpdatedAt: time.Now().Unix(),
		Source:    "provider",
	})

	fields := BuildBillingCNYReferenceFields("USD", 5.0094)
	if fields["amount_usd"] != 5.0094 {
		t.Fatalf("amount_usd = %#v, want 5.0094", fields["amount_usd"])
	}
	if fields["amount_cny"] != 36.56862 {
		t.Fatalf("amount_cny = %#v, want 36.56862", fields["amount_cny"])
	}
	if fields["amount_cny_text"] != "\u00a536.56862" {
		t.Fatalf("amount_cny_text = %#v, want \\u00a536.56862", fields["amount_cny_text"])
	}
	if fields["exchange_rate_stale"] != false {
		t.Fatalf("exchange_rate_stale = %#v, want false", fields["exchange_rate_stale"])
	}
}

func TestBuildBillingCNYReferenceFieldsKeepsSmallCNYPrecision(t *testing.T) {
	t.Setenv("BILLING_CNY_REFERENCE_ENABLED", "true")
	t.Setenv("EXCHANGE_RATE_REFRESH_INTERVAL_SEC", "600")
	resetExchangeRateSnapshotForTest(t)
	setExchangeRateSnapshot(ExchangeRateSnapshot{
		Rate:      6.7817,
		Base:      "USD",
		Target:    "CNY",
		UpdatedAt: time.Now().Unix(),
		Source:    "provider",
	})

	fields := BuildBillingCNYReferenceFields("USD", 0.000136)
	if fields["amount_cny"] != 0.00092231 {
		t.Fatalf("amount_cny = %#v, want 0.00092231", fields["amount_cny"])
	}
	if fields["amount_cny_text"] != "\u00a50.00092231" {
		t.Fatalf("amount_cny_text = %#v, want \\u00a50.00092231", fields["amount_cny_text"])
	}
}

func TestBuildBillingCNYReferenceFieldsKeepsEightDigitPrecision(t *testing.T) {
	t.Setenv("BILLING_CNY_REFERENCE_ENABLED", "true")
	t.Setenv("EXCHANGE_RATE_REFRESH_INTERVAL_SEC", "600")
	resetExchangeRateSnapshotForTest(t)
	setExchangeRateSnapshot(ExchangeRateSnapshot{
		Rate:      7.123456,
		Base:      "USD",
		Target:    "CNY",
		UpdatedAt: time.Now().Unix(),
		Source:    "provider",
	})

	fields := BuildBillingCNYReferenceFields("USD", 0.0000007)
	if fields["amount_usd"] != 0.0000007 {
		t.Fatalf("amount_usd = %#v, want 0.0000007", fields["amount_usd"])
	}
	if fields["amount_cny"] != 0.00000499 {
		t.Fatalf("amount_cny = %#v, want 0.00000499", fields["amount_cny"])
	}
	if fields["amount_cny_text"] != "\u00a50.00000499" {
		t.Fatalf("amount_cny_text = %#v, want \\u00a50.00000499", fields["amount_cny_text"])
	}
}

func TestBuildBillingCNYReferenceFieldsFallback(t *testing.T) {
	t.Setenv("BILLING_CNY_REFERENCE_ENABLED", "true")
	resetExchangeRateSnapshotForTest(t)

	oldRate := operation_setting.USDExchangeRate
	operation_setting.USDExchangeRate = 7.2
	t.Cleanup(func() {
		operation_setting.USDExchangeRate = oldRate
	})

	fields := BuildBillingCNYReferenceFields("USD", 2.5)
	if fields["amount_cny"] != 18.0 {
		t.Fatalf("amount_cny = %#v, want 18.0", fields["amount_cny"])
	}
	if fields["exchange_rate_source"] != "fallback" {
		t.Fatalf("exchange_rate_source = %#v, want fallback", fields["exchange_rate_source"])
	}
	if fields["exchange_rate_stale"] != true {
		t.Fatalf("exchange_rate_stale = %#v, want true", fields["exchange_rate_stale"])
	}
}

func TestCurrentPersistableExchangeRateSnapshotRejectsFallbackAndStaleRates(t *testing.T) {
	resetExchangeRateSnapshotForTest(t)

	if snapshot, ok := currentPersistableExchangeRateSnapshot(); ok {
		t.Fatalf("fallback snapshot must not be persisted: %#v", snapshot)
	}

	setExchangeRateSnapshot(ExchangeRateSnapshot{
		Rate:      7.1,
		Base:      "USD",
		Target:    "CNY",
		UpdatedAt: time.Now().Add(-time.Hour).Unix(),
		Source:    "provider",
	})
	if snapshot, ok := currentPersistableExchangeRateSnapshot(); ok {
		t.Fatalf("stale snapshot must not be persisted: %#v", snapshot)
	}

	setExchangeRateSnapshot(ExchangeRateSnapshot{
		Rate:      7.2,
		Base:      "USD",
		Target:    "CNY",
		UpdatedAt: time.Now().Unix(),
		Source:    "provider",
	})
	snapshot, ok := currentPersistableExchangeRateSnapshot()
	if !ok || snapshot.Rate != 7.2 {
		t.Fatalf("fresh provider snapshot = %#v, ok=%v", snapshot, ok)
	}
}

func TestFetchExchangeRate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":"success","base_code":"USD","rates":{"CNY":7.1234567}}`))
	}))
	defer server.Close()

	snapshot, err := fetchExchangeRate(context.Background(), server.Client(), server.URL, "CNY")
	if err != nil {
		t.Fatalf("fetchExchangeRate() error = %v", err)
	}
	if snapshot.Rate != 7.123457 {
		t.Fatalf("Rate = %v, want 7.123457", snapshot.Rate)
	}
	if snapshot.Base != "USD" || snapshot.Target != "CNY" || snapshot.Source != "provider" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if snapshot.UpdatedAt <= 0 {
		t.Fatalf("UpdatedAt = %d, want > 0", snapshot.UpdatedAt)
	}
}

func TestParseSinaExchangeRate(t *testing.T) {
	body := []byte(`var hq_str_fx_susdcny="03:00:02,6.7770000000,6.7795000000,6.7780000000,58.0000000000,6.7830000000,6.7837000000,6.7779000000,6.7780000000,在岸人民币,0.0000,0.0000,0.0058,此行情由新浪财经计算得出,0.0000,0.0000,,2026-07-04";`)

	snapshot, err := parseSinaExchangeRate(body, "https://hq.sinajs.cn/list=fx_susdcny", "CNY")
	if err != nil {
		t.Fatalf("parseSinaExchangeRate() error = %v", err)
	}
	if snapshot.Rate != 6.777 {
		t.Fatalf("Rate = %v, want 6.777", snapshot.Rate)
	}
	if snapshot.Base != "USD" || snapshot.Target != "CNY" || snapshot.Source != "sina" {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
	if snapshot.UpdatedAt <= 0 {
		t.Fatalf("UpdatedAt = %d, want > 0", snapshot.UpdatedAt)
	}
	if exchangeRateSnapshotStale(snapshot) {
		t.Fatalf("sina snapshot should not be stale immediately")
	}
}

func resetExchangeRateSnapshotForTest(t *testing.T) {
	t.Helper()
	exchangeRateMu.Lock()
	oldSnapshot := exchangeRateSnapshot
	exchangeRateSnapshot = nil
	exchangeRateMu.Unlock()
	t.Cleanup(func() {
		exchangeRateMu.Lock()
		exchangeRateSnapshot = oldSnapshot
		exchangeRateMu.Unlock()
	})
}
