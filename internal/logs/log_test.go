package logs

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestGetReturnsHandlerByEncoding(t *testing.T) {
	jsonLogger := Get(Config{Encoding: JSON})
	if _, ok := jsonLogger.Handler().(*slog.JSONHandler); !ok {
		t.Fatalf("expected JSONHandler, got %T", jsonLogger.Handler())
	}

	textLogger := Get(Config{})
	if _, ok := textLogger.Handler().(*slog.TextHandler); !ok {
		t.Fatalf("expected TextHandler for default encoding, got %T", textLogger.Handler())
	}
}

func TestGetAppliesLevel(t *testing.T) {
	ctx := context.Background()

	debug := Get(Config{Level: slog.LevelDebug})
	if !debug.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("expected debug logs to be enabled at DEBUG level")
	}

	info := Get(Config{})
	if info.Enabled(ctx, slog.LevelDebug) {
		t.Fatal("expected debug logs to be disabled at default INFO level")
	}

	if !info.Enabled(ctx, slog.LevelInfo) {
		t.Fatal("expected info logs to be enabled at default level")
	}
}

func TestShortenTimeKeepsClockOnly(t *testing.T) {
	moment := time.Date(2026, 10, 3, 12, 34, 56, 0, time.UTC)

	attr := shortenTime(nil, slog.Time(slog.TimeKey, moment))
	if got := attr.Value.String(); got != "12:34:56" {
		t.Fatalf("expected short time format, got %q", got)
	}

	// Атрибуты с другими ключами и внутри групп не трогаем.
	other := shortenTime(nil, slog.Time("begin", moment))
	if other.Value.Any().(time.Time) != moment {
		t.Fatalf("expected non-time-key attr to pass through, got %v", other.Value)
	}

	grouped := shortenTime([]string{"g"}, slog.Time(slog.TimeKey, moment))
	if grouped.Value.Any().(time.Time) != moment {
		t.Fatalf("expected grouped time attr to pass through, got %v", grouped.Value)
	}

	nonTime := shortenTime(nil, slog.String(slog.TimeKey, "not a time"))
	if nonTime.Value.String() != "not a time" {
		t.Fatalf("expected non-time value to pass through, got %v", nonTime.Value)
	}
}

func TestAttrHelpers(t *testing.T) {
	if got := Error(context.Canceled); got.Key != "error" || got.Value.String() != "context canceled" {
		t.Fatalf("unexpected error attr: %v", got)
	}

	if got := Database("lkdr"); got.Key != "database" || got.Value.String() != "lkdr" {
		t.Fatalf("unexpected database attr: %v", got)
	}
}
