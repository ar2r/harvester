package loaders

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/AlekSi/pointer"
	"github.com/jfk9w-go/lkdr-api"
	"gorm.io/gorm"

	"github.com/jfk9w/hoarder/internal/database"
	"github.com/jfk9w/hoarder/internal/jobs"
	"github.com/jfk9w/hoarder/internal/jobs/lkdr/internal/entities"
	"github.com/jfk9w/hoarder/internal/logs"
)

type Receipts struct {
	Phone     string
	BatchSize int
	// FirstSyncFrom — дата, с которой загружать чеки при первой синхронизации
	// (в базе ещё нет чеков этого телефона). nil — последние 12 месяцев.
	FirstSyncFrom *lkdr.Date
	// MinDepthMonths — желаемая глубина истории в месяцах (firstSyncMonths
	// пользователя). 0 — не задана. Если накопленных чеков меньше этого окна,
	// запуск один раз докачивает старые чеки от now-N месяцев и запоминает
	// границу в sync_depths; повторная перекачка того же окна выполняется
	// только при увеличении глубины (backfill MONTHS больше прежней).
	MinDepthMonths int
	// MaxRequests — максимум запросов выгрузки (страниц чеков) за запуск;
	// 0 — без ограничения. Остановка по лимиту — не ошибка: маркер глубины
	// при этом не ставится, и следующий запуск повторит докачку.
	MaxRequests int
}

func (l Receipts) TableName() string {
	return new(entities.Receipt).TableName()
}

func (l Receipts) Load(ctx jobs.Context, client Client, db database.DB) (_ []Interface, errs error) {
	var oldest, newest sql.NullTime
	for _, probe := range []struct {
		order string
		dest  *sql.NullTime
	}{
		{"receive_date asc", &oldest},
		{"receive_date desc", &newest},
	} {
		if err := db.WithContext(ctx).
			Model(new(entities.Receipt)).
			Select("receive_date").
			Where("user_phone = ?", l.Phone).
			Order(probe.order).
			Limit(1).
			Scan(probe.dest).
			Error; ctx.Error(&errs, err, "failed to select date range") {
			return
		}
	}

	now := time.Now()
	var dateFrom *lkdr.Date
	var backfillFrom *time.Time
	switch {
	case newest.Valid:
		// Инкремент от самого свежего чека; при заданной глубине истории
		// докачиваем вглубь, если накопленное короче запрошенного окна
		// и окно глубже уже отработанного.
		dateFrom = pointer.To(lkdr.Date(newest.Time))
		if l.MinDepthMonths > 0 {
			depthFrom := now.AddDate(0, -l.MinDepthMonths, 0)
			if !oldest.Valid || oldest.Time.After(depthFrom) {
				needBackfill := true
				var marker entities.SyncDepth
				err := db.WithContext(ctx).
					Where("user_phone = ?", l.Phone).
					First(&marker).Error
				switch {
				case err == nil:
					// Окно вглубь до этой границы уже перекачано.
					needBackfill = marker.DepthFrom.Time().After(depthFrom)
				case errors.Is(err, gorm.ErrRecordNotFound):
				default:
					if ctx.Error(&errs, err, "failed to select sync depth marker") {
						return
					}
				}

				if needBackfill {
					dateFrom = pointer.To(lkdr.Date(depthFrom))
					backfillFrom = &depthFrom
				}
			}
		}
	case l.FirstSyncFrom != nil:
		dateFrom = l.FirstSyncFrom
	default:
		// Limit to last 12 months for the initial sync
		dateFrom = pointer.To(lkdr.Date(now.AddDate(-1, 0, 0)))
	}

	batch := &receiptsBatch{
		phone:       l.Phone,
		client:      client,
		db:          db,
		dateFrom:    dateFrom,
		maxRequests: l.MaxRequests,
	}
	batchErr := jobs.Batch[int]{
		Key:  "offset",
		Size: l.BatchSize,
	}.Run(ctx, batch.load)
	if batchErr != nil {
		return nil, batchErr
	}

	if batch.limited {
		ctx.Warn("загрузка остановлена по лимиту запросов",
			"max", l.MaxRequests,
			"loaded_pages", batch.requests)
		return
	}

	if backfillFrom != nil {
		marker := &entities.SyncDepth{
			UserPhone: l.Phone,
			DepthFrom: entities.DateTime{DateTime: lkdr.DateTime(*backfillFrom)},
		}

		if err := db.WithContext(ctx).
			Upsert(marker).
			Error; ctx.Error(&errs, err, "failed to save sync depth marker") {
			return
		}
	}

	return
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
