package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ar2r/ledger-fox/internal/logs"
	"go.uber.org/multierr"
)

type contextPathElement struct {
	key   string
	value any
}

type contextPath []contextPathElement

func (p contextPath) String() string {
	var b strings.Builder
	for _, el := range p {
		b.WriteString(fmt.Sprintf("%v: ", el.value))
	}

	return b.String()
}

type AskFunc func(ctx context.Context, text string) (string, error)

type Context struct {
	std   context.Context
	log   *slog.Logger
	path  contextPath
	askFn AskFunc
}

func NewContext(ctx context.Context, log *slog.Logger) Context {
	return Context{
		std: ctx,
		log: log,
	}
}

func (ctx Context) Deadline() (time.Time, bool) { return ctx.std.Deadline() }
func (ctx Context) Done() <-chan struct{}       { return ctx.std.Done() }
func (ctx Context) Err() error                  { return ctx.std.Err() }
func (ctx Context) Value(key any) any           { return ctx.std.Value(key) }

func (ctx Context) Debug(msg string, args ...any) { ctx.log.Debug(msg, args...) }
func (ctx Context) Info(msg string, args ...any)  { ctx.log.Info(msg, args...) }
func (ctx Context) Warn(msg string, args ...any)  { ctx.log.Warn(msg, args...) }

func (ctx Context) With(key string, value any) Context {
	ctx.log = ctx.log.With(slog.Any(key, value))
	ctx.path = append(ctx.path, contextPathElement{key, value})
	return ctx
}

func (ctx Context) withLog(key string, value any) Context {
	ctx.log = ctx.log.With(slog.Any(key, value))
	return ctx
}

func (ctx Context) WithAskFn(askFn AskFunc) Context {
	ctx.askFn = askFn
	return ctx
}

func (ctx Context) ApplyAskFn(fn func(ctx context.Context, askFn AskFunc) context.Context) Context {
	if ctx.askFn != nil {
		ctx.std = fn(ctx.std, ctx.askFn)
	}

	return ctx
}

func (ctx Context) Error(errs *error, err error, msg string) bool {
	if err == nil {
		return false
	}

	// Причина заворачивается через %w: агрегированный текст несёт
	// исходную ошибку, а errors.Is/As по причине продолжают работать.
	_ = multierr.AppendInto(errs, fmt.Errorf("%s%s: %w", ctx.path.String(), msg, err))
	ctx.log.Error(msg, logs.Error(err))

	return true
}
