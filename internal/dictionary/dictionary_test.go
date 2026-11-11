package dictionary

import (
	"strings"
	"testing"
)

func TestEnrichWordsUsesDictionaryOnly(t *testing.T) {
	d := &Dictionary{
		forward: map[string]Entry{
			"casa":  {Lemma: "casa", Gender: "f", POS: PosTags{"n"}, Translations: []string{"Haus"}, TargetGender: "n"},
			"perro": {Lemma: "perro", Gender: "m", POS: PosTags{"n"}, Translations: []string{"Hund"}, TargetGender: "m"},
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
				POS:          PosTags{"n"},
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

func TestEnrichLemmaGlossesPromotesMatchingDictSense(t *testing.T) {
	d := &Dictionary{
		forward: map[string]Entry{
			"casa": {
				Lemma:        "casa",
				Gender:       "f",
				POS:          PosTags{"n"},
				Translations: []string{"Haus", "Gebäude", "Heim"},
				TargetGender: "n",
			},
			"querer": {
				Lemma:        "querer",
				POS:          PosTags{"v"},
				Translations: []string{"lieben", "möchten", "wollen"},
			},
		},
	}

	cards := d.EnrichLemmaGlosses([]LemmaGloss{
		{Lemma: "casa", Gloss: "Gebäude"},
		{Lemma: "querer", Gloss: "WOLLEN"},
		{Lemma: "casa", Gloss: "invented"},
	})
	if len(cards) != 2 {
		t.Fatalf("got %d cards", len(cards))
	}
	if cards[0].Target != "Gebäude" || cards[0].Translations[0] != "Gebäude" || cards[0].Translations[1] != "Haus" {
		t.Fatalf("casa promoted: %+v", cards[0])
	}
	if cards[1].Target != "wollen" || cards[1].Matched != "wollen" {
		t.Fatalf("querer case-insensitive match: %+v", cards[1])
	}

	miss := d.EnrichLemmaGlosses([]LemmaGloss{{Lemma: "casa", Gloss: "Villa"}})
	if len(miss) != 1 || miss[0].Target != "Haus" || miss[0].Matched != "" {
		t.Fatalf("unmatched gloss must keep FreeDict order: %+v", miss[0])
	}
}

func TestArticleStressedAFeminines(t *testing.T) {
	n := PosTags{"n"}
	if got := Article("es", "f", n, "agua"); got != "el" {
		t.Fatalf("agua: %q", got)
	}
	if got := Article("es", "f", n, "alma"); got != "el" {
		t.Fatalf("alma: %q", got)
	}
	if got := Article("es", "f", n, "águila"); got != "el" {
		t.Fatalf("águila: %q", got)
	}
	if got := Article("es", "f", n, "casa"); got != "la" {
		t.Fatalf("casa: %q", got)
	}
	if got := Article("es", "f", n, "a"); got != "la" {
		t.Fatalf("letter a: %q", got)
	}
	if got := Article("es", "f", PosTags{"v"}, "agua"); got != "" {
		t.Fatalf("verb POS should skip article: %q", got)
	}
	if got := Article("es", "f", PosTags{"n", "v"}, "agua"); got != "el" {
		t.Fatalf("dual POS noun+verb still articles: %q", got)
	}
}

func TestPosTagsAllowsVerb(t *testing.T) {
	if !(PosTags{"n", "v"}).AllowsVerb() {
		t.Fatal("n+v should allow verb")
	}
	if (PosTags{"n"}).AllowsVerb() {
		t.Fatal("noun-only should not allow verb")
	}
	if !PosTags(nil).AllowsVerb() {
		t.Fatal("untagged should allow verb")
	}
	card := Card{POS: "n", posTags: PosTags{"n", "v"}}
	if !card.AllowsVerb() || !card.HasPOS("v") || card.POS != "n" {
		t.Fatalf("card dual POS: %+v allows=%v", card, card.AllowsVerb())
	}
}

func TestSearchRanksLemmaPrefixAndTranslations(t *testing.T) {
	d := &Dictionary{
		forward: map[string]Entry{
			"casa":      {Lemma: "casa", Gender: "f", POS: PosTags{"n"}, Translations: []string{"Haus"}, TargetGender: "n"},
			"casar":     {Lemma: "casar", POS: PosTags{"v"}, Translations: []string{"heiraten"}},
			"casi":      {Lemma: "casi", POS: PosTags{"adv"}, Translations: []string{"fast"}},
			"perro":     {Lemma: "perro", Gender: "m", POS: PosTags{"n"}, Translations: []string{"Hund"}, TargetGender: "m"},
			"hablar":    {Lemma: "hablar", POS: PosTags{"v"}, Translations: []string{"sprechen", "reden"}},
			"niño":      {Lemma: "niño", Gender: "m", POS: PosTags{"n"}, Translations: []string{"Kind"}, TargetGender: "n"},
			"banco":     {Lemma: "banco", Gender: "m", POS: PosTags{"n"}, Translations: []string{"Bank", "Kreditinstitut"}, TargetGender: "f"},
			"hundir":    {Lemma: "hundir", POS: PosTags{"v"}, Translations: []string{"versenken"}},
			"haber":     {Lemma: "haber", POS: PosTags{"v"}, Translations: []string{"haben"}},
			"hallar":    {Lemma: "hallar", POS: PosTags{"v"}, Translations: []string{"finden"}},
			"querer":    {Lemma: "querer", POS: PosTags{"v"}, Translations: []string{"lieben", "möchten", "mögen", "wollen", "wünschen"}},
			"pretender": {Lemma: "pretender", POS: PosTags{"v"}, Translations: []string{"behaupten", "wollen", "beabsichtigen"}},
			"saltar":    {Lemma: "saltar", POS: PosTags{"v"}, Translations: []string{"springen"}},
			"brincar":   {Lemma: "brincar", POS: PosTags{"v"}, Translations: []string{"springen", "überspringen"}},
			"botar":     {Lemma: "botar", Translations: []string{"wegwerfen", "auswerfen", "springen"}},
			"rajarse":   {Lemma: "rajarse", POS: PosTags{"v"}, Translations: []string{"aufreißen", "einen ausgeben", "einladen", "reißen", "springen"}},
			"prueba":    {Lemma: "prueba", Gender: "f", POS: PosTags{"n"}, Translations: []string{"Beweis", "Probe", "Test"}, TargetGender: "n"},
			"brincador": {Lemma: "brincador", POS: PosTags{"adj"}, Translations: []string{"springend"}},
			"poder":     {Lemma: "poder", Gender: "m", POS: PosTags{"n", "v"}, Translations: []string{"Macht", "können"}, TargetGender: "f"},
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

	// Exact German beats Spanish lemma prefixes that share the same letters.
	hund := d.Search("Hund", 5, nil)
	if len(hund) == 0 || hund[0].Source != "perro" {
		t.Fatalf("exact DE first: %+v", sources(hund))
	}

	// Short queries keep both Spanish lemmas and German translations visible.
	mixed := d.Search("ha", 4, nil)
	if len(mixed) != 4 {
		t.Fatalf("mixed limit: got %d (%s)", len(mixed), sources(mixed))
	}
	var sawES, sawDE bool
	for _, c := range mixed {
		switch c.Source {
		case "haber", "hallar", "hablar":
			sawES = true
		case "casa": // Haus
			sawDE = true
		}
	}
	if !sawES || !sawDE {
		t.Fatalf("expected ES+DE mix, got %s", sources(mixed))
	}

	wollen := d.Search("wollen", 5, nil)
	if len(wollen) == 0 || wollen[0].Source != "querer" {
		t.Fatalf("wollen → querer first: %s", sources(wollen))
	}
	if wollen[0].Matched != "wollen" || wollen[0].Target != "wollen" {
		t.Fatalf("wollen gloss promoted: %+v", wollen[0])
	}

	springen := d.Search("springen", 10, nil)
	if len(springen) < 2 || springen[0].Source != "brincar" && springen[0].Source != "saltar" {
		t.Fatalf("primary springen first: %s", sources(springen))
	}
	for _, c := range springen {
		if c.Source == "rajarse" {
			t.Fatalf("buried sense rajarse should not match springen: %s", sources(springen))
		}
	}

	testen := d.Search("testen", 5, nil)
	if len(testen) == 0 || testen[0].Source != "prueba" {
		t.Fatalf("testen stem → prueba: %s", sources(testen))
	}
	if testen[0].Matched != "Test" {
		t.Fatalf("testen matched gloss: %+v", testen[0])
	}

	limited := d.Search("cas", 2, nil)
	if len(limited) != 2 {
		t.Fatalf("limit: got %d", len(limited))
	}

	verbs := d.Search("ha", 10, func(c Card) bool { return c.AllowsVerb() && c.HasPOS("v") })
	if len(verbs) == 0 {
		t.Fatalf("keep filter empty")
	}
	for _, c := range verbs {
		if !c.HasPOS("v") {
			t.Fatalf("keep filter leaked %+v", c)
		}
	}

	poder := d.Search("poder", 5, func(c Card) bool { return c.AllowsVerb() })
	if len(poder) == 0 || poder[0].Source != "poder" || !poder[0].AllowsVerb() {
		t.Fatalf("dual-POS poder should pass verb filter: %+v", poder)
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
