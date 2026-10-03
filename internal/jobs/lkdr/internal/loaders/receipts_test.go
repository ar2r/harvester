package loaders

import (
	"errors"
	"testing"
	"time"

	"github.com/AlekSi/pointer"
	"github.com/jfk9w-go/lkdr-api"

	"github.com/jfk9w/hoarder/internal/jobs/lkdr/internal/entities"
)

func TestReceiptsInitialSyncLimitsToTwelveMonths(t *testing.T) {
	db := testDB(t)

	var dateFrom *lkdr.Date
	client := &fakeClient{receiptFn: func(in *lkdr.ReceiptIn) (*lkdr.ReceiptOut, error) {
		dateFrom = in.DateFrom
		return &lkdr.ReceiptOut{}, nil
	}}

	if _, errs := (Receipts{Phone: "79000000000", BatchSize: 100}).Load(testJobsContext(), client, db); errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if dateFrom == nil {
		t.Fatal("expected dateFrom for initial sync")
	}

	expected := time.Now().AddDate(-1, 0, 0)
	if diff := dateFrom.Time().Sub(expected); diff < -time.Hour || diff > time.Hour {
		t.Fatalf("expected initial sync from ~12 months ago (%s), got %s", expected, dateFrom.Time())
	}
}

func TestReceiptsMaxRequestsStopsPagination(t *testing.T) {
	db := testDB(t)
	seedReceipts(t, db, "79000000000", "k1", "k2", "k3")

	page := &lkdr.ReceiptOut{HasMore: true}
	var offsets []int
	client := &fakeClient{receiptFn: func(in *lkdr.ReceiptIn) (*lkdr.ReceiptOut, error) {
		offsets = append(offsets, in.Offset)
		return page, nil
	}}

	loader := Receipts{Phone: "79000000000", BatchSize: 2, MaxRequests: 1}
	if _, errs := loader.Load(testJobsContext(), client, db); errs != nil {
		t.Fatalf("expected limit stop to be a success, got %v", errs)
	}

	if len(offsets) != 1 || offsets[0] != 0 {
		t.Fatalf("expected exactly 1 api call at offset 0, got %v", offsets)
	}
}

func TestReceiptsIncrementalFromLatestReceiveDate(t *testing.T) {
	db := testDB(t)
	// seedReceipts создаёт чеки от базы 2026-01-01 00:00 UTC с шагом в час:
	// k1 — 00:00, k2 — 01:00, k3 — 02:00.
	seedReceipts(t, db, "79000000000", "k1", "k2", "k3")

	var dateFrom *lkdr.Date
	client := &fakeClient{receiptFn: func(in *lkdr.ReceiptIn) (*lkdr.ReceiptOut, error) {
		dateFrom = in.DateFrom
		return &lkdr.ReceiptOut{}, nil
	}}

	if _, errs := (Receipts{Phone: "79000000000", BatchSize: 100}).Load(testJobsContext(), client, db); errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}

	expected := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	if dateFrom == nil || !dateFrom.Time().Equal(expected) {
		t.Fatalf("expected dateFrom %s, got %v", expected, dateFrom)
	}
}

func TestReceiptsSavesBrandsAndReceiptsWithPagination(t *testing.T) {
	db := testDB(t)
	if err := db.Create(&entities.User{Phone: "79000000000", Name: "test"}).Error; err != nil {
		t.Fatal(err)
	}

	var (
		brandId = int64(7)
		base    = time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	)

	receipt := func(key string, hour int) lkdr.Receipt {
		return lkdr.Receipt{
			Key:         key,
			KktOwner:    "Магазин у дома",
			ReceiveDate: lkdr.DateTime(base.Add(time.Duration(hour) * time.Hour)),
			TotalSum:    "100.00",
			BrandId:     pointer.To(brandId),
		}
	}

	pages := []*lkdr.ReceiptOut{
		{
			Brands:   []lkdr.Brand{{Id: brandId, Name: "Магазин у дома"}},
			Receipts: []lkdr.Receipt{receipt("r1", 0), receipt("r2", 1)},
			HasMore:  true,
		},
		{},
	}

	var offsets []int
	client := &fakeClient{receiptFn: func(in *lkdr.ReceiptIn) (*lkdr.ReceiptOut, error) {
		offsets = append(offsets, in.Offset)
		out := pages[0]
		pages = pages[1:]
		return out, nil
	}}

	if _, errs := (Receipts{Phone: "79000000000", BatchSize: 2}).Load(testJobsContext(), client, db); errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(offsets) != 2 || offsets[0] != 0 || offsets[1] != 2 {
		t.Fatalf("unexpected pagination offsets: %v", offsets)
	}

	var brands int64
	if err := db.Model(new(entities.Brand)).Count(&brands).Error; err != nil {
		t.Fatal(err)
	}

	if brands != 1 {
		t.Fatalf("expected 1 brand in db, got %d", brands)
	}

	var receipts []entities.Receipt
	if err := db.Model(new(entities.Receipt)).Find(&receipts).Error; err != nil {
		t.Fatal(err)
	}

	if len(receipts) != 2 {
		t.Fatalf("expected 2 receipts in db, got %d", len(receipts))
	}

	for _, receipt := range receipts {
		if receipt.UserPhone != "79000000000" {
			t.Fatalf("expected user phone on receipt %s, got %q", receipt.Key, receipt.UserPhone)
		}

		if receipt.KktOwner != "Магазин у дома" {
			t.Fatalf("expected store name on receipt %s, got %q", receipt.Key, receipt.KktOwner)
		}
	}
}

func TestReceiptsSkipsKnownAPIError(t *testing.T) {
	db := testDB(t)
	client := &fakeClient{receiptFn: func(*lkdr.ReceiptIn) (*lkdr.ReceiptOut, error) {
		return nil, errors.New("lkdr: Внутреняя ошибка. Попробуйте еще раз")
	}}

	if _, errs := (Receipts{Phone: "79000000000", BatchSize: 100}).Load(testJobsContext(), client, db); errs != nil {
		t.Fatalf("expected known internal api error to be skipped, got: %v", errs)
	}
}
