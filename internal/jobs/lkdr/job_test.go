package lkdr

import (
	"strings"
	"testing"
	"time"

	"github.com/AlekSi/pointer"
	"github.com/jfk9w-go/confi"
	"github.com/jfk9w-go/lkdr-api"
)

// Схема конфига генерируется на каждом старте приложения; doc-теги парсятся
// как YAML, поэтому «двоеточие + пробел» в описании ломает запуск.
func TestConfigSchemaGenerates(t *testing.T) {
	if _, err := confi.GenerateSchema(Config{}); err != nil {
		t.Fatalf("expected schema to generate, got %v", err)
	}
}

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

func TestFirstSyncFor(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	globalFrom := pointer.To(lkdr.Date(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)))

	// Настройка пользователя перекрывает глобальный firstSyncFrom.
	value, depth, err := firstSyncFor(now, globalFrom, 0, Credential{FirstSyncMonths: 3})
	if err != nil {
		t.Fatal(err)
	}

	if got := value.Time().Format("2006-01-02"); got != "2026-07-03" {
		t.Fatalf("expected 2026-07-03 (3 months back), got %s", got)
	}

	if depth != 3 {
		t.Fatalf("expected depth 3, got %d", depth)
	}

	// Глобальные месяцы без настройки пользователя: та же семантика.
	value, depth, err = firstSyncFor(now, globalFrom, 5, Credential{})
	if err != nil {
		t.Fatal(err)
	}

	if got := value.Time().Format("2006-01-02"); got != "2026-05-03" {
		t.Fatalf("expected 2026-05-03 (5 months back), got %s", got)
	}

	if depth != 5 {
		t.Fatalf("expected depth 5, got %d", depth)
	}

	// Действует большая из глубин: разовая докачка вглубь не срезается
	// меньшей настройкой пользователя (и наоборот).
	for _, tc := range []struct {
		global, user, expected int
	}{
		{60, 36, 60},
		{36, 60, 60},
		{36, 36, 36},
		{0, 36, 36},
		{36, 0, 36},
	} {
		_, depth, err := firstSyncFor(now, globalFrom, tc.global, Credential{FirstSyncMonths: tc.user})
		if err != nil {
			t.Fatal(err)
		}

		if depth != tc.expected {
			t.Fatalf("max(%d, %d): expected depth %d, got %d", tc.global, tc.user, tc.expected, depth)
		}
	}

	// Без глубин действует глобальный firstSyncFrom, глубина не задана.
	if value, depth, err = firstSyncFor(now, globalFrom, 0, Credential{}); err != nil || value != globalFrom || depth != 0 {
		t.Fatalf("expected global firstSyncFrom and zero depth, got %v, %d (%v)", value, depth, err)
	}

	// Ничего не задано — nil: загрузчик возьмёт 12 месяцев по умолчанию.
	if value, depth, err = firstSyncFor(now, nil, 0, Credential{}); err != nil || value != nil || depth != 0 {
		t.Fatalf("expected nil (loader default) and zero depth, got %v, %d (%v)", value, depth, err)
	}
}

func TestFirstSyncForRejectsNegativeMonths(t *testing.T) {
	for _, months := range []int{-1, -12} {
		if _, _, err := firstSyncFor(time.Now(), nil, 0, Credential{FirstSyncMonths: months}); err == nil || !strings.Contains(err.Error(), "firstSyncMonths") {
			t.Fatalf("expected firstSyncMonths error for %d, got %v", months, err)
		}

		if _, _, err := firstSyncFor(time.Now(), nil, months, Credential{}); err == nil || !strings.Contains(err.Error(), "lkdr.firstSyncMonths") {
			t.Fatalf("expected lkdr.firstSyncMonths error for %d, got %v", months, err)
		}
	}
}
