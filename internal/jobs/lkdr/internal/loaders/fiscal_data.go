package loaders

import (
	"strings"

	"github.com/AlekSi/pointer"
	"github.com/jfk9w-go/lkdr-api"

	"github.com/ar2r/ledger-fox/internal/database"
	"github.com/ar2r/ledger-fox/internal/jobs"
	"github.com/ar2r/ledger-fox/internal/jobs/lkdr/internal/entities"
	"github.com/ar2r/ledger-fox/internal/logs"
)

const maxUnavailableFiscalDataSkips = 5

type FiscalData struct {
	Phone     string
	BatchSize int
	// MaxRequests — максимум запросов фискальных данных к API за запуск;
	// 0 — без ограничения. Остановка по лимиту — не ошибка: следующий
	// запуск продолжит с первого чека без деталей.
	MaxRequests int
}

func (l FiscalData) TableName() string {
	return new(entities.FiscalData).TableName()
}

func (l FiscalData) Load(ctx jobs.Context, client Client, db database.DB) (_ []Interface, errs error) {
	return nil, jobs.Batch[int]{
		Key:  "offset",
		Size: l.BatchSize,
	}.Run(ctx, (&fiscalDataBatch{
		phone:       l.Phone,
		client:      client,
		db:          db,
		maxRequests: l.MaxRequests,
	}).load)
}

type fiscalDataBatch struct {
	phone                string
	client               Client
	db                   database.DB
	unavailableDataSkips int
	maxRequests          int
	requests             int
}

func (l *fiscalDataBatch) load(ctx jobs.Context, offset, limit int) (nextOffset *int, errs error) {
	var pendingReceipts []struct {
		Key           string
		HasFiscalData bool
	}

	if err := l.db.WithContext(ctx).
		Model(new(entities.Receipt)).
		Select("receipts.key, fiscal_data.receipt_key is not null as has_fiscal_data").
		Joins("left join fiscal_data on receipts.key = fiscal_data.receipt_key").
		Where("receipts.user_phone = ?", l.phone).
		Order("receive_date asc").
		Offset(offset).
		Limit(limit).
		Scan(&pendingReceipts).
		Error; ctx.Error(&errs, err, "failed to select pending receipts") {
		return
	}

	for _, pendingReceipt := range pendingReceipts {
		if pendingReceipt.HasFiscalData {
			continue
		}

		key := pendingReceipt.Key
		ctx := ctx.With("key", key)

		if l.maxRequests > 0 && l.requests >= l.maxRequests {
			ctx.Warn("загрузка остановлена по лимиту запросов", "max", l.maxRequests)
			return
		}

		out, err := l.client.FiscalData(ctx, &lkdr.FiscalDataIn{Key: key})
		l.requests++
		if err != nil {
			if lkdr.IsDataNotFound(err) {
				ctx.Warn("fiscal data not found", logs.Error(err))
				continue
			}

			msg := "failed to get data from api"
			if strings.Contains(err.Error(), "Внутреняя ошибка. Попробуйте еще раз") {
				ctx.Warn(msg, logs.Error(err))
				continue
			}

			if strings.Contains(err.Error(), "receipt.fiscal.data.unavailable") {
				l.unavailableDataSkips++
				if l.unavailableDataSkips <= maxUnavailableFiscalDataSkips {
					ctx.Warn(msg, logs.Error(err),
						"skipped", l.unavailableDataSkips,
						"limit", maxUnavailableFiscalDataSkips)
					continue
				}

				_ = ctx.Error(&errs, err, "fiscal data unavailable skip limit exceeded")
				return
			}

			_ = ctx.Error(&errs, err, msg)
			return
		}

		entity, err := database.ToViaJSON[entities.FiscalData](out)
		if ctx.Error(&errs, err, "entity conversion failed") {
			return
		}

		entity.ReceiptKey = key

		if err := l.db.WithContext(ctx).
			Upsert(&entity).
			Error; ctx.Error(&errs, err, "failed to update entities in db") {
			return
		}

		ctx.Info("загружены детали чека",
			"store", entity.RetailPlace,
			"total_sum", entity.TotalSum,
			"items_count", len(entity.Items))
	}

	if len(pendingReceipts) == limit {
		nextOffset = pointer.To(offset + limit)
	}

	return
}
