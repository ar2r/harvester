package common

import (
	"runtime"
	"sync"
	"sync/atomic"
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

func TestMultiMutexAllowsSingleHolderAtATime(t *testing.T) {
	var mu MultiMutex[int]

	// TryLock не ждёт освобождения: при контеншне возвращает ошибку
	// (реестр задач превращает её в «already running»). Контракт —
	// взаимное исключение: держатель ключа всегда один.
	var (
		concurrent    atomic.Int32
		maxConcurrent atomic.Int32
		wg            sync.WaitGroup
	)

	const goroutines = 8
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()

			for {
				unlock, err := mu.TryLock(42)
				if err != nil {
					runtime.Gosched()
					continue
				}

				current := concurrent.Add(1)
				for {
					max := maxConcurrent.Load()
					if current <= max || maxConcurrent.CompareAndSwap(max, current) {
						break
					}
				}

				concurrent.Add(-1)
				unlock()
				return
			}
		}()
	}

	wg.Wait()

	if got := maxConcurrent.Load(); got != 1 {
		t.Fatalf("expected single holder at a time, saw %d concurrent", got)
	}
}
