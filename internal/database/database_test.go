package database

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/jfk9w-go/based"
)

type testRow struct {
	Id   int64  `gorm:"primaryKey;autoIncrement:false"`
	Name string
}

// compositeRow — составной первичный ключ с явным именем колонки,
// как у entities.FiscalDataItem (ReceiptKey + DbIdx).
type compositeRow struct {
	A string `gorm:"primaryKey"`
	B int    `gorm:"primaryKey;column:b_id"`
	V string
}

func testParams(t *testing.T) Params {
	t.Helper()

	return Params{
		Clock:  based.StandardClock,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: Config{DSN: filepath.Join(t.TempDir(), "test.db")},
		Entities: []any{
			new(testRow),
			new(compositeRow),
		},
	}
}

func TestOpenValidatesParams(t *testing.T) {
	if _, err := Open(context.Background(), Params{}); err == nil {
		t.Fatal("expected validation error for empty params")
	}
}

func TestOpenMigratesEntities(t *testing.T) {
	db, err := Open(context.Background(), testParams(t))
	if err != nil {
		t.Fatal(err)
	}

	for _, table := range []string{"test_rows", "composite_rows"} {
		var count int64
		if err := db.Table("sqlite_master").
			Where("type = ? AND name = ?", "table", table).
			Count(&count).Error; err != nil {
			t.Fatal(err)
		}

		if count != 1 {
			t.Fatalf("expected table %s to be migrated", table)
		}
	}
}

func TestUpsertInsertsThenUpdatesWithoutDuplicates(t *testing.T) {
	db, err := Open(context.Background(), testParams(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Upsert(&testRow{Id: 1, Name: "first"}).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Upsert(&testRow{Id: 1, Name: "second"}).Error; err != nil {
		t.Fatal(err)
	}

	var rows []testRow
	if err := db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}

	if len(rows) != 1 || rows[0].Name != "second" {
		t.Fatalf("expected single updated row, got %+v", rows)
	}
}

func TestUpsertCompositePrimaryKey(t *testing.T) {
	db, err := Open(context.Background(), testParams(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Upsert(&compositeRow{A: "r1", B: 1, V: "old"}).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Upsert(&compositeRow{A: "r1", B: 1, V: "new"}).Error; err != nil {
		t.Fatal(err)
	}

	if err := db.Upsert(&compositeRow{A: "r1", B: 2, V: "other"}).Error; err != nil {
		t.Fatal(err)
	}

	var rows []compositeRow
	if err := db.Order("b_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}

	if len(rows) != 2 || rows[0].V != "new" || rows[1].V != "other" {
		t.Fatalf("expected composite key upsert to update and add, got %+v", rows)
	}
}

func TestUpsertInBatchesIsIdempotent(t *testing.T) {
	db, err := Open(context.Background(), testParams(t))
	if err != nil {
		t.Fatal(err)
	}

	batch := func(name string) []testRow {
		return []testRow{
			{Id: 1, Name: name},
			{Id: 2, Name: name},
			{Id: 3, Name: name},
		}
	}

	// Повторная загрузка того же окна данных не должна создавать дубли.
	for _, name := range []string{"first-pass", "second-pass"} {
		if err := db.UpsertInBatches(batch(name), 2).Error; err != nil {
			t.Fatal(err)
		}
	}

	var rows []testRow
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}

	if len(rows) != 3 {
		t.Fatalf("expected 3 rows after repeated batches, got %d", len(rows))
	}

	for _, row := range rows {
		if row.Name != "second-pass" {
			t.Fatalf("expected rows to be updated, got %+v", rows)
		}
	}
}

func TestUpsertPanicsOnNonStruct(t *testing.T) {
	db, err := Open(context.Background(), testParams(t))
	if err != nil {
		t.Fatal(err)
	}

	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for non-struct upsert value")
		}
	}()

	_ = db.Upsert(42)
}

func TestTransactionRollsBackOnError(t *testing.T) {
	db, err := Open(context.Background(), testParams(t))
	if err != nil {
		t.Fatal(err)
	}

	err = db.Transaction(func(tx DB) error {
		if err := tx.Upsert(&testRow{Id: 1, Name: "doomed"}).Error; err != nil {
			return err
		}

		return context.Canceled
	})

	if err == nil {
		t.Fatal("expected transaction error to propagate")
	}

	var count int64
	if err := db.Model(new(testRow)).Count(&count).Error; err != nil {
		t.Fatal(err)
	}

	if count != 0 {
		t.Fatalf("expected rollback to drop the row, got %d rows", count)
	}
}
