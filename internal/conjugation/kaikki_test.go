package conjugation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseKaikkiCommonIrregulars(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "kaikki_verbs_sample.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	got, err := ParseKaikki(f)
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := got["quiero"]; ok {
		t.Fatal("form-of entry quiero must not become a lemma")
	}
	if _, ok := got["tener que"]; ok {
		t.Fatal("multiword verb must be skipped")
	}

	hablar := got["hablar"]
	wantPrs := []string{"hablo", "hablas", "habla", "hablamos", "habláis", "hablan"}
	if strings.Join(hablar.Ind["prs"], ",") != strings.Join(wantPrs, ",") {
		t.Fatalf("hablar present %v", hablar.Ind["prs"])
	}
	if hablar.Ger != "hablando" || hablar.Ptcp != "hablado" {
		t.Fatalf("hablar ger %q ptcp %q", hablar.Ger, hablar.Ptcp)
	}
	if hablar.Imp[1] != "habla" || hablar.Imp[2] != "hable" || hablar.Imp[4] != "hablad" {
		t.Fatalf("hablar imp %v", hablar.Imp)
	}
	if hablar.Nimp[1] != "hables" || hablar.Nimp[4] != "habléis" {
		t.Fatalf("hablar nimp %v", hablar.Nimp)
	}
	if hablar.Sbjv["ra"][0] != "hablara" || hablar.Sbjv["se"][0] != "hablase" {
		t.Fatalf("hablar sbjv ra=%v se=%v", hablar.Sbjv["ra"], hablar.Sbjv["se"])
	}

	checks := map[string][]string{
		"querer": {"quiero", "quieres", "quiere", "queremos", "queréis", "quieren"},
		"tener":  {"tengo", "tienes", "tiene", "tenemos", "tenéis", "tienen"},
		"poder":  {"puedo", "puedes", "puede", "podemos", "podéis", "pueden"},
		"venir":  {"vengo", "vienes", "viene", "venimos", "venís", "vienen"},
		"poner":  {"pongo", "pones", "pone", "ponemos", "ponéis", "ponen"},
	}
	for lemma, want := range checks {
		p, ok := got[lemma]
		if !ok {
			t.Fatalf("missing %s", lemma)
		}
		if strings.Join(p.Ind["prs"], ",") != strings.Join(want, ",") {
			t.Fatalf("%s present %v want %v", lemma, p.Ind["prs"], want)
		}
	}

	if got["ser"].Ind["prs"][1] != "eres" {
		t.Fatalf("ser tú %v", got["ser"].Ind["prs"])
	}
	if got["hacer"].Imp[1] != "haz" {
		t.Fatalf("hacer imp %v", got["hacer"].Imp)
	}
	if got["ir"].Imp[1] != "ve" {
		t.Fatalf("ir imp %v", got["ir"].Imp)
	}
	if got["fiar"].Ind["prs"][1] != "fías" {
		t.Fatalf("fiar prefers accented tuteo %v", got["fiar"].Ind["prs"])
	}
	if got["freír"].Ptcp != "frito" {
		t.Fatalf("freír ptcp %q", got["freír"].Ptcp)
	}
	if got["haber"].Ind["prs"][2] != "ha" {
		t.Fatalf("haber 3sg %v", got["haber"].Ind["prs"])
	}
}

func TestParseKaikkiEmpty(t *testing.T) {
	got, err := ParseKaikki(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d", len(got))
	}
}
