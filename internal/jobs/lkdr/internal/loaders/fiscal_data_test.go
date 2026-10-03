package loaders

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/jfk9w-go/based"
	"github.com/jfk9w-go/lkdr-api"

	"github.com/ar2r/ledger-fox/internal/database"
	"github.com/ar2r/ledger-fox/internal/jobs"
	"github.com/ar2r/ledger-fox/internal/jobs/lkdr/internal/entities"
)

type fakeClient struct {
	fiscalDataCalls int
	fiscalDataFn    func(key string) (*lkdr.FiscalDataOut, error)
	receiptFn       func(in *lkdr.ReceiptIn) (*lkdr.ReceiptOut, error)
}

func (f *fakeClient) Receipt(_ context.Context, in *lkdr.ReceiptIn) (*lkdr.ReceiptOut, error) {
	if f.receiptFn != nil {
		return f.receiptFn(in)
	}

	return nil, nil
}

func (f *fakeClient) FiscalData(_ context.Context, in *lkdr.FiscalDataIn) (*lkdr.FiscalDataOut, error) {
	f.fiscalDataCalls++
	return f.fiscalDataFn(in.Key)
}

var errFiscalDataUnavailable = lkdr.Error{
	Code:    "receipt.fiscal.data.unavailable",
	Message: "Сервис получения чеков временно недоступен. Пожалуйста, попробуйте позже. Приносим извинения за доставленные неудобства",
}

func testDB(t *testing.T) database.DB {
	t.Helper()

	db, err := database.Open(context.Background(), database.Params{
		Clock:  based.StandardClock,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Config: database.Config{
			DSN: filepath.Join(t.TempDir(), "test.db"),
		},
		Entities: []any{new(entities.User), new(entities.Brand), new(entities.Receipt), new(entities.FiscalData), new(entities.FiscalDataItem)},
	})

	if err != nil {
		t.Fatal(err)
	}

	return db
}

func seedReceipts(t *testing.T, db database.DB, phone string, keys ...string) {
	t.Helper()

	if err := db.Create(&entities.User{Phone: phone, Name: "test"}).Error; err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, key := range keys {
		receipt := entities.Receipt{
			UserPhone:   phone,
			Key:         key,
			ReceiveDate: entities.DateTime{DateTime: lkdr.DateTime(base.Add(time.Duration(i) * time.Hour))},
		}

		if err := db.Upsert(&receipt).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func testJobsContext() jobs.Context {
	return jobs.NewContext(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestFiscalDataSkipsUnavailableReceiptsUpToLimit(t *testing.T) {
	db := testDB(t)
	phone := "79000000000"
	unavailable := map[string]bool{"k1": true, "k2": true, "k3": true, "k4": true, "k5": true}
	seedReceipts(t, db, phone, "k1", "k2", "k3", "k4", "k5", "k6", "k7")

	client := &fakeClient{fiscalDataFn: func(key string) (*lkdr.FiscalDataOut, error) {
		if unavailable[key] {
			return nil, errFiscalDataUnavailable
		}

		return &lkdr.FiscalDataOut{}, nil
	}}

	batch := fiscalDataBatch{phone: phone, client: client, db: db}
	if _, errs := batch.load(testJobsContext(), 0, 100); errs != nil {
		t.Fatalf("expected no errors, got %v", errs)
	}

	var count int64
	if err := db.Model(new(entities.FiscalData)).Count(&count).Error; err != nil {
		t.Fatal(err)
	}

	if count != 2 {
		t.Fatalf("expected 2 fiscal data records, got %d", count)
	}
}

func TestFiscalDataAbortsWhenUnavailableReceiptsExceedLimit(t *testing.T) {
	db := testDB(t)
	phone := "79000000000"
	seedReceipts(t, db, phone, "k1", "k2", "k3", "k4", "k5", "k6", "k7")

	client := &fakeClient{fiscalDataFn: func(string) (*lkdr.FiscalDataOut, error) {
		return nil, errFiscalDataUnavailable
	}}

	batch := fiscalDataBatch{phone: phone, client: client, db: db}
	_, errs := batch.load(testJobsContext(), 0, 100)

	if errs == nil {
		t.Fatal("expected error when unavailable receipts exceed limit")
	}

	if client.fiscalDataCalls != 6 {
		t.Fatalf("expected 6 api calls (5 skipped + 1 failed), got %d", client.fiscalDataCalls)
	}
}

func TestFiscalDataMaxRequestsLimitsApiCalls(t *testing.T) {
	db := testDB(t)
	phone := "79000000000"
	seedReceipts(t, db, phone, "k1", "k2", "k3", "k4", "k5")

	client := &fakeClient{fiscalDataFn: func(string) (*lkdr.FiscalDataOut, error) {
		return &lkdr.FiscalDataOut{}, nil
	}}

	loader := FiscalData{Phone: phone, BatchSize: 100, MaxRequests: 2}
	if _, errs := loader.Load(testJobsContext(), client, db); errs != nil {
		t.Fatalf("expected limit stop to be a success, got %v", errs)
	}

	if client.fiscalDataCalls != 2 {
		t.Fatalf("expected exactly 2 api calls, got %d", client.fiscalDataCalls)
	}
}

func TestFiscalDataSkipsNotFound(t *testing.T) {
	db := testDB(t)
	phone := "79000000000"
	seedReceipts(t, db, phone, "k1", "k2", "k3")

	client := &fakeClient{fiscalDataFn: func(key string) (*lkdr.FiscalDataOut, error) {
		if key == "k2" {
			return nil, lkdr.Error{
				Code:    lkdr.ReceiptFiscalDataNotFound,
				Message: "no fiscal data",
			}
		}

		return &lkdr.FiscalDataOut{}, nil
	}}

	loader := FiscalData{Phone: phone, BatchSize: 100}
	if _, errs := loader.Load(testJobsContext(), client, db); errs != nil {
		t.Fatalf("expected not-found to be skipped, got %v", errs)
	}

	var count int64
	if err := db.Model(new(entities.FiscalData)).Count(&count).Error; err != nil {
		t.Fatal(err)
	}

	if count != 2 {
		t.Fatalf("expected 2 fiscal data records (k2 skipped), got %d", count)
	}
}

func TestFiscalDataSkipsKnownAPIErrorAndContinues(t *testing.T) {
	db := testDB(t)
	phone := "79000000000"
	seedReceipts(t, db, phone, "k1", "k2")

	var calls int
	client := &fakeClient{fiscalDataFn: func(string) (*lkdr.FiscalDataOut, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("lkdr: Внутреняя ошибка. Попробуйте еще раз")
		}

		return &lkdr.FiscalDataOut{}, nil
	}}

	loader := FiscalData{Phone: phone, BatchSize: 100}
	if _, errs := loader.Load(testJobsContext(), client, db); errs != nil {
		t.Fatalf("expected known internal api error to be skipped, got %v", errs)
	}

	if calls != 2 {
		t.Fatalf("expected loader to continue after known error, got %d calls", calls)
	}
}

func TestFiscalDataAbortsOnGenericError(t *testing.T) {
	db := testDB(t)
	phone := "79000000000"
	seedReceipts(t, db, phone, "k1", "k2", "k3")

	client := &fakeClient{fiscalDataFn: func(string) (*lkdr.FiscalDataOut, error) {
		return nil, errors.New("connection reset by peer")
	}}

	loader := FiscalData{Phone: phone, BatchSize: 100}
	if _, errs := loader.Load(testJobsContext(), client, db); errs == nil {
		t.Fatal("expected generic api error to fail the loader")
	}

	// Остановка на первом чеке: k2 и k3 не запрашиваются.
	if client.fiscalDataCalls != 1 {
		t.Fatalf("expected loader to stop after first error, got %d calls", client.fiscalDataCalls)
	}
}

func TestFiscalDataPaginatesPendingReceipts(t *testing.T) {
	db := testDB(t)
	phone := "79000000000"
	seedReceipts(t, db, phone, "k1", "k2", "k3", "k4")

	client := &fakeClient{fiscalDataFn: func(string) (*lkdr.FiscalDataOut, error) {
		return &lkdr.FiscalDataOut{}, nil
	}}

	loader := FiscalData{Phone: phone, BatchSize: 2}
	if _, errs := loader.Load(testJobsContext(), client, db); errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if client.fiscalDataCalls != 4 {
		t.Fatalf("expected 4 api calls across pages, got %d", client.fiscalDataCalls)
	}

	var count int64
	if err := db.Model(new(entities.FiscalData)).Count(&count).Error; err != nil {
		t.Fatal(err)
	}

	if count != 4 {
		t.Fatalf("expected 4 fiscal data records, got %d", count)
	}
}
