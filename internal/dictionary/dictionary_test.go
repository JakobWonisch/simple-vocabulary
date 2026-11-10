package dictionary

import (
	"strings"
	"testing"
)

func TestEnrichWordsUsesDictionaryOnly(t *testing.T) {
	d := &Dictionary{
		forward: map[string]Entry{
			"casa":  {Lemma: "casa", Gender: "f", POS: "n", Translations: []string{"Haus"}, TargetGender: "n"},
			"perro": {Lemma: "perro", Gender: "m", POS: "n", Translations: []string{"Hund"}, TargetGender: "m"},
		},
	}

	cards := d.EnrichWords([]string{"casa", "no-existe", "perro", "casa", "  "})
	if len(cards) != 2 {
		t.Fatalf("got %d cards, want 2: %+v", len(cards), cards)
	}
	if cards[0].Source != "casa" || cards[0].Target != "Haus" || cards[0].SourceArticle != "la" || !cards[0].FoundInDict {
		t.Fatalf("casa card = %+v", cards[0])
	}
	if cards[1].Source != "perro" || cards[1].Target != "Hund" || cards[1].TargetArticle != "der" {
		t.Fatalf("perro card = %+v", cards[1])
	}
}

func TestEnrichWordsKeepsThreeTranslations(t *testing.T) {
	d := &Dictionary{
		forward: map[string]Entry{
			"banco": {
				Lemma:        "banco",
				Gender:       "m",
				POS:          "n",
				Translations: []string{"Bank", " bank ", "Kreditinstitut", "Geldinstitut", "Sitzbank"},
				TargetGender: "f",
			},
		},
	}

	cards := d.EnrichWords([]string{"banco"})
	if len(cards) != 1 {
		t.Fatalf("got %d cards", len(cards))
	}
	got := cards[0].Translations
	want := []string{"Bank", "Kreditinstitut", "Geldinstitut"}
	if cards[0].Target != "Bank" || len(got) != len(want) {
		t.Fatalf("translations %+v", cards[0])
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("translations %v, want %v", got, want)
		}
	}
}

func TestSearchRanksLemmaPrefixAndTranslations(t *testing.T) {
	d := &Dictionary{
		forward: map[string]Entry{
			"casa":   {Lemma: "casa", Gender: "f", POS: "n", Translations: []string{"Haus"}, TargetGender: "n"},
			"casar":  {Lemma: "casar", POS: "v", Translations: []string{"heiraten"}},
			"casi":   {Lemma: "casi", POS: "adv", Translations: []string{"fast"}},
			"perro":  {Lemma: "perro", Gender: "m", POS: "n", Translations: []string{"Hund"}, TargetGender: "m"},
			"hablar": {Lemma: "hablar", POS: "v", Translations: []string{"sprechen", "reden"}},
			"niño":   {Lemma: "niño", Gender: "m", POS: "n", Translations: []string{"Kind"}, TargetGender: "n"},
			"banco":  {Lemma: "banco", Gender: "m", POS: "n", Translations: []string{"Bank", "Kreditinstitut"}, TargetGender: "f"},
		},
	}

	got := d.Search("cas", 10, nil)
	if len(got) != 3 {
		t.Fatalf("cas → %d results: %+v", len(got), got)
	}
	wantOrder := []string{"casa", "casi", "casar"}
	for i, lemma := range wantOrder {
		if got[i].Source != lemma {
			t.Fatalf("order[%d]=%s, want %s (%+v)", i, got[i].Source, lemma, sources(got))
		}
	}

	exact := d.Search("casa", 5, nil)
	if len(exact) == 0 || exact[0].Source != "casa" {
		t.Fatalf("exact casa first: %+v", exact)
	}

	folded := d.Search("nino", 5, nil)
	if len(folded) != 1 || folded[0].Source != "niño" {
		t.Fatalf("diacritic soft match: %+v", folded)
	}

	byDE := d.Search("Kred", 5, nil)
	if len(byDE) != 1 || byDE[0].Source != "banco" {
		t.Fatalf("translation prefix: %+v", byDE)
	}

	limited := d.Search("cas", 2, nil)
	if len(limited) != 2 {
		t.Fatalf("limit: got %d", len(limited))
	}

	verbs := d.Search("ha", 10, func(c Card) bool { return c.POS == "v" })
	if len(verbs) != 1 || verbs[0].Source != "hablar" {
		t.Fatalf("keep filter: %+v", verbs)
	}

	empty := d.Search("  ", 10, nil)
	if empty != nil {
		t.Fatalf("blank query: %+v", empty)
	}
	none := d.Search("zzzz", 10, nil)
	if len(none) != 0 {
		t.Fatalf("no match: %+v", none)
	}
}

func sources(cards []Card) string {
	parts := make([]string, len(cards))
	for i, c := range cards {
		parts[i] = c.Source
	}
	return strings.Join(parts, ",")
}
