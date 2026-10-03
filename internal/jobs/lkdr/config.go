package lkdr

import (
	"time"

	"github.com/ar2r/ledger-fox/internal/database"
)

type Credential struct {
	Phone     string `yaml:"phone" pattern:"7\\d{10}" doc:"Номер телефона пользователя."`
	DeviceID  string `yaml:"deviceId,omitempty" doc:"Используется для авторизации и обновления токена доступа.\n\nПри отсутствии генерируется автоматически из userAgent и номера телефона.\n\nМожно подсмотреть в браузере при попытке авторизации.\n\nОбратите внимание, что токены доступа привязаны к deviceId. При смене deviceId потребуется авторизоваться заново."`
	UserAgent string `yaml:"userAgent,omitempty" doc:"Используется для авторизации и обновления токена доступа.\n\nМожно подсмотреть в браузере при попытке авторизации." default:"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.0.0 Safari/537.36"`
}

type Config struct {
	Database database.Config `yaml:"database" doc:"Настройка подключения к БД."`
	APIURL   string          `yaml:"apiUrl,omitempty" doc:"Базовый URL API ЛКДР. По умолчанию — сервис ФНС; для интеграционных тестов можно указать адрес мок-сервиса (docker compose up mocklkdr)."`
	// MaxRequests — ограничитель живых прогонов против реального API:
	// максимум запросов выгрузки за один запуск на загрузчик (страниц
	// чеков и фискальных деталей). 0 — без ограничения.
	MaxRequests int                     `yaml:"maxRequests,omitempty" doc:"Максимум запросов выгрузки за один запуск для каждого загрузчика (страница чеков, запрос фискальных данных). 0 — без ограничения. Для живого теста с урезанным объёмом (например --lkdr.maxRequests=2 вместе с --lkdr.batchSize=10)."`
	BatchSize   int                     `yaml:"batchSize,omitempty" default:"1000" doc:"Количество чеков в одном запросе и количество фискальных данных за одно обновление."`
	Timeout     time.Duration           `yaml:"timeout,omitempty" default:"5m" doc:"Таймаут для запросов."`
	Users       map[string][]Credential `yaml:"users" doc:"Пользователи и их авторизационные данные."`
}
