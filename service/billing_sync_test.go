package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/shopspring/decimal"
)

func TestBuildBillingImportPayloadIncludesCNYSnapshot(t *testing.T) {
	ledger := &model.BillingLedger{
		BillingID:          "task_cny_sync",
		ServerRegion:       "overseas",
		Currency:           "USD",
		ExchangeRate:       decimal.RequireFromString("7.12345600"),
		AmountCNYReference: decimal.RequireFromString("14.24691200"),
		Other:              `{"cny_reference":{"policy":"settlement_snapshot"}}`,
	}

	payload := buildBillingImportPayload(ledger)
	if payload.ExchangeRate != "7.12345600" || payload.AmountCNYReference != "14.24691200" {
		t.Fatalf("exchange_rate=%q amount_cny_reference=%q", payload.ExchangeRate, payload.AmountCNYReference)
	}
	reference, ok := payload.Other["cny_reference"].(map[string]interface{})
	if !ok || reference["policy"] != "settlement_snapshot" {
		t.Fatalf("cny_reference=%v", payload.Other["cny_reference"])
	}
}
