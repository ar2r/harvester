package lkdr

import (
	"time"

	"github.com/jfk9w/hoarder/internal/database"
)

type Credential struct {
	Phone     string `yaml:"phone" pattern:"7\\d{10}" doc:"Номер телефона пользователя."`
	DeviceID  string `yaml:"deviceId,omitempty" doc:"Используется для авторизации и обновления токена доступа.\n\nПри отсутствии генерируется автоматически из userAgent и номера телефона.\n\nМожно подсмотреть в браузере при попытке авторизации.\n\nОбратите внимание, что токены доступа привязаны к deviceId. При смене deviceId потребуется авторизоваться заново."`
	UserAgent string `yaml:"userAgent,omitempty" doc:"Используется для авторизации и обновления токена доступа.\n\nМожно подсмотреть в браузере при попытке авторизации." default:"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36"`
	// FirstSyncMonths — за сколько последних месяцев скачивать чеки при
	// первой синхронизации телефона. 0 — не задано: используется глобальный
	// firstSyncFrom, а без него — 12 месяцев по умолчанию. Перекрывает
	// глобальную настройку для этого пользователя.
	FirstSyncMonths int `yaml:"firstSyncMonths,omitempty" doc:"За сколько последних месяцев скачивать чеки при первой синхронизации (только этого пользователя; по умолчанию 12). Перекрывает lkdr.firstSyncFrom."`
}

type Config struct {
	Database      database.Config         `yaml:"database" doc:"Настройка подключения к БД."`
	APIURL        string                  `yaml:"apiUrl,omitempty" doc:"Базовый URL API ЛКДР. По умолчанию — сервис ФНС; для интеграционных тестов можно указать адрес мок-сервиса (docker compose up mocklkdr)."`
	FirstSyncFrom string                  `yaml:"firstSyncFrom,omitempty" doc:"Дата (YYYY-MM-DD), с которой загружать чеки при первой синхронизации — когда в базе ещё нет чеков пользователя. По умолчанию — последние 12 месяцев. На повторные инкрементальные запуски не влияет."`
	// FirstSyncMonths — глобальная глубина истории в месяцах: и окно первой
	// синхронизации, и минимальная глубина на инкрементальных запусках.
	// Берётся большая из глобальной и пользовательской настройки — так
	// разовая докачка (make backfill --lkdr.firstSyncMonths=N) не молчит
	// из-за меньшей настройки в config.json.
	FirstSyncMonths int                   `yaml:"firstSyncMonths,omitempty" doc:"Глубина истории в месяцах для всех пользователей — окно первой синхронизации и минимальная глубина на инкрементальных запусках. Действует большая из глобальной и пользовательской настройки firstSyncMonths, перекрывает lkdr.firstSyncFrom. Разовую докачку вглубь запускают make backfill MONTHS=N."`
	BatchSize     int                     `yaml:"batchSize,omitempty" default:"1000" doc:"Количество чеков в одном запросе и количество фискальных данных за одно обновление."`
	Timeout       time.Duration           `yaml:"timeout,omitempty" default:"5m" doc:"Таймаут для запросов."`
	Users         map[string][]Credential `yaml:"users" doc:"Пользователи и их авторизационные данные."`
}
