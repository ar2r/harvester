package loaders

import (
	"database/sql"
	"strings"
	"time"

	"github.com/AlekSi/pointer"
	"github.com/jfk9w-go/lkdr-api"

	"github.com/ar2r/harvester/internal/database"
	"github.com/ar2r/harvester/internal/jobs"
	"github.com/ar2r/harvester/internal/jobs/lkdr/internal/entities"
	"github.com/ar2r/harvester/internal/logs"
)

type Receipts struct {
	Phone     string
	BatchSize int
	// MaxRequests — максимум запросов выгрузки (страниц чеков) за запуск;
	// 0 — без ограничения. Остановка по лимиту — не ошибка.
	MaxRequests int
}

func (l Receipts) TableName() string {
	return new(entities.Receipt).TableName()
}

func (l Receipts) Load(ctx jobs.Context, client Client, db database.DB) (_ []Interface, errs error) {
	var newest sql.NullTime
	if err := db.WithContext(ctx).
		Model(new(entities.Receipt)).
		Select("receive_date").
		Where("user_phone = ?", l.Phone).
		Order("receive_date desc").
		Limit(1).
		Scan(&newest).
		Error; ctx.Error(&errs, err, "failed to select latest receive date") {
		return
	}

	var dateFrom *lkdr.Date
	if newest.Valid {
		// Инкремент от самого свежего чека.
		dateFrom = pointer.To(lkdr.Date(newest.Time))
	} else {
		// Limit to last 12 months for the initial sync
		dateFrom = pointer.To(lkdr.Date(time.Now().AddDate(-1, 0, 0)))
	}

	batch := &receiptsBatch{
		phone:       l.Phone,
		client:      client,
		db:          db,
		dateFrom:    dateFrom,
		maxRequests: l.MaxRequests,
	}

	return nil, jobs.Batch[int]{
		Key:  "offset",
		Size: l.BatchSize,
	}.Run(ctx, batch.load)
}

type receiptsBatch struct {
	phone    string
	client   Client
	db       database.DB
	dateFrom *lkdr.Date
	// maxRequests — лимит запросов выгрузки за запуск (0 — без лимита);
	// requests — счётчик сделанных запросов, limited — остановка по лимиту.
	maxRequests int
	requests    int
	limited     bool
}

func (l *receiptsBatch) load(ctx jobs.Context, offset int, limit int) (nextOffset *int, errs error) {
	if l.maxRequests > 0 && l.requests >= l.maxRequests {
		l.limited = true
		return
	}

	in := &lkdr.ReceiptIn{
		DateFrom: l.dateFrom,
		OrderBy:  "RECEIVE_DATE:ASC",
		Offset:   offset,
		Limit:    limit,
	}

	out, err := l.client.Receipt(ctx, in)
	l.requests++
	if err != nil {
		msg := "failed to get data from api"
		if strings.Contains(err.Error(), "Внутреняя ошибка. Попробуйте еще раз") {
			ctx.Warn(msg, logs.Error(err))
			return
		}

		_ = ctx.Error(&errs, err, msg)
		return
	}

	type Entities struct {
		Brands   []entities.Brand   `json:"brands"`
		Receipts []entities.Receipt `json:"receipts"`
	}

	entities, err := database.ToViaJSON[Entities](out)
	if ctx.Error(&errs, err, "entity conversion failed") {
		return
	}

	if brands := entities.Brands; len(brands) > 0 {
		if err := l.db.WithContext(ctx).
			Upsert(brands).
			Error; ctx.Error(&errs, err, "failed to update brands in db") {
			return
		}

		ctx.Debug("updated brands in db", "count", len(brands))
	}

	if receipts := entities.Receipts; len(receipts) > 0 {
		for i := range receipts {
			receipts[i].UserPhone = l.phone
		}

		if err := l.db.WithContext(ctx).
			Upsert(receipts).
			Error; ctx.Error(&errs, err, "failed to update receipts in db") {
			return
		}

		ctx.Debug("updated receipts in db", "count", len(receipts))

		for _, receipt := range receipts {
			ctx.Info("загружен чек",
				"store", receipt.KktOwner,
				"total_sum", receipt.TotalSum,
				"date", receipt.ReceiveDate.Time().Format(time.DateTime))
		}
	}

	if out.HasMore {
		nextOffset = pointer.To(offset + limit)
	}

	return
}
