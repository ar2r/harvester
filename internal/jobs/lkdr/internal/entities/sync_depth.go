package entities

// SyncDepth фиксирует нижнюю границу окна истории, до которой уже
// выполнялась докачка вглубь (firstSyncMonths). Если чеков старше
// запрошенной границы не существует, без маркера каждый запуск считал бы
// историю недостаточно глубокой и перекачивал то же окно заново.
type SyncDepth struct {
	UserPhone string `gorm:"primaryKey"`

	DepthFrom DateTime
}

func (SyncDepth) TableName() string {
	return "sync_depths"
}
