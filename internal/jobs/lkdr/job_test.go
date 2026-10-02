package lkdr

import (
	"strings"
	"testing"
)

func TestParseFirstSyncFrom(t *testing.T) {
	if value, err := parseFirstSyncFrom(""); err != nil || value != nil {
		t.Fatalf("expected nil for empty value, got %v (%v)", value, err)
	}

	value, err := parseFirstSyncFrom("2020-01-01")
	if err != nil {
		t.Fatal(err)
	}

	if got := value.Time().Format("2006-01-02"); got != "2020-01-01" {
		t.Fatalf("expected 2020-01-01, got %s", got)
	}
}

func TestParseFirstSyncFromRejectsInvalidFormat(t *testing.T) {
	for _, input := range []string{"01.02.2020", "2020/01/01", "2020-13-01", "not-a-date"} {
		if _, err := parseFirstSyncFrom(input); err == nil || !strings.Contains(err.Error(), "firstSyncFrom") {
			t.Fatalf("expected firstSyncFrom error for %q, got %v", input, err)
		}
	}
}
