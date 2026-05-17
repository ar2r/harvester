package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

func testContext() Context {
	return NewContext(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)))
}

type recordingJob struct {
	id  string
	run func(ctx Context, now time.Time, userID string) error
}

func (j *recordingJob) Info() Info {
	return Info{ID: j.id, Description: j.id}
}

func (j *recordingJob) Run(ctx Context, now time.Time, userID string) error {
	if j.run == nil {
		return nil
	}

	return j.run(ctx, now, userID)
}

func TestRegistryRunFiltersJobsByID(t *testing.T) {
	var (
		mu  sync.Mutex
		ran []string
	)

	record := func(id string) func(Context, time.Time, string) error {
		return func(Context, time.Time, string) error {
			mu.Lock()
			defer mu.Unlock()
			ran = append(ran, id)
			return nil
		}
	}

	registry := new(Registry)
	registry.Register(&recordingJob{id: "a", run: record("a")})
	registry.Register(&recordingJob{id: "b", run: record("b")})

	results := registry.Run(testContext(), time.Now(), "user", []string{"a"})
	if len(results) != 1 || results[0].JobID != "a" || results[0].Error != nil {
		t.Fatalf("unexpected results: %+v", results)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(ran) != 1 || ran[0] != "a" {
		t.Fatalf("unexpected runs: %v", ran)
	}
}

func TestRegistryRunAllRunsEveryJob(t *testing.T) {
	for _, jobIDs := range [][]string{nil, {All}} {
		t.Run("filter "+strings.Join(jobIDs, ","), func(t *testing.T) {
			var (
				mu  sync.Mutex
				ran []string
			)

			registry := new(Registry)
			registry.Register(&recordingJob{id: "a", run: func(Context, time.Time, string) error {
				mu.Lock()
				defer mu.Unlock()
				ran = append(ran, "a")
				return nil
			}})
			registry.Register(&recordingJob{id: "b", run: func(Context, time.Time, string) error {
				mu.Lock()
				defer mu.Unlock()
				ran = append(ran, "b")
				return nil
			}})

			results := registry.Run(testContext(), time.Now(), "user", jobIDs)
			if len(results) != 2 {
				t.Fatalf("expected 2 results, got %+v", results)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(ran) != 2 {
				t.Fatalf("expected both jobs to run, got %v", ran)
			}
		})
	}
}

func TestRegistryRunUnknownJobIDRunsNothing(t *testing.T) {
	registry := new(Registry)
	registry.Register(&recordingJob{id: "a"})

	if results := registry.Run(testContext(), time.Now(), "user", []string{"nope"}); len(results) != 0 {
		t.Fatalf("expected no results, got %+v", results)
	}
}

func TestRegistryRunsDifferentJobsInParallel(t *testing.T) {
	var (
		entered = make(chan string, 2)
		release = make(chan struct{})
	)

	go func() {
		<-entered
		<-entered
		close(release)
	}()

	block := func(id string) func(Context, time.Time, string) error {
		return func(Context, time.Time, string) error {
			entered <- id
			select {
			case <-release:
				return nil
			case <-time.After(10 * time.Second):
				return errors.New("parallel run timeout: " + id)
			}
		}
	}

	registry := new(Registry)
	registry.Register(&recordingJob{id: "a", run: block("a")})
	registry.Register(&recordingJob{id: "b", run: block("b")})

	results := registry.Run(testContext(), time.Now(), "user", nil)
	for _, result := range results {
		if result.Error != nil {
			t.Fatalf("job %s did not run in parallel: %v", result.JobID, result.Error)
		}
	}
}

func TestRegistryRejectsConcurrentRunOfSameJobForSameUser(t *testing.T) {
	var (
		started = make(chan struct{})
		release = make(chan struct{})
		once    sync.Once
	)

	registry := new(Registry)
	registry.Register(&recordingJob{id: "a", run: func(Context, time.Time, string) error {
		once.Do(func() { close(started) })
		<-release
		return nil
	}})

	firstDone := make(chan struct{})
	go func() {
		registry.Run(testContext(), time.Now(), "user", nil)
		close(firstDone)
	}()

	<-started

	results := registry.Run(testContext(), time.Now(), "user", nil)
	if len(results) != 1 || results[0].Error == nil {
		t.Fatalf("expected already-running error, got %+v", results)
	}

	if msg := results[0].Error.Error(); !strings.Contains(msg, "already running") {
		t.Fatalf("unexpected error message: %s", msg)
	}

	close(release)
	select {
	case <-firstDone:
	case <-time.After(10 * time.Second):
		t.Fatal("first run did not finish after release")
	}
}

func TestRegistryRunsSameJobForDifferentUsersInParallel(t *testing.T) {
	var (
		entered = make(chan string, 2)
		release = make(chan struct{})
	)

	go func() {
		<-entered
		<-entered
		close(release)
	}()

	registry := new(Registry)
	registry.Register(&recordingJob{id: "a", run: func(_ Context, _ time.Time, userID string) error {
		entered <- userID
		select {
		case <-release:
			return nil
		case <-time.After(10 * time.Second):
			return errors.New("parallel run timeout: " + userID)
		}
	}})

	run := func(userID string) []Result {
		return registry.Run(testContext(), time.Now(), userID, nil)
	}

	results := make(chan []Result, 2)
	go func() { results <- run("user1") }()
	go func() { results <- run("user2") }()

	for i := 0; i < 2; i++ {
		select {
		case got := <-results:
			for _, result := range got {
				if result.Error != nil {
					t.Fatalf("run for a user did not run in parallel: %v", result.Error)
				}
			}
		case <-time.After(10 * time.Second):
			t.Fatal("runs did not complete in time")
		}
	}
}

func TestRegistryDropsUnconfiguredJobsWhenAnyConfigured(t *testing.T) {
	registry := new(Registry)
	registry.Register(&recordingJob{id: "unconfigured", run: func(Context, time.Time, string) error {
		return ErrJobUnconfigured
	}})
	registry.Register(&recordingJob{id: "configured"})

	results := registry.Run(testContext(), time.Now(), "user", nil)
	if len(results) != 1 || results[0].JobID != "configured" || results[0].Error != nil {
		t.Fatalf("expected only the configured job in results, got %+v", results)
	}
}

func TestRegistryReportsUnconfiguredWhenNothingConfigured(t *testing.T) {
	registry := new(Registry)
	registry.Register(&recordingJob{id: "a", run: func(Context, time.Time, string) error {
		return ErrJobUnconfigured
	}})

	results := registry.Run(testContext(), time.Now(), "user", nil)
	if len(results) != 1 || !errors.Is(results[0].Error, ErrJobUnconfigured) {
		t.Fatalf("expected unconfigured error in results, got %+v", results)
	}
}

func TestRegistryInfoAppendsAllAlias(t *testing.T) {
	registry := new(Registry)
	registry.Register(&recordingJob{id: "a"})
	registry.Register(&recordingJob{id: "b"})

	infos := registry.Info()
	if len(infos) != 3 {
		t.Fatalf("expected 3 infos, got %d", len(infos))
	}

	if last := infos[len(infos)-1]; last.ID != All {
		t.Fatalf("expected %q alias last, got %+v", All, last)
	}
}
