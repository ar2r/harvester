package triggers

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"
)

type recordingTrigger struct {
	id      string
	started chan struct{}
	ctxDone <-chan struct{}
}

func (t *recordingTrigger) ID() string { return t.id }

func (t *recordingTrigger) Run(ctx Context, _ Jobs) {
	if t.started != nil {
		close(t.started)
	}

	t.ctxDone = ctx.Done()
	<-ctx.Done()
}

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRegistryRunsTriggerUntilClose(t *testing.T) {
	started := make(chan struct{})
	trigger := &recordingTrigger{id: "test", started: started}

	registry := NewRegistry(testLogger())
	registry.Register(trigger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry.Run(ctx, nil)

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("trigger did not start")
	}

	// Close отменяет контекст триггера и дожидается завершения.
	if err := registry.Close(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected close error: %v", err)
	}

	select {
	case <-trigger.ctxDone:
	default:
		t.Fatal("expected trigger context to be canceled after Close")
	}
}

func TestRegistryCloseJoinsFinishedTrigger(t *testing.T) {
	// Триггер, завершившийся сам, не мешает Close; Join горутины
	// может вернуть context.Canceled в зависимости от тайминга —
	// main.go ошибку Close игнорирует.
	registry := NewRegistry(testLogger())
	registry.Register(fakeTrigger{id: "quick"})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	registry.Run(ctx, nil)

	if err := registry.Close(); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected close error: %v", err)
	}
}

type fakeTrigger struct {
	id string
}

func (t fakeTrigger) ID() string { return t.id }

func (t fakeTrigger) Run(Context, Jobs) {}

func TestContextRoundtripAndDerivation(t *testing.T) {
	log := testLogger()
	ctx := NewContext(context.Background(), log)

	// ContextFrom восстанавливает контекст с тем же логгером из std context.
	restored := ContextFrom(ctx)
	if restored.log != log {
		t.Fatal("expected ContextFrom to restore the same logger")
	}

	// Value делегируется в std context.
	type key struct{}
	std := context.WithValue(context.Background(), key{}, "value")
	if got := NewContext(std, log).Value(key{}); got != "value" {
		t.Fatalf("expected std context value, got %v", got)
	}

	// Job() даёт jobs.Context с тем же логгером и рабочим Done.
	job := ctx.Job()
	jobCtx, cancel := context.WithCancel(job)
	cancel()
	select {
	case <-jobCtx.Done():
	default:
		t.Fatal("expected jobs.Context Done to delegate")
	}
}

func TestContextWithAccumulatesAttrs(t *testing.T) {
	records := make(chan *slog.Record, 4)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	log = slog.New(recordingHandler{next: log.Handler(), records: records})

	ctx := NewContext(context.Background(), log).As("alice").With("job", "lkdr")
	ctx.Info("hello")

	select {
	case record := <-records:
		attrs := attrMap(record)

		if attrs["user"] != "alice" || attrs["job"] != "lkdr" {
			t.Fatalf("expected user and job attrs, got %v", attrs)
		}

		// Контекст, восстановленный из std context, несёт те же атрибуты.
		restored := ContextFrom(ctx)
		restored.Warn("again")

		select {
		case record := <-records:
			attrs := attrMap(record)
			if attrs["user"] != "alice" || attrs["job"] != "lkdr" {
				t.Fatalf("expected attrs to survive ContextFrom, got %v", attrs)
			}
		default:
			t.Fatal("expected record from restored context")
		}

	default:
		t.Fatal("expected log record")
	}
}

func attrMap(record *slog.Record) map[string]string {
	attrs := make(map[string]string)
	record.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.String()
		return true
	})

	return attrs
}

// recordingHandler копит атрибуты With внутри себя: slog кладёт их
// в хендлер, а не в запись, поэтому проверяем через собственную копию.
type recordingHandler struct {
	next    slog.Handler
	records chan *slog.Record
	attrs   []slog.Attr
}

func (h recordingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h recordingHandler) Handle(ctx context.Context, record slog.Record) error {
	if len(h.records) < cap(h.records) {
		for _, attr := range h.attrs {
			record.Add(attr)
		}

		h.records <- &record
	}

	return h.next.Handle(ctx, record)
}

func (h recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return recordingHandler{
		next:    h.next.WithAttrs(attrs),
		records: h.records,
		attrs:   append(slices.Clone(h.attrs), attrs...),
	}
}

func (h recordingHandler) WithGroup(name string) slog.Handler {
	return recordingHandler{next: h.next.WithGroup(name), records: h.records}
}
