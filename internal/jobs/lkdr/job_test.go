package lkdr

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/jfk9w-go/based"
	"github.com/jfk9w-go/confi"

	"github.com/ar2r/harvester/internal/database"
)

// Схема конфига генерируется на каждом старте приложения; doc-теги парсятся
// как YAML, поэтому «двоеточие + пробел» в описании ломает запуск.
func TestConfigSchemaGenerates(t *testing.T) {
	if _, err := confi.GenerateSchema(Config{}); err != nil {
		t.Fatalf("expected schema to generate, got %v", err)
	}
}

func TestNewJobRejectsNegativeMaxRequests(t *testing.T) {
	// Проверка срабатывает до открытия БД и создания клиентов.
	params := JobParams{
		Config: Config{
			MaxRequests: -1,
			Database: database.Config{
				DSN: filepath.Join(t.TempDir(), "lkdr.db"),
			},
		},
		Clock:  based.StandardClock,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	if _, err := NewJob(context.Background(), params); err == nil {
		t.Fatal("expected error for negative lkdr.maxRequests")
	}
}
