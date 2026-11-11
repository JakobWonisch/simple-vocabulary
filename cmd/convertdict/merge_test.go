package main

import (
	"path/filepath"
	"testing"

	"github.com/jakob/simple-vocabulary/internal/dictionary"
)

func TestMergeWikdictPrefersFreeDict(t *testing.T) {
	dst := map[string]Entry{
		"casa": {
			Lemma:        "casa",
			Gender:       "f",
			POS:          dictionary.PosTags{"n"},
			Translations: []string{"Haus"},
			TargetGender: "n",
		},
	}
	rows, err := loadWikdict(filepath.Join("testdata", "wikdict_sample.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	added, skipped := mergeWikdictInto(dst, rows)
	if added != 2 { // nuevo, zorro (-aje skipped, casa existing)
		t.Fatalf("added=%d skipped=%d want added=2 dst=%v", added, skipped, keys(dst))
	}
	if got := dst["casa"].Translations; len(got) != 1 || got[0] != "Haus" {
		t.Fatalf("casa should keep FreeDict glosses: %+v", dst["casa"])
	}
	if dst["casa"].Gender != "f" || !dst["casa"].POS.Has("n") {
		t.Fatalf("casa metadata changed: %+v", dst["casa"])
	}
	if _, ok := dst["-aje"]; ok {
		t.Fatal("affix -aje should be skipped")
	}
	if got := dst["zorro"].Translations; len(got) != 1 || got[0] != "Fuchs" {
		t.Fatalf("zorro: %+v", dst["zorro"])
	}
	if got := dst["nuevo"].Translations; len(got) != 1 || got[0] != "neu" {
		t.Fatalf("nuevo: %+v", dst["nuevo"])
	}
}

func TestSplitWikdictTranslations(t *testing.T) {
	got := splitWikdictTranslations(" Haus | Wohnung | Haus ")
	if len(got) != 2 || got[0] != "Haus" || got[1] != "Wohnung" {
		t.Fatalf("%v", got)
	}
}

func keys(m map[string]Entry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
