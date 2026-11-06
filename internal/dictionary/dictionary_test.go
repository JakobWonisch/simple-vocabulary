package dictionary

import "testing"

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
