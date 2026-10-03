package database

import (
	"reflect"
	"testing"
)

func TestToViaJSONTrimsStringsAndDropsEmpty(t *testing.T) {
	type target struct {
		Name    string  `json:"name"`
		Skipped *string `json:"skipped"`
		Value   string  `json:"value"`
	}

	got, err := ToViaJSON[target](map[string]any{
		"name":    "  Пятёрочка  ",
		"skipped": "   ",
		"value":   "ok",
	})

	if err != nil {
		t.Fatal(err)
	}

	if got.Name != "Пятёрочка" {
		t.Fatalf("expected trimmed name, got %q", got.Name)
	}

	if got.Skipped != nil {
		t.Fatalf("expected whitespace-only string to be dropped, got %q", *got.Skipped)
	}

	if got.Value != "ok" {
		t.Fatalf("expected value to survive, got %q", got.Value)
	}
}

func TestToViaJSONIndexesSlicesWithOneBasedDbIdx(t *testing.T) {
	type item struct {
		Name  string `json:"name"`
		DbIdx int    `json:"dbIdx"`
	}

	got, err := ToViaJSON[[]item]([]any{
		map[string]any{"name": "first"},
		map[string]any{"name": "second"},
		map[string]any{"name": "third"},
	})

	if err != nil {
		t.Fatal(err)
	}

	expected := []item{{Name: "first", DbIdx: 1}, {Name: "second", DbIdx: 2}, {Name: "third", DbIdx: 3}}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %+v, got %+v", expected, got)
	}
}

func TestToViaJSONIndexesNestedSlices(t *testing.T) {
	type item struct {
		DbIdx int `json:"dbIdx"`
	}

	type receipt struct {
		Items []item `json:"items"`
	}

	got, err := ToViaJSON[[]receipt]([]any{
		map[string]any{"items": []any{map[string]any{}, map[string]any{}}},
		map[string]any{"items": []any{map[string]any{}}},
	})

	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || len(got[0].Items) != 2 || len(got[1].Items) != 1 {
		t.Fatalf("unexpected shape: %+v", got)
	}

	if got[0].Items[0].DbIdx != 1 || got[0].Items[1].DbIdx != 2 || got[1].Items[0].DbIdx != 1 {
		t.Fatalf("expected per-slice dbIdx numbering, got %+v", got)
	}
}

func TestToViaJSONKeepsNonStringValues(t *testing.T) {
	type target struct {
		Count int     `json:"count"`
		Price float64 `json:"price"`
		Flag  bool    `json:"flag"`
		Null  *int    `json:"null"`
	}

	got, err := ToViaJSON[target](map[string]any{
		"count": 3,
		"price": 12.5,
		"flag":  false,
		"null":  nil,
	})

	if err != nil {
		t.Fatal(err)
	}

	expected := target{Count: 3, Price: 12.5, Flag: false, Null: nil}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %+v, got %+v", expected, got)
	}
}
