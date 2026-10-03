package common

import (
	"sync"
	"testing"
)

// Push внутри одного вызова сохраняет порядок аргументов: загрузчики
// executeLoaders полагаются на то, что Push(Receipts, FiscalData)
// извлекается в этом же порядке. Отдельные вызовы Push — LIFO:
// последний батч извлекается первым.
func TestStackPopsBatchInArgumentOrder(t *testing.T) {
	var s Stack[int]
	s.Push(1, 2, 3)

	for _, expected := range []int{1, 2, 3} {
		value, ok := s.Pop()
		if !ok || value != expected {
			t.Fatalf("expected %d, got %d (ok=%v)", expected, value, ok)
		}
	}
}

func TestStackPopsLastPushedBatchFirst(t *testing.T) {
	var s Stack[int]
	s.Push(1, 2)
	s.Push(3, 4)

	for _, expected := range []int{3, 4, 1, 2} {
		value, ok := s.Pop()
		if !ok || value != expected {
			t.Fatalf("expected %d, got %d (ok=%v)", expected, value, ok)
		}
	}
}

func TestStackPopOnEmpty(t *testing.T) {
	var s Stack[int]
	if value, ok := s.Pop(); ok {
		t.Fatalf("expected no value from empty stack, got %d", value)
	}

	s.Push(1)
	if _, ok := s.Pop(); !ok {
		t.Fatal("expected value after push")
	}

	if value, ok := s.Pop(); ok {
		t.Fatalf("expected empty stack after single pop, got %d", value)
	}
}

func TestMultiMutexSameKeyIsExclusive(t *testing.T) {
	var mu MultiMutex[string]

	unlock, err := mu.TryLock("a")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := mu.TryLock("a"); err == nil {
		t.Fatal("expected second lock of the same key to fail")
	}

	// Другой ключ не блокируется.
	if _, err := mu.TryLock("b"); err != nil {
		t.Fatalf("expected different key to lock: %v", err)
	}

	unlock()
	if _, err := mu.TryLock("a"); err != nil {
		t.Fatalf("expected key to lock after unlock: %v", err)
	}
}

func TestMultiMutexConcurrentSameKey(t *testing.T) {
	var (
		mu      MultiMutex[int]
		counter int
		wg      sync.WaitGroup
	)

	const goroutines = 8
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			unlock, err := mu.TryLock(42)
			if err != nil {
				t.Error(err)
				return
			}

			counter++
			unlock()
		}()
	}

	wg.Wait()

	if counter != goroutines {
		t.Fatalf("expected %d successful locks, got %d", goroutines, counter)
	}
}
