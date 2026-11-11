package shared

import (
	"errors"
	"testing"
)

func TestSanitizeDropsEmptySources(t *testing.T) {
	set, err := Sanitize(Set{
		ID:    "set-1",
		Title: "Test",
		Kind:  "vocab",
		Cards: []Card{
			{ID: "c1", Source: "casa", Target: "Haus"},
			{ID: "c2", Source: "  ", Target: "x"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Cards) != 1 || set.Cards[0].Source != "casa" {
		t.Fatalf("unexpected cards: %+v", set.Cards)
	}
}

func TestPutGetRoundTrip(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Put(Set{
		ID:    "round-trip-1",
		Title: "Verben",
		Kind:  "conjugation",
		Cards: []Card{{ID: "v1", Source: "hablar", Target: "sprechen", POS: "v"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.PublishedAt == "" {
		t.Fatal("missing publishedAt")
	}
	got, err := store.Get("round-trip-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Verben" || got.Kind != "conjugation" || got.Cards[0].Source != "hablar" {
		t.Fatalf("unexpected get: %+v", got)
	}
}

func TestGetMissing(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.Get("nope")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}
