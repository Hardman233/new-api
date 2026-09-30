package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/joho/godotenv"
	"github.com/shopspring/decimal"
)

type options struct {
	Rate         string
	Before       int64
	ServerRegion string
	BatchID      string
	Apply        bool
	ConfirmBatch string
}

func main() {
	opts := parseOptions()
	_ = godotenv.Load(".env")
	common.InitEnv()
	common.IsMasterNode = false
	if err := model.InitDB(); err != nil {
		fatal("connect database: " + err.Error())
	}

	rate, rateSnapshot, err := resolveRate(opts.Rate)
	if err != nil {
		fatal(err.Error())
	}
	if opts.Before <= 0 {
		opts.Before = common.GetTimestamp()
	}
	if opts.BatchID == "" {
		opts.BatchID = fmt.Sprintf("billing-cny-backfill-current-%s", time.Now().Format("20060102-150405"))
	}
	if opts.Apply && opts.ConfirmBatch != opts.BatchID {
		fatal("--confirm-batch must exactly match --batch-id when --apply is used")
	}

	summary, err := model.BackfillBillingCNYReferences(model.BillingCNYBackfillOptions{
		Before:        opts.Before,
		ServerRegion:  opts.ServerRegion,
		Rate:          rate,
		Base:          rateSnapshot.Base,
		Target:        rateSnapshot.Target,
		Source:        rateSnapshot.Source,
		Stale:         false,
		RateUpdatedAt: rateSnapshot.UpdatedAt,
		CapturedAt:    common.GetTimestamp(),
		BatchID:       opts.BatchID,
		Apply:         opts.Apply,
	})
	if err != nil {
		fatal(err.Error())
	}
	fmt.Printf("batch_id=%s\n", opts.BatchID)
	fmt.Printf("server_region=%s before=%d rate=%s source=%s\n", opts.ServerRegion, opts.Before, rate.StringFixed(8), rateSnapshot.Source)
	fmt.Printf("matched=%d updated=%d outbox_reset=%d\n", summary.Matched, summary.Updated, summary.OutboxReset)
	fmt.Printf("old_amount_usd=%s backfilled_amount_cny=%s\n", summary.OldAmountUSD.StringFixed(8), summary.BackfilledCNY.StringFixed(8))
	fmt.Printf("database_changed=%t\n", opts.Apply && summary.Updated > 0)
}

func parseOptions() options {
	var opts options
	flag.StringVar(&opts.Rate, "rate", "", "override rate; when omitted, fetch the current provider rate")
	flag.Int64Var(&opts.Before, "before", 0, "only backfill rows created before this Unix timestamp; default is startup time")
	flag.StringVar(&opts.ServerRegion, "server-region", common.ServerRegionOverseas, "server region to backfill")
	flag.StringVar(&opts.BatchID, "batch-id", "", "backfill batch identifier")
	flag.BoolVar(&opts.Apply, "apply", false, "apply the backfill transaction")
	flag.StringVar(&opts.ConfirmBatch, "confirm-batch", "", "must exactly match --batch-id when --apply is used")
	flag.Parse()
	if strings.TrimSpace(opts.ServerRegion) == "" {
		opts.ServerRegion = common.ServerRegionOverseas
	}
	if !opts.Apply && strings.TrimSpace(opts.ConfirmBatch) != "" {
		fatal("--confirm-batch may only be used with --apply")
	}
	return opts
}

func resolveRate(raw string) (decimal.Decimal, service.ExchangeRateSnapshot, error) {
	if strings.TrimSpace(raw) != "" {
		rate, err := decimal.NewFromString(strings.TrimSpace(raw))
		if err != nil || !rate.GreaterThan(decimal.Zero) {
			return decimal.Zero, service.ExchangeRateSnapshot{}, fmt.Errorf("--rate must be a positive decimal")
		}
		return rate.Round(8), service.ExchangeRateSnapshot{
			Rate:      rate.InexactFloat64(),
			Base:      common.BillingCurrencyUSD,
			Target:    common.BillingCurrencyCNY,
			UpdatedAt: common.GetTimestamp(),
			Source:    "manual_override",
		}, nil
	}
	snapshot, err := service.RefreshBillingCNYExchangeRateNow()
	if err != nil {
		return decimal.Zero, service.ExchangeRateSnapshot{}, fmt.Errorf("fetch current exchange rate: %w", err)
	}
	return decimal.NewFromFloat(snapshot.Rate).Round(8), snapshot, nil
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
