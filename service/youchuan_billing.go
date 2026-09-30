package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
)

type youchuanBillingCost struct {
	FastCost  float64 `json:"fastCost"`
	RelaxCost float64 `json:"relaxCost"`
	FeeCost   float64 `json:"feeCost"`
	CostAt    string  `json:"costAt"`
}

// YouchuanFeeCostQuota converts the upstream fee in CNY cents to platform quota.
func YouchuanFeeCostQuota(feeCost, groupRatio float64) (int, bool) {
	if feeCost <= 0 || groupRatio <= 0 || common.QuotaPerUnit <= 0 {
		return 0, false
	}
	quota := decimal.NewFromFloat(feeCost).
		Div(decimal.NewFromInt(100)).
		Mul(decimal.NewFromFloat(groupRatio)).
		Mul(decimal.NewFromFloat(common.QuotaPerUnit)).
		Round(0)
	if !quota.IsPositive() {
		return 0, false
	}
	quotaValue := quota.IntPart()
	maxInt := int64(^uint(0) >> 1)
	if quotaValue <= 0 || quotaValue > maxInt {
		return 0, false
	}
	return int(quotaValue), true
}

// SettleYouchuanTaskQuota settles a successful task from the upstream actual
// CNY cost. Missing or invalid cost data keeps the estimated precharge.
func SettleYouchuanTaskQuota(ctx context.Context, task *model.Task, reason string) {
	if task == nil {
		return
	}
	if common.GetServerBillingCurrency() != common.BillingCurrencyCNY {
		SettleTaskQuotaExact(ctx, task, reason+"; non-CNY billing keeps estimated quota")
		return
	}
	cost, ok := youchuanTaskBillingCost(task)
	if !ok {
		SettleTaskQuotaExact(ctx, task, reason+"; upstream cost unavailable")
		return
	}
	if task.PrivateData.BillingContext == nil {
		SettleTaskQuotaExact(ctx, task, reason+"; billing snapshot unavailable")
		return
	}
	groupRatio := task.PrivateData.BillingContext.GroupRatio
	actualQuota, ok := YouchuanFeeCostQuota(cost.FeeCost, groupRatio)
	if !ok {
		SettleTaskQuotaExact(ctx, task, reason+"; invalid upstream cost")
		return
	}

	extra := map[string]interface{}{
		"usage_source":           model.BillingUsageSourceUpstream,
		"upstream_cost_currency": common.BillingCurrencyCNY,
		"upstream_fee_cost_cent": cost.FeeCost,
		"upstream_cost_cny":      cost.FeeCost / 100,
		"upstream_fast_cost":     cost.FastCost,
		"upstream_relax_cost":    cost.RelaxCost,
		"upstream_cost_at":       strings.TrimSpace(cost.CostAt),
		"billing_group_ratio":    groupRatio,
	}
	recalculateTaskQuotaWithExtra(ctx, task, actualQuota, reason+"; upstream actual cost", extra)
}

func youchuanTaskBillingCost(task *model.Task) (youchuanBillingCost, bool) {
	cost, ok := youchuanTaskReportedBillingCost(task)
	if !ok || cost.FeeCost <= 0 {
		return youchuanBillingCost{}, false
	}
	return cost, true
}

func youchuanTaskReportedBillingCost(task *model.Task) (youchuanBillingCost, bool) {
	if task == nil {
		return youchuanBillingCost{}, false
	}
	if task.PrivateData.Youchuan != nil && len(task.PrivateData.Youchuan.RawCost) > 0 {
		if cost, ok := parseYouchuanBillingCost(task.PrivateData.Youchuan.RawCost); ok {
			return cost, true
		}
	}
	if len(task.Data) == 0 {
		return youchuanBillingCost{}, false
	}
	var response struct {
		Cost json.RawMessage `json:"cost"`
	}
	if err := common.Unmarshal(task.Data, &response); err != nil {
		return youchuanBillingCost{}, false
	}
	return parseYouchuanBillingCost(response.Cost)
}

func parseYouchuanBillingCost(raw json.RawMessage) (youchuanBillingCost, bool) {
	if len(raw) == 0 {
		return youchuanBillingCost{}, false
	}
	var fields map[string]json.RawMessage
	if err := common.Unmarshal(raw, &fields); err != nil {
		return youchuanBillingCost{}, false
	}
	if _, exists := fields["feeCost"]; !exists {
		return youchuanBillingCost{}, false
	}
	var cost youchuanBillingCost
	if err := common.Unmarshal(raw, &cost); err != nil || cost.FeeCost < 0 {
		return youchuanBillingCost{}, false
	}
	return cost, true
}

func youchuanTaskCostLedgerExtra(task *model.Task) map[string]interface{} {
	cost, ok := youchuanTaskReportedBillingCost(task)
	if !ok {
		return nil
	}
	extra := map[string]interface{}{
		"upstream_cost_currency": common.BillingCurrencyCNY,
		"upstream_fee_cost_cent": cost.FeeCost,
		"upstream_cost_cny":      cost.FeeCost / 100,
		"upstream_fast_cost":     cost.FastCost,
		"upstream_relax_cost":    cost.RelaxCost,
		"upstream_cost_at":       strings.TrimSpace(cost.CostAt),
	}
	if task != nil && task.PrivateData.BillingContext != nil {
		extra["billing_group_ratio"] = task.PrivateData.BillingContext.GroupRatio
	}
	return extra
}
