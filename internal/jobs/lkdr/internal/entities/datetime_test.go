package entities

import (
	"testing"
	"time"

	"github.com/jfk9w-go/lkdr-api"
)

// Контракты DateTime/DateTimeTZ уже ловили продакшн-баг (wall-clock
// сериализация DateTimeTZ сдвигала сроки на смещение локальной зоны),
// поэтому Scan/Value защищены тестами напрямую.

func TestDateTimeValueScanRoundtrip(t *testing.T) {
	moment := time.Date(2026, 9, 10, 13, 45, 30, 0, time.UTC)

	original := DateTime{DateTime: lkdr.DateTime(moment)}

	value, err := original.Value()
	if err != nil {
		t.Fatal(err)
	}

	scanned, ok := value.(time.Time)
	if !ok {
		t.Fatalf("expected time.Time driver value, got %T", value)
	}

	if !scanned.Equal(moment) {
		t.Fatalf("expected %s, got %s", moment, scanned)
	}

	var target DateTime
	if err := target.Scan(scanned); err != nil {
		t.Fatal(err)
	}

	if !target.Time().Equal(moment) {
		t.Fatalf("expected %s after scan, got %s", moment, target.Time())
	}
}

func TestDateTimeTZValueScanRoundtrip(t *testing.T) {
	moment := time.Date(2026, 10, 1, 6, 15, 0, 0, time.UTC)

	original := DateTimeTZ{DateTimeTZ: lkdr.DateTimeTZ(moment)}

	value, err := original.Value()
	if err != nil {
		t.Fatal(err)
	}

	var target DateTimeTZ
	if err := target.Scan(value); err != nil {
		t.Fatal(err)
	}

	if !target.Time().Equal(moment) {
		t.Fatalf("expected %s after scan, got %s", moment, target.Time())
	}
}

func TestDateTimeScanRejectsNonTime(t *testing.T) {
	var (
		dateTime   DateTime
		dateTimeTZ DateTimeTZ
	)

	if err := dateTime.Scan("2026-09-10"); err == nil {
		t.Fatal("expected DateTime.Scan to reject string")
	}

	if err := dateTimeTZ.Scan(int64(1725960000)); err == nil {
		t.Fatal("expected DateTimeTZ.Scan to reject int64")
	}
}

func TestDateTimeGormDataTypes(t *testing.T) {
	if got := (DateTime{}).GormDataType(); got != "time" {
		t.Fatalf("expected DateTime gorm type time, got %q", got)
	}

	if got := (DateTimeTZ{}).GormDataType(); got != "time" {
		t.Fatalf("expected DateTimeTZ gorm type time, got %q", got)
	}
}
