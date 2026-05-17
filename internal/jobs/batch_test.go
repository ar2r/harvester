package jobs

import (
	"errors"
	"testing"
)

func TestBatchRunsUntilNextValueIsNil(t *testing.T) {
	var calls []int
	errs := Batch[int]{Key: "offset", Size: 10}.Run(testContext(), func(_ Context, value, limit int) (*int, error) {
		calls = append(calls, value)
		if value >= 20 {
			return nil, nil
		}

		next := value + limit
		return &next, nil
	})

	if errs != nil {
		t.Fatalf("unexpected errors: %v", errs)
	}

	if len(calls) != 3 || calls[0] != 0 || calls[1] != 10 || calls[2] != 20 {
		t.Fatalf("unexpected batch values: %v", calls)
	}
}

func TestBatchStopsOnFirstError(t *testing.T) {
	var calls int
	errs := Batch[int]{Key: "offset", Size: 10}.Run(testContext(), func(_ Context, _ int, _ int) (*int, error) {
		calls++
		return nil, errors.New("boom")
	})

	if errs == nil {
		t.Fatal("expected error")
	}

	if calls != 1 {
		t.Fatalf("expected single call, got %d", calls)
	}
}
