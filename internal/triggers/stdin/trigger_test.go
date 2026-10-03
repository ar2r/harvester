package stdin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jfk9w-go/based"

	"github.com/ar2r/harvester/internal/jobs"
	"github.com/ar2r/harvester/internal/triggers"
)

type fakeRun struct {
	userID string
	jobIDs []string
}

type fakeJobs struct {
	runs   []fakeRun
	failed map[string]bool
}

func (f *fakeJobs) Info() []jobs.Info {
	return nil
}

func (f *fakeJobs) Run(_ jobs.Context, _ time.Time, userID string, jobIDs []string) []jobs.Result {
	f.runs = append(f.runs, fakeRun{userID: userID, jobIDs: jobIDs})

	// Пустой список задач означает «все включённые» — как в реальном реестре.
	if len(jobIDs) == 0 {
		jobIDs = []string{"lkdr"}
	}

	results := make([]jobs.Result, len(jobIDs))
	for i, jobID := range jobIDs {
		result := jobs.Result{JobID: jobID}
		if f.failed[jobID] {
			result.Error = errors.New("job failed")
		}

		results[i] = result
	}

	return results
}

// lineReader имитирует построчный ввод с клавиатуры:
// bufio.Scanner в ask создаётся на каждый вызов и буферизует данные,
// поэтому bulk-Reader (например strings.Reader) не годится.
type lineReader struct {
	lines []string
	next  int
}

func (r *lineReader) Read(p []byte) (int, error) {
	if r.next >= len(r.lines) {
		return 0, io.EOF
	}

	line := r.lines[r.next] + "\n"
	r.next++
	return copy(p, line), nil
}

func TestTriggerAutostartsConfiguredJobs(t *testing.T) {
	var out bytes.Buffer
	registry := &fakeJobs{}

	trigger, err := NewTrigger(TriggerParams{
		Clock:  based.StandardClock,
		User:   "a",
		Reader: strings.NewReader(""),
		Writer: &out,
		Exit:   func(int) {},
	})

	if err != nil {
		t.Fatal(err)
	}

	trigger.Run(testContext(), registry)

	expected := []fakeRun{{userID: "a"}}
	if !reflect.DeepEqual(registry.runs, expected) {
		t.Fatalf("unexpected runs: %+v, expected %+v", registry.runs, expected)
	}

	if !strings.Contains(out.String(), "✔ a/lkdr") {
		t.Fatalf("expected success output, got %q", out.String())
	}
}

func TestTriggerDoesNotAutostartWithoutUser(t *testing.T) {
	var out bytes.Buffer
	registry := &fakeJobs{}

	trigger, err := NewTrigger(TriggerParams{
		Clock:  based.StandardClock,
		Reader: strings.NewReader(""),
		Writer: &out,
		Exit:   func(int) {},
	})

	if err != nil {
		t.Fatal(err)
	}

	trigger.Run(testContext(), registry)

	if len(registry.runs) != 0 {
		t.Fatalf("unexpected runs: %+v", registry.runs)
	}
}

func TestTriggerRunsInteractively(t *testing.T) {
	var out bytes.Buffer
	registry := &fakeJobs{}

	trigger, err := NewTrigger(TriggerParams{
		Clock:  based.StandardClock,
		Reader: &lineReader{lines: []string{"b"}},
		Writer: &out,
		Exit:   func(int) {},
	})

	if err != nil {
		t.Fatal(err)
	}

	trigger.Run(testContext(), registry)

	expected := []fakeRun{{userID: "b"}}
	if !reflect.DeepEqual(registry.runs, expected) {
		t.Fatalf("unexpected runs: %+v, expected %+v", registry.runs, expected)
	}

	if !strings.Contains(out.String(), "Enter user:") || !strings.Contains(out.String(), "✔ b/lkdr") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}

func TestTriggerExitsAfterAutostartInsteadOfAskingForUser(t *testing.T) {
	var out bytes.Buffer
	registry := &fakeJobs{}
	codes := make([]int, 0, 1)

	trigger, err := NewTrigger(TriggerParams{
		Clock:  based.StandardClock,
		User:   "a",
		Reader: &lineReader{lines: []string{"b"}},
		Writer: &out,
		Exit:   func(code int) { codes = append(codes, code) },
	})

	if err != nil {
		t.Fatal(err)
	}

	trigger.Run(testContext(), registry)

	if !reflect.DeepEqual(codes, []int{0}) {
		t.Fatalf("expected exit code 0, got %v", codes)
	}

	if strings.Contains(out.String(), "Enter user:") {
		t.Fatalf("expected no interactive prompt, got %q", out.String())
	}
}

func TestTriggerExitsWithNonZeroCodeAfterFailedAutostart(t *testing.T) {
	var out bytes.Buffer
	registry := &fakeJobs{failed: map[string]bool{"lkdr": true}}
	codes := make([]int, 0, 1)

	trigger, err := NewTrigger(TriggerParams{
		Clock:  based.StandardClock,
		User:   "a",
		Reader: strings.NewReader(""),
		Writer: &out,
		Exit:   func(code int) { codes = append(codes, code) },
	})

	if err != nil {
		t.Fatal(err)
	}

	trigger.Run(testContext(), registry)

	if !reflect.DeepEqual(codes, []int{1}) {
		t.Fatalf("expected exit code 1, got %v", codes)
	}

	if !strings.Contains(out.String(), "✘ a/lkdr") {
		t.Fatalf("expected failure output, got %q", out.String())
	}
}

func TestTriggerJSONOutputOnSuccess(t *testing.T) {
	var out bytes.Buffer
	registry := &fakeJobs{}
	codes := make([]int, 0, 1)

	trigger, err := NewTrigger(TriggerParams{
		Clock:  based.StandardClock,
		User:   "a",
		JSON:   true,
		Reader: strings.NewReader(""),
		Writer: &out,
		Exit:   func(code int) { codes = append(codes, code) },
	})

	if err != nil {
		t.Fatal(err)
	}

	trigger.Run(testContext(), registry)

	if !reflect.DeepEqual(codes, []int{0}) {
		t.Fatalf("expected exit code 0, got %v", codes)
	}

	assertJSONLines(t, out.String(), []map[string]any{
		{"event": "job", "status": "ok", "user": "a", "job": "lkdr"},
	})
}

func TestTriggerJSONOutputOnFailureAndPrompt(t *testing.T) {
	var out bytes.Buffer
	registry := &fakeJobs{failed: map[string]bool{"lkdr": true}}

	// В интерактивном режиме триггер — бесконечный REPL: запрашивается
	// только пользователь, после строки ввода EOF завершает цикл.
	trigger, err := NewTrigger(TriggerParams{
		Clock:  based.StandardClock,
		JSON:   true,
		Reader: &lineReader{lines: []string{"a"}},
		Writer: &out,
		Exit:   func(code int) { t.Fatalf("unexpected exit call: %d", code) },
	})

	if err != nil {
		t.Fatal(err)
	}

	trigger.Run(testContext(), registry)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("expected 3 json lines (prompt + result + repeat prompt before EOF), got %d: %q", len(lines), out.String())
	}

	assertJSONLines(t, lines[0], []map[string]any{
		{"event": "prompt", "message": "Enter user: "},
	})

	var last map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &last); err != nil {
		t.Fatalf("invalid json %q: %v", lines[1], err)
	}

	if last["event"] != "job" || last["status"] != "error" || last["job"] != "lkdr" || last["user"] != "a" {
		t.Fatalf("unexpected result line: %v", last)
	}

	if last["error"] == "" {
		t.Fatal("expected non-empty error field")
	}

	assertJSONLines(t, lines[2], []map[string]any{
		{"event": "prompt", "message": "Enter user: "},
	})
}

func TestTriggerAllUsersRunsEachConfiguredUserInOrder(t *testing.T) {
	var out bytes.Buffer
	registry := &fakeJobs{failed: map[string]bool{"lkdr": true}}
	codes := make([]int, 0, 1)

	// Пользователь b падает первым по алфавиту: остальные всё равно
	// выполняются, итоговый код возврата — 1.
	trigger, err := NewTrigger(TriggerParams{
		Clock:  based.StandardClock,
		User:   AllUsers,
		Users:  []string{"b", "a"},
		Reader: strings.NewReader(""),
		Writer: &out,
		Exit:   func(code int) { codes = append(codes, code) },
	})

	if err != nil {
		t.Fatal(err)
	}

	trigger.Run(testContext(), registry)

	expected := []fakeRun{
		{userID: "b"},
		{userID: "a"},
	}

	// Порядок задан списком Users (main.go передаёт отсортированный).
	if !reflect.DeepEqual(registry.runs, expected) {
		t.Fatalf("unexpected runs: %+v, expected %+v", registry.runs, expected)
	}

	if !reflect.DeepEqual(codes, []int{1}) {
		t.Fatalf("expected exit code 1, got %v", codes)
	}

	if !strings.Contains(out.String(), "✘ b/lkdr") || !strings.Contains(out.String(), "✘ a/lkdr") {
		t.Fatalf("expected per-user failure output, got %q", out.String())
	}
}

func TestTriggerAllUsersWithoutConfiguredUsersFallsBackToSingleRun(t *testing.T) {
	var out bytes.Buffer
	registry := &fakeJobs{}
	codes := make([]int, 0, 1)

	trigger, err := NewTrigger(TriggerParams{
		Clock:  based.StandardClock,
		User:   AllUsers,
		Reader: strings.NewReader(""),
		Writer: &out,
		Exit:   func(code int) { codes = append(codes, code) },
	})

	if err != nil {
		t.Fatal(err)
	}

	trigger.Run(testContext(), registry)

	// Без списка пользователей "all" обрабатывается как обычный ID
	// (в реальном приложении задача ответит ErrJobUnconfigured).
	expected := []fakeRun{{userID: AllUsers}}
	if !reflect.DeepEqual(registry.runs, expected) {
		t.Fatalf("unexpected runs: %+v, expected %+v", registry.runs, expected)
	}

	if !reflect.DeepEqual(codes, []int{0}) {
		t.Fatalf("expected exit code 0, got %v", codes)
	}
}

func assertJSONLines(t *testing.T, out string, expected []map[string]any) {
	t.Helper()

	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != len(expected) {
		t.Fatalf("expected %d json lines, got %d: %q", len(expected), len(lines), out)
	}

	for i, line := range lines {
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("line %d is not valid json (%q): %v", i, line, err)
		}

		if !reflect.DeepEqual(got, expected[i]) {
			t.Fatalf("line %d: expected %v, got %v", i, expected[i], got)
		}
	}
}

func testContext() triggers.Context {
	return triggers.NewContext(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}
