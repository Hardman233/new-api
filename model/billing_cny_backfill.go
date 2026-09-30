package model

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type BillingCNYBackfillOptions struct {
	Before        int64
	ServerRegion  string
	Rate          decimal.Decimal
	Base          string
	Target        string
	Source        string
	Stale         bool
	RateUpdatedAt int64
	CapturedAt    int64
	BatchID       string
	Apply         bool
}

type BillingCNYBackfillSummary struct {
	Matched       int
	Updated       int
	OutboxReset   int
	OldAmountUSD  decimal.Decimal
	BackfilledCNY decimal.Decimal
}

func BackfillBillingCNYReferences(options BillingCNYBackfillOptions) (BillingCNYBackfillSummary, error) {
	var summary BillingCNYBackfillSummary
	if options.Before <= 0 || options.Rate.LessThanOrEqual(decimal.Zero) {
		return summary, fmt.Errorf("before and a positive rate are required")
	}
	if strings.TrimSpace(options.BatchID) == "" {
		return summary, fmt.Errorf("batch_id is required")
	}
	region := strings.TrimSpace(options.ServerRegion)
	if region == "" {
		region = common.ServerRegionOverseas
	}
	if !strings.EqualFold(region, common.ServerRegionOverseas) {
		return summary, fmt.Errorf("CNY backfill only supports overseas billing ledgers")
	}
	if strings.TrimSpace(options.Base) == "" {
		options.Base = common.BillingCurrencyUSD
	}
	if strings.TrimSpace(options.Target) == "" {
		options.Target = common.BillingCurrencyCNY
	}
	if strings.TrimSpace(options.Source) == "" {
		options.Source = "backfill_current_rate"
	}
	if options.CapturedAt <= 0 {
		options.CapturedAt = common.GetTimestamp()
	}

	query := DB.Where("currency = ? AND exchange_rate = 0", common.BillingCurrencyUSD).
		Where("status IN ?", []string{BillingLedgerStatusSettled, BillingLedgerStatusRefunded, BillingLedgerStatusFailed}).
		Where("created_at < ?", options.Before).
		Where("server_region = ?", common.ServerRegionOverseas)
	var rows []BillingLedger
	if err := query.Order("id ASC").Find(&rows).Error; err != nil {
		return summary, err
	}

	summary.Matched = len(rows)
	for _, ledger := range rows {
		amountDecimal := ledger.Amount.Round(8)
		cny := amountDecimal.Mul(options.Rate).Round(8)
		summary.OldAmountUSD = summary.OldAmountUSD.Add(amountDecimal)
		summary.BackfilledCNY = summary.BackfilledCNY.Add(cny)
	}
	if !options.Apply || len(rows) == 0 {
		return summary, nil
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		for _, ledger := range rows {
			amountDecimal := ledger.Amount.Round(8)
			cny := amountDecimal.Mul(options.Rate).Round(8)
			other := map[string]interface{}{}
			if parsed, parseErr := common.StrToMap(ledger.Other); parseErr == nil && parsed != nil {
				other = parsed
			}
			other["cny_reference"] = map[string]interface{}{
				"schema_version":    1,
				"policy":            "backfill_current_rate",
				"backfilled":        true,
				"backfill_batch_id": options.BatchID,
				"amount_usd":        amountDecimal.StringFixed(8),
				"amount_cny":        cny.StringFixed(8),
				"exchange_rate":     options.Rate.Round(8).StringFixed(8),
				"base":              strings.ToUpper(strings.TrimSpace(options.Base)),
				"target":            strings.ToUpper(strings.TrimSpace(options.Target)),
				"rate_updated_at":   options.RateUpdatedAt,
				"captured_at":       options.CapturedAt,
				"source":            options.Source,
				"stale":             options.Stale,
			}
			other["amount_cny_reference_backfilled"] = true

			result := tx.Model(&BillingLedger{}).Where("id = ? AND exchange_rate = 0", ledger.Id).Updates(map[string]interface{}{
				"exchange_rate":        options.Rate.Round(8),
				"amount_cny_reference": cny,
				"other":                common.MapToJsonStr(other),
			})
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				continue
			}
			summary.Updated++

			outboxResult := tx.Model(&BillingSyncOutbox{}).Where("billing_id = ?", ledger.BillingID).Updates(map[string]interface{}{
				"status":          BillingSyncStatusPending,
				"retry_count":     0,
				"next_retry_at":   0,
				"last_error":      "",
				"last_attempt_at": 0,
				"synced_at":       0,
				"updated_at":      options.CapturedAt,
			})
			if outboxResult.Error != nil && !isMissingBillingLedgerTableError(outboxResult.Error) {
				return outboxResult.Error
			}
			summary.OutboxReset += int(outboxResult.RowsAffected)
		}
		return nil
	})
	return summary, err
}
