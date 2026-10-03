package lkdr

import (
	"strings"
	"testing"
	"time"

	"github.com/AlekSi/pointer"
	"github.com/jfk9w-go/lkdr-api"
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

func TestFirstSyncDateFor(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	global := pointer.To(lkdr.Date(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)))

	// Настройка пользователя перекрывает глобальный firstSyncFrom.
	value, err := firstSyncDateFor(now, global, Credential{FirstSyncMonths: 3})
	if err != nil {
		t.Fatal(err)
	}

	if got := value.Time().Format("2006-01-02"); got != "2026-07-03" {
		t.Fatalf("expected 2026-07-03 (3 months back), got %s", got)
	}

	// Без настройки пользователя действует глобальный firstSyncFrom.
	value, err = firstSyncDateFor(now, global, Credential{})
	if err != nil {
		t.Fatal(err)
	}

	if value != global {
		t.Fatalf("expected global firstSyncFrom, got %v", value)
	}

	// Ничего не задано — nil: загрузчик возьмёт 12 месяцев по умолчанию.
	if value, err = firstSyncDateFor(now, nil, Credential{}); err != nil || value != nil {
		t.Fatalf("expected nil (loader default), got %v (%v)", value, err)
	}
}

func TestFirstSyncDateForRejectsNegativeMonths(t *testing.T) {
	for _, months := range []int{-1, -12} {
		if _, err := firstSyncDateFor(time.Now(), nil, Credential{FirstSyncMonths: months}); err == nil || !strings.Contains(err.Error(), "firstSyncMonths") {
			t.Fatalf("expected firstSyncMonths error for %d, got %v", months, err)
		}
	}
}
