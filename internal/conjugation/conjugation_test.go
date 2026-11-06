package conjugation

import (
	"strings"
	"testing"
)

func TestParseUniMorphPrefersTuteo(t *testing.T) {
	const raw = "" +
		"hablar\thablo\tV;IND;PRS;1;SG\n" +
		"hablar\thablas\tV;IND;PRS;2;SG;INFM\n" +
		"hablar\thablás\tV;IND;PRS;2;SG;INFM\n" +
		"hablar\thabla\tV;IND;PRS;3;SG\n" +
		"hablar\thabla\tV;IND;PRS;2;SG;FORM\n" +
		"hablar\thablamos\tV;IND;PRS;1;PL\n" +
		"hablar\thabláis\tV;IND;PRS;2;PL\n" +
		"hablar\thablan\tV;IND;PRS;3;PL\n" +
		"hablar\thablando\tV;V.CVB;PRS\n" +
		"hablar\thablándote\tV;V.CVB;PRS;DAT;PRO;2;SG\n" +
		"hablar\thabla\tV;POS;IMP;2;SG;INFM\n" +
		"hablar\thablá\tV;POS;IMP;2;SG;INFM\n" +
		"hablar\thable\tV;POS;IMP;2;SG;FORM\n" +
		"hablar\thablemos\tV;POS;IMP;1;PL\n" +
		"hablar\thablad\tV;POS;IMP;2;PL;INFM\n" +
		"hablar\thablen\tV;POS;IMP;2;PL;FORM\n" +
		"hablar\tno hables\tV;NEG;IMP;2;SG;INFM\n" +
		"ser\teres\tV;IND;PRS;2;SG;INFM\n" +
		"ser\tsos\tV;IND;PRS;2;SG;INFM\n" +
		"hacer\thaz\tV;POS;IMP;2;SG;INFM\n" +
		"hacer\thacé\tV;POS;IMP;2;SG;INFM\n" +
		"ir\tve\tV;POS;IMP;2;SG;INFM\n" +
		"ir\tandá\tV;POS;IMP;2;SG;INFM\n" +
		"ir\tvamos\tV;POS;IMP;1;PL\n" +
		"ir\tvayamos\tV;POS;IMP;1;PL\n" +
		"fiar\tfias\tV;IND;PRS;2;SG;INFM\n" +
		"fiar\tfías\tV;IND;PRS;2;SG;INFM\n" +
		"haber\tha\tV;IND;PRS;3;SG\n" +
		"haber\thay\tV;IND;PRS;3;SG\n" +
		"haber\the\tV;POS;IMP;2;SG;INFM\n" +
		"haber\thabe\tV;POS;IMP;2;SG;INFM\n"

	got, err := ParseUniMorph(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	hablar := got["hablar"]
	want := []string{"hablo", "hablas", "habla", "hablamos", "habláis", "hablan"}
	if strings.Join(hablar.Ind["prs"], ",") != strings.Join(want, ",") {
		t.Fatalf("present %v", hablar.Ind["prs"])
	}
	if hablar.Ger != "hablando" {
		t.Fatalf("ger %q", hablar.Ger)
	}
	if hablar.Imp[0] != "" || hablar.Imp[1] != "habla" || hablar.Imp[2] != "hable" || hablar.Imp[4] != "hablad" || hablar.Imp[5] != "hablen" {
		t.Fatalf("imp %v", hablar.Imp)
	}
	if got["ser"].Ind["prs"][1] != "eres" {
		t.Fatalf("ser tú %v", got["ser"].Ind["prs"])
	}
	if got["hacer"].Imp[1] != "haz" {
		t.Fatalf("hacer imp %v", got["hacer"].Imp)
	}
	if got["ir"].Imp[1] != "ve" || got["ir"].Imp[3] != "vamos" {
		t.Fatalf("ir imp %v", got["ir"].Imp)
	}
	if got["fiar"].Ind["prs"][1] != "fías" {
		t.Fatalf("fiar %v", got["fiar"].Ind["prs"])
	}
	if got["haber"].Ind["prs"][2] != "ha" || got["haber"].Imp[1] != "he" {
		t.Fatalf("haber ind %v imp %v", got["haber"].Ind["prs"], got["haber"].Imp)
	}
}

func TestLookupFoldsAccent(t *testing.T) {
	table := New(map[string]Paradigm{
		"oír": {Ger: "oyendo"},
	})
	p, ok := table.Lookup("oir")
	if !ok || p.Ger != "oyendo" {
		t.Fatalf("lookup %+v %v", p, ok)
	}
}
