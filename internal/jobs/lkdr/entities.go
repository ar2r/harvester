package lkdr

import (
	. "github.com/ar2r/ledger-fox/internal/jobs/lkdr/internal/entities"
)

var entities = []any{
	new(User),
	new(Tokens),
	new(Brand),
	new(Receipt),
	new(FiscalData),
	new(FiscalDataItem),
}
