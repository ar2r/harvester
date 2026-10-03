package jobs

import (
	"errors"
	"strings"
	"testing"
)

func TestContextErrorPreservesCauseChain(t *testing.T) {
	sentinel := errors.New("root cause")
	ctx := testContext().With("user", "a").With("phone", "79000000000")

	var errs error
	if !ctx.Error(&errs, errors.Join(sentinel), "failed to get data from api") {
		t.Fatal("expected error to be reported")
	}

	if !errors.Is(errs, sentinel) {
		t.Fatalf("cause chain lost: %v", errs)
	}

	msg := errs.Error()
	if !strings.Contains(msg, "a: 79000000000: failed to get data from api") {
		t.Fatalf("aggregated message must carry the context path, got %q", msg)
	}

	if !strings.Contains(msg, "root cause") {
		t.Fatalf("aggregated message must carry the cause, got %q", msg)
	}
}

func TestContextErrorNilIsNoop(t *testing.T) {
	var errs error
	if testContext().Error(&errs, nil, "never happens") {
		t.Fatal("nil error must not be reported")
	}

	if errs != nil {
		t.Fatalf("unexpected error: %v", errs)
	}
}
