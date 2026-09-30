package dto

import "testing"

func TestUsageNormalizeTokenAliases(t *testing.T) {
	usage := Usage{
		InputTokens:  1526,
		OutputTokens: 233,
		InputTokensDetails: &InputTokenDetails{
			CachedTokens: 120,
			TextTokens:   18,
			ImageTokens:  1508,
		},
		OutputTokensDetails: &OutputTokenDetails{ImageTokens: 233},
	}

	if !usage.NormalizeTokenAliases() {
		t.Fatal("NormalizeTokenAliases should report a change")
	}
	if usage.PromptTokens != 1526 || usage.CompletionTokens != 233 || usage.TotalTokens != 1759 {
		t.Fatalf("normalized totals = (%d, %d, %d)", usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens)
	}
	if usage.PromptTokensDetails.CachedTokens != 120 || usage.PromptTokensDetails.ImageTokens != 1508 {
		t.Fatalf("normalized input details = %+v", usage.PromptTokensDetails)
	}
	if usage.CompletionTokenDetails.ImageTokens != 233 {
		t.Fatalf("normalized output details = %+v", usage.CompletionTokenDetails)
	}
}

func TestUsageNormalizeTokenAliasesDoesNotDoubleCountCanonicalFields(t *testing.T) {
	usage := Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
		InputTokens:      100,
		OutputTokens:     20,
	}

	usage.NormalizeTokenAliases()
	if usage.PromptTokens != 100 || usage.CompletionTokens != 20 || usage.TotalTokens != 120 {
		t.Fatalf("canonical totals changed: %+v", usage)
	}
}
