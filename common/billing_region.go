package common

import (
	"fmt"
	"strings"
)

const (
	ServerRegionDomestic = "domestic"
	ServerRegionOverseas = "overseas"

	BillingCurrencyCNY = "CNY"
	BillingCurrencyUSD = "USD"
)

func GetServerRegion() string {
	region := strings.ToLower(strings.TrimSpace(GetEnvOrDefaultString("SERVER_REGION", ServerRegionDomestic)))
	switch region {
	case ServerRegionDomestic, ServerRegionOverseas:
		return region
	default:
		return ServerRegionDomestic
	}
}

func GetServerBillingCurrency() string {
	currency := strings.ToUpper(strings.TrimSpace(GetEnvOrDefaultString("SERVER_BILLING_CURRENCY", "")))
	switch currency {
	case BillingCurrencyCNY, BillingCurrencyUSD:
		return currency
	}
	if GetServerRegion() == ServerRegionOverseas {
		return BillingCurrencyUSD
	}
	return BillingCurrencyCNY
}

func BillingDisplayAmountFromQuota(quota int64) float64 {
	if QuotaPerUnit <= 0 {
		return 0
	}
	return float64(quota) / QuotaPerUnit
}

func FormatBillingAmountText(amount float64, currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	switch currency {
	case BillingCurrencyUSD:
		return fmt.Sprintf("$%.6f", amount)
	case BillingCurrencyCNY:
		return fmt.Sprintf("CNY %.6f", amount)
	default:
		if currency == "" {
			return fmt.Sprintf("%.6f", amount)
		}
		return fmt.Sprintf("%s %.6f", currency, amount)
	}
}
