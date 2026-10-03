package lkdr

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/AlekSi/pointer"
	"github.com/jfk9w-go/based"
	"github.com/jfk9w-go/lkdr-api"

	"github.com/ar2r/ledger-fox/internal/database"
	. "github.com/ar2r/ledger-fox/internal/jobs/lkdr/internal/entities"
)

func testStorage(t *testing.T) *storage {
	t.Helper()

	db, err := database.Open(context.Background(), database.Params{
		Clock:  based.StandardClock,
		Logger: discardLogger(),
		Config: database.Config{DSN: filepath.Join(t.TempDir(), "lkdr.db")},
		Entities: []any{
			new(User),
			new(Tokens),
			new(Brand),
			new(Receipt),
			new(FiscalData),
			new(FiscalDataItem),
		},
	})

	if err != nil {
		t.Fatal(err)
	}

	return &storage{db: db}
}

func TestStorageLoadTokensWithoutRecord(t *testing.T) {
	s := testStorage(t)

	// Нет записи — нет токенов и нет ошибки: клиент пойдёт авторизоваться.
	tokens, err := s.LoadTokens(context.Background(), "79000000000")
	if err != nil {
		t.Fatal(err)
	}

	if tokens != nil {
		t.Fatalf("expected nil tokens, got %+v", tokens)
	}
}

func TestStorageUpdateAndLoadTokens(t *testing.T) {
	s := testStorage(t)

	// UTC и запас по срокам: DateTimeTZ сериализуется wall-clock временем
	// без зоны (см. интеграционные тесты).
	now := time.Now().UTC()
	tokens := &lkdr.Tokens{
		Token:                 "access-1",
		TokenExpireIn:         lkdr.DateTimeTZ(now.Add(time.Hour)),
		RefreshToken:          "refresh-1",
		RefreshTokenExpiresIn: pointer.To(lkdr.DateTimeTZ(now.Add(30 * 24 * time.Hour))),
	}

	if err := s.UpdateTokens(context.Background(), "79000000000", tokens); err != nil {
		t.Fatal(err)
	}

	loaded, err := s.LoadTokens(context.Background(), "79000000000")
	if err != nil {
		t.Fatal(err)
	}

	if loaded == nil || loaded.Token != "access-1" || loaded.RefreshToken != "refresh-1" {
		t.Fatalf("unexpected tokens: %+v", loaded)
	}

	// DateTimeTZ сериализуется с точностью до миллисекунд, поэтому
	// сравниваем с усечённым эталоном.
	if !loaded.TokenExpireIn.Time().Equal(tokens.TokenExpireIn.Time().Truncate(time.Millisecond)) {
		t.Fatalf("expected token expiry %s, got %s",
			tokens.TokenExpireIn.Time(), loaded.TokenExpireIn.Time())
	}

	// Повторное сохранение перезаписывает, а не дублирует.
	tokens.Token = "access-2"
	if err := s.UpdateTokens(context.Background(), "79000000000", tokens); err != nil {
		t.Fatal(err)
	}

	loaded, err = s.LoadTokens(context.Background(), "79000000000")
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Token != "access-2" {
		t.Fatalf("expected updated token, got %q", loaded.Token)
	}
}

func TestStorageUpdateTokensNilDeletes(t *testing.T) {
	s := testStorage(t)

	if err := s.UpdateTokens(context.Background(), "79000000000", &lkdr.Tokens{Token: "x"}); err != nil {
		t.Fatal(err)
	}

	// nil — разавторизация: запись удаляется.
	if err := s.UpdateTokens(context.Background(), "79000000000", nil); err != nil {
		t.Fatal(err)
	}

	tokens, err := s.LoadTokens(context.Background(), "79000000000")
	if err != nil {
		t.Fatal(err)
	}

	if tokens != nil {
		t.Fatalf("expected tokens to be deleted, got %+v", tokens)
	}
}
