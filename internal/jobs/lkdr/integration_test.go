package lkdr

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/jfk9w-go/based"
	"github.com/jfk9w-go/lkdr-api"

	"github.com/jfk9w/hoarder/internal/captcha"
	"github.com/jfk9w/hoarder/internal/database"
	"github.com/jfk9w/hoarder/internal/jobs"
	. "github.com/jfk9w/hoarder/internal/jobs/lkdr/internal/entities"
	"github.com/jfk9w/hoarder/internal/mocklkdr"
)

// Интеграционные тесты гоняют весь конвейер задачи lkdr — реальный клиент
// lkdr-api, загрузчики и SQLite — против мок-сервиса API ФНС
// (internal/mocklkdr). Сеть подменяется redirect-транспортом:
// запросы к https://mco.nalog.ru/api переадресуются на httptest.Server.

const testPhone = "79000000000"

const testPhoneB = "79000000001"

func integrationConfig(t *testing.T) (Config, string) {
	t.Helper()

	dsn := filepath.Join(t.TempDir(), "lkdr.db")
	return Config{
		Database:  database.Config{DSN: dsn},
		BatchSize: 2,
		Timeout:   time.Minute,
		Users: map[string][]Credential{
			"a": {{Phone: testPhone, UserAgent: "integration-test-agent"}},
		},
	}, dsn
}

func openIntegrationDB(t *testing.T, dsn string) database.DB {
	t.Helper()

	db, err := database.Open(context.Background(), database.Params{
		Clock:    based.StandardClock,
		Logger:   discardLogger(),
		Config:   database.Config{DSN: dsn},
		Entities: entities,
	})

	if err != nil {
		t.Fatal(err)
	}

	return db
}

func seedTokens(t *testing.T, dsn, name, phone string) {
	t.Helper()

	db := openIntegrationDB(t, dsn)
	if err := db.Upsert(&User{Name: name, Phone: phone}).Error; err != nil {
		t.Fatal(err)
	}

	// UTC и запас в сроках: DateTimeTZ сериализуется wall-clock временем
	// без зоны, и при круговороте через JSON сроки сдвигаются на смещение
	// локальной зоны процесса.
	now := time.Now().UTC()
	refreshExpiry := DateTimeTZ{DateTimeTZ: lkdr.DateTimeTZ(now.Add(30 * 24 * time.Hour))}
	tokenExpiry := DateTimeTZ{DateTimeTZ: lkdr.DateTimeTZ(now.Add(7 * 24 * time.Hour))}

	if err := db.Upsert(&Tokens{
		UserPhone:             phone,
		RefreshToken:          "seed-refresh-token",
		RefreshTokenExpiresIn: &refreshExpiry,
		Token:                 "seeded-token",
		TokenExpireIn:         tokenExpiry,
	}).Error; err != nil {
		t.Fatal(err)
	}
}

// newMockBackedJob поднимает мок API рядом с задачей и связывает их
// redirect-транспортом. Возвращаемый сервер хранит журнал запросов.
func newMockBackedJob(t *testing.T, cfg Config, captchaSolver captcha.TokenProvider) (*Job, *mocklkdr.Server) {
	t.Helper()

	server := mocklkdr.New()
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	transport, err := NewRedirectTransport(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}

	job, err := NewJob(context.Background(), JobParams{
		Config: cfg,
		Clock:  based.StandardClock,
		Logger: discardLogger(),
		ClientFactory: func(params lkdr.ClientParams) (Client, error) {
			params.Transport = transport
			return defaultClientFactory(params)
		},
		CaptchaSolver: captchaSolver,
	})

	if err != nil {
		t.Fatal(err)
	}

	return job, server
}

func countRequests(requests []mocklkdr.Request, path string) int {
	var count int
	for _, request := range requests {
		if request.Path == path {
			count++
		}
	}

	return count
}

func TestIntegrationJobLoadsReceiptsAndFiscalData(t *testing.T) {
	cfg, dsn := integrationConfig(t)
	job, server := newMockBackedJob(t, cfg, nil)
	seedTokens(t, dsn, "a", testPhone)

	if err := job.Run(jobs.NewContext(context.Background(), discardLogger()), time.Now(), "a"); err != nil {
		t.Fatalf("unexpected errors: %v", err)
	}

	requests := server.Requests()

	// batchSize = 2, в моке 3 чека: первая страница (offset 0, hasMore)
	// и вторая (offset 2).
	if count := countRequests(requests, "/api/v1/receipt"); count != 2 {
		t.Fatalf("expected 2 paginated receipt requests, got %d", count)
	}

	if count := countRequests(requests, "/api/v1/receipt/fiscal_data"); count != 3 {
		t.Fatalf("expected 3 fiscal data requests, got %d", count)
	}

	for _, request := range requests {
		if request.Path == "/api/v1/receipt" && request.Token != "seeded-token" {
			t.Fatalf("expected seeded bearer token on %s, got %q", request.Path, request.Token)
		}
	}

	db := openIntegrationDB(t, dsn)

	var receipts []Receipt
	if err := db.Model(new(Receipt)).Find(&receipts).Error; err != nil {
		t.Fatal(err)
	}

	if len(receipts) != 3 {
		t.Fatalf("expected 3 receipts, got %d", len(receipts))
	}

	for _, receipt := range receipts {
		if receipt.UserPhone != testPhone {
			t.Fatalf("expected user phone on receipt %s, got %q", receipt.Key, receipt.UserPhone)
		}
	}

	var brands int64
	if err := db.Model(new(Brand)).Count(&brands).Error; err != nil {
		t.Fatal(err)
	}

	if brands != 2 {
		t.Fatalf("expected 2 brands, got %d", brands)
	}

	var fiscalData []FiscalData
	if err := db.Model(new(FiscalData)).Preload("Items").Find(&fiscalData).Error; err != nil {
		t.Fatal(err)
	}

	if len(fiscalData) != 3 {
		t.Fatalf("expected 3 fiscal data records, got %d", len(fiscalData))
	}

	expectedItems := map[string]int{"r1": 2, "r2": 1, "r3": 3}
	for _, data := range fiscalData {
		if len(data.Items) != expectedItems[data.ReceiptKey] {
			t.Fatalf("expected %d items for %s, got %d", expectedItems[data.ReceiptKey], data.ReceiptKey, len(data.Items))
		}
	}

	var items int64
	if err := db.Model(new(FiscalDataItem)).Count(&items).Error; err != nil {
		t.Fatal(err)
	}

	if items != 6 {
		t.Fatalf("expected 6 fiscal data items, got %d", items)
	}
}

type fakeCaptchaSolver struct{}

func (fakeCaptchaSolver) GetCaptchaToken(context.Context, string, string, string) (string, error) {
	return "mock-captcha-token", nil
}

func TestIntegrationJobAuthorizesAndPersistsTokens(t *testing.T) {
	cfg, dsn := integrationConfig(t)
	job, server := newMockBackedJob(t, cfg, fakeCaptchaSolver{})

	ctx := jobs.NewContext(context.Background(), discardLogger()).
		WithAskFn(func(context.Context, string) (string, error) { return "1234", nil })

	if err := job.Run(ctx, time.Now(), "a"); err != nil {
		t.Fatalf("unexpected errors: %v", err)
	}

	requests := server.Requests()
	if count := countRequests(requests, "/api/v2/auth/challenge/sms/start"); count != 1 {
		t.Fatalf("expected 1 sms start request, got %d", count)
	}

	if count := countRequests(requests, "/api/v1/auth/challenge/sms/verify"); count != 1 {
		t.Fatalf("expected 1 sms verify request, got %d", count)
	}

	db := openIntegrationDB(t, dsn)

	var tokens Tokens
	if err := db.First(&tokens, testPhone).Error; err != nil {
		t.Fatalf("expected tokens persisted after authorization: %v", err)
	}

	if tokens.Token != mocklkdr.AccessToken {
		t.Fatalf("expected mock access token %q, got %q", mocklkdr.AccessToken, tokens.Token)
	}

	// Повторный запуск не должен требовать повторной SMS-авторизации:
	// прозрачное обновление токена (/v1/auth/token) допустимо.
	server.Reset()
	if err := job.Run(ctx, time.Now(), "a"); err != nil {
		t.Fatalf("unexpected errors on second run: %v", err)
	}

	requests = server.Requests()
	for _, path := range []string{"/api/v2/auth/challenge/sms/start", "/api/v1/auth/challenge/sms/verify"} {
		if count := countRequests(requests, path); count != 0 {
			t.Fatalf("expected no interactive auth requests on second run, got %d for %s", count, path)
		}
	}

	for _, request := range requests {
		if request.Token != mocklkdr.AccessToken {
			t.Fatalf("expected persisted bearer token on %s, got %q", request.Path, request.Token)
		}
	}
}

func TestIntegrationJobIncrementalSyncStartsFromLatestReceipt(t *testing.T) {
	cfg, dsn := integrationConfig(t)
	job, server := newMockBackedJob(t, cfg, nil)
	seedTokens(t, dsn, "a", testPhone)

	ctx := jobs.NewContext(context.Background(), discardLogger())
	if err := job.Run(ctx, time.Now(), "a"); err != nil {
		t.Fatalf("unexpected errors: %v", err)
	}

	server.Reset()
	if err := job.Run(ctx, time.Now(), "a"); err != nil {
		t.Fatalf("unexpected errors on second run: %v", err)
	}

	var receiptRequests int
	var lastReceiptIn lkdr.ReceiptIn

	for _, request := range server.Requests() {
		switch request.Path {
		case "/api/v1/receipt":
			receiptRequests++
			if err := json.Unmarshal([]byte(request.Body), &lastReceiptIn); err != nil {
				t.Fatal(err)
			}
		case "/api/v1/receipt/fiscal_data":
			t.Fatal("expected no fiscal data requests on incremental run")
		}
	}

	if receiptRequests != 1 {
		t.Fatalf("expected single receipt request on incremental run, got %d", receiptRequests)
	}

	if lastReceiptIn.DateFrom == nil {
		t.Fatal("expected dateFrom on incremental request")
	}

	// Самый свежий чек в датасете мока — r3 (2026-09-10); lkdr.Date
	// передаётся датой без времени, поэтому сравниваем только дату
	// (parse и format у lkdr.Date работают в одной timezone).
	if got := lastReceiptIn.DateFrom.Time().Format("2006-01-02"); got != "2026-09-10" {
		t.Fatalf("expected dateFrom 2026-09-10, got %s", got)
	}
}

func TestIntegrationJobFirstSyncFromConfiguredDate(t *testing.T) {
	cfg, dsn := integrationConfig(t)
	cfg.FirstSyncFrom = "2020-01-01"

	job, server := newMockBackedJob(t, cfg, nil)
	seedTokens(t, dsn, "a", testPhone)

	if err := job.Run(jobs.NewContext(context.Background(), discardLogger()), time.Now(), "a"); err != nil {
		t.Fatalf("unexpected errors: %v", err)
	}

	var dateFrom string
	for _, request := range server.Requests() {
		if request.Path != "/api/v1/receipt" {
			continue
		}

		var in lkdr.ReceiptIn
		if err := json.Unmarshal([]byte(request.Body), &in); err != nil {
			t.Fatal(err)
		}

		if in.DateFrom != nil {
			dateFrom = in.DateFrom.Time().Format("2006-01-02")
		}
	}

	if dateFrom != "2020-01-01" {
		t.Fatalf("expected first sync from 2020-01-01, got %q", dateFrom)
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestIntegrationJobBackfillsConfiguredDepth(t *testing.T) {
	cfg, dsn := integrationConfig(t)
	seedTokens(t, dsn, "a", testPhone)

	// Первый прогон наполняет базу тремя чеками сентября 2026.
	job, _ := newMockBackedJob(t, cfg, nil)
	ctx := jobs.NewContext(context.Background(), discardLogger())
	if err := job.Run(ctx, time.Now(), "a"); err != nil {
		t.Fatalf("unexpected errors: %v", err)
	}

	// Глубина истории меняется на 36 месяцев: накопленное (сентябрь 2026)
	// короче окна при now = 2026-10-03 → следующий запуск должен тянуть
	// с 2023-10-03, а не инкрементально с самого свежего чека.
	cfg.Users["a"] = []Credential{{Phone: testPhone, UserAgent: "integration-test-agent", FirstSyncMonths: 36}}

	server := mocklkdr.New()
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	transport, err := NewRedirectTransport(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	deepJob, err := NewJob(context.Background(), JobParams{
		Config: cfg,
		Clock:  based.ClockFunc(func() time.Time { return now }),
		Logger: discardLogger(),
		ClientFactory: func(params lkdr.ClientParams) (Client, error) {
			params.Transport = transport
			return defaultClientFactory(params)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := deepJob.Run(ctx, now, "a"); err != nil {
		t.Fatalf("unexpected errors on backfill run: %v", err)
	}

	if got := lastReceiptDateFrom(t, server).Format("2006-01-02"); got != "2023-10-03" {
		t.Fatalf("expected backfill dateFrom 2023-10-03 (now - 36 months), got %s", got)
	}
}

func TestIntegrationJobPerUserFirstSyncMonths(t *testing.T) {
	cfg, dsn := integrationConfig(t)
	cfg.FirstSyncFrom = "2020-01-01"
	cfg.Users["b"] = []Credential{{Phone: testPhoneB, UserAgent: "integration-test-agent", FirstSyncMonths: 1}}

	seedTokens(t, dsn, "a", testPhone)
	seedTokens(t, dsn, "b", testPhoneB)

	server := mocklkdr.New()
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)

	transport, err := NewRedirectTransport(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}

	// Фиксированные часы: дата первой синхронизации b детерминирована.
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	job, err := NewJob(context.Background(), JobParams{
		Config: cfg,
		Clock:  based.ClockFunc(func() time.Time { return now }),
		Logger: discardLogger(),
		ClientFactory: func(params lkdr.ClientParams) (Client, error) {
			params.Transport = transport
			return defaultClientFactory(params)
		},
	})

	if err != nil {
		t.Fatal(err)
	}

	ctx := jobs.NewContext(context.Background(), discardLogger())

	// Пользователь a без firstSyncMonths — глобальный firstSyncFrom.
	if err := job.Run(ctx, now, "a"); err != nil {
		t.Fatalf("unexpected errors: %v", err)
	}

	if got := lastReceiptDateFrom(t, server).Format("2006-01-02"); got != "2020-01-01" {
		t.Fatalf("expected global firstSyncFrom 2020-01-01 for user a, got %s", got)
	}

	// Пользователь b с firstSyncMonths=1 — месяц назад от часов задачи,
	// глобальная дата перекрыта настройкой пользователя.
	server.Reset()
	if err := job.Run(ctx, now, "b"); err != nil {
		t.Fatalf("unexpected errors on second run: %v", err)
	}

	if got := lastReceiptDateFrom(t, server).Format("2006-01-02"); got != "2026-09-03" {
		t.Fatalf("expected firstSyncMonths=1 date 2026-09-03 for user b, got %s", got)
	}
}

func lastReceiptDateFrom(t *testing.T, server *mocklkdr.Server) time.Time {
	t.Helper()

	var last lkdr.ReceiptIn
	for _, request := range server.Requests() {
		if request.Path != "/api/v1/receipt" {
			continue
		}

		if err := json.Unmarshal([]byte(request.Body), &last); err != nil {
			t.Fatal(err)
		}
	}

	if last.DateFrom == nil {
		t.Fatal("expected dateFrom in receipt requests")
	}

	return last.DateFrom.Time()
}
