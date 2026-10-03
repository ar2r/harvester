package loaders

import (
	"context"

	"github.com/jfk9w-go/lkdr-api"
	"gorm.io/gorm/schema"

	"github.com/ar2r/harvester/internal/database"
	"github.com/ar2r/harvester/internal/jobs"
)

type Client interface {
	Receipt(ctx context.Context, in *lkdr.ReceiptIn) (*lkdr.ReceiptOut, error)
	FiscalData(ctx context.Context, in *lkdr.FiscalDataIn) (*lkdr.FiscalDataOut, error)
}

type Interface interface {
	schema.Tabler
	Load(ctx jobs.Context, client Client, db database.DB) ([]Interface, error)
}
