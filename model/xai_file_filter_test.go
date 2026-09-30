package model

import (
	"testing"
)

func TestParseXAIFileFilter(t *testing.T) {
	clause, args, err := parseXAIFileFilter(`content_type = "image/png" AND (size_bytes >= 8 OR public_url != null)`)
	if err != nil {
		t.Fatal(err)
	}
	if clause == "" || len(args) != 2 || args[0] != "image/png" || args[1] != int64(8) {
		t.Fatalf("unexpected parsed filter: clause=%q args=%+v", clause, args)
	}

	_, args, err = parseXAIFileFilter(`created_at >= "2026-07-31T00:00:00Z" AND NOT upload_status != "completed"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 1 || args[0] != int64(1785456000) {
		t.Fatalf("unexpected timestamp args: %+v", args)
	}
}

func TestParseXAIFileFilterRejectsInvalidInput(t *testing.T) {
	invalid := []string{
		`filename LIKE "%.png"`,
		`unknown = "value"`,
		`size_bytes = "large"`,
		`public_url > null`,
		`filename = "unterminated`,
	}
	for _, input := range invalid {
		if _, _, err := parseXAIFileFilter(input); err == nil {
			t.Fatalf("expected %q to fail", input)
		}
	}
}
