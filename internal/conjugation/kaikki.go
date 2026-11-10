package conjugation

import (
	"bufio"
	"encoding/json"
	"io"
	"strings"
)

// Kaikki / Wiktextract JSONL record (fields we care about).
type kaikkiEntry struct {
	Word     string        `json:"word"`
	POS      string        `json:"pos"`
	LangCode string        `json:"lang_code"`
	Senses   []kaikkiSense `json:"senses"`
	Forms    []kaikkiForm  `json:"forms"`
}

type kaikkiSense struct {
	Tags   []string          `json:"tags"`
	FormOf []kaikkiFormOfRef `json:"form_of"`
}

type kaikkiFormOfRef struct {
	Word string `json:"word"`
}

type kaikkiForm struct {
	Form string   `json:"form"`
	Tags []string `json:"tags"`
}

// ParseKaikki reads Kaikki/Wiktextract Spanish verb JSONL (one object per line)
// and builds paradigms in the same shape as ParseUniMorph.
//
// Only lemma entries with conjugation tables are kept (form-of rows and
// multiword verbs are skipped). When tú and vos spellings share a slot,
// tuteo wins via better().
func ParseKaikki(r io.Reader) (map[string]Paradigm, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	drafts := map[string]*verbDraft{}

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var entry kaikkiEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, err
		}
		lemma, ok := kaikkiLemma(entry)
		if !ok {
			continue
		}
		d := drafts[lemma]
		if d == nil {
			d = newVerbDraft()
			drafts[lemma] = d
		}
		for _, fr := range entry.Forms {
			applyKaikkiForm(d, fr.Form, fr.Tags)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make(map[string]Paradigm, len(drafts))
	for lemma, d := range drafts {
		p := d.paradigm()
		if p.HasForms() {
			out[lemma] = p
		}
	}
	return out, nil
}

// ParseWiktextract is an alias for ParseKaikki.
func ParseWiktextract(r io.Reader) (map[string]Paradigm, error) {
	return ParseKaikki(r)
}

func kaikkiLemma(entry kaikkiEntry) (string, bool) {
	if entry.POS != "" && entry.POS != "verb" {
		return "", false
	}
	if entry.LangCode != "" && entry.LangCode != "es" {
		return "", false
	}
	lemma := normalize(entry.Word)
	if lemma == "" || strings.Contains(lemma, " ") {
		return "", false
	}
	if !looksLikeInfinitive(lemma) {
		return "", false
	}
	if kaikkiFormOfOnly(entry.Senses) {
		return "", false
	}
	if !kaikkiHasConjugationTable(entry.Forms) {
		return "", false
	}
	return lemma, true
}

func looksLikeInfinitive(s string) bool {
	for _, suf := range []string{"ar", "er", "ir", "ár", "ér", "ír"} {
		if strings.HasSuffix(s, suf) {
			return true
		}
	}
	return false
}

func kaikkiFormOfOnly(senses []kaikkiSense) bool {
	if len(senses) == 0 {
		return false
	}
	for _, s := range senses {
		if len(s.FormOf) == 0 && !tagHas(s.Tags, "form-of") {
			return false
		}
	}
	return true
}

func kaikkiHasConjugationTable(forms []kaikkiForm) bool {
	for _, fr := range forms {
		if tagHas(fr.Tags, "indicative") || tagHas(fr.Tags, "subjunctive") || tagHas(fr.Tags, "imperative") {
			return true
		}
	}
	return false
}

func applyKaikkiForm(d *verbDraft, form string, tags []string) {
	form = strings.TrimSpace(form)
	if form == "" || form == "-" || strings.Contains(form, " ") {
		return
	}
	if tagHas(tags, "combined-form") || tagHas(tags, "with-clitic") {
		return
	}
	if tagHas(tags, "table-tags") || tagHas(tags, "inflection-template") || tagHas(tags, "class") {
		return
	}
	if tagHas(tags, "infinitive") {
		return
	}

	if tagHas(tags, "gerund") {
		// Bare gerund only (no attached clitics / person tags).
		if tagHas(tags, "first-person") || tagHas(tags, "second-person") || tagHas(tags, "third-person") {
			return
		}
		if tagHas(tags, "dative") || tagHas(tags, "accusative") {
			return
		}
		d.ger = better(d.ger, form)
		return
	}

	if tagHas(tags, "participle") && tagHas(tags, "past") {
		if tagHas(tags, "feminine") || tagHas(tags, "plural") {
			return
		}
		// Masculine singular, or ungendered head-table "past participle".
		d.ptcp = better(d.ptcp, form)
		return
	}

	b, ok := kaikkiBundle(tags)
	if !ok {
		return
	}
	slot, fallback, ok := kaikkiPersonSlot(tags)
	if !ok {
		return
	}
	d.row(b).set(slot, form, fallback)
}

func kaikkiBundle(tags []string) (bundle, bool) {
	if tagHas(tags, "imperative") {
		if tagHas(tags, "negative") {
			return bundle{group: "nimp"}, true
		}
		return bundle{group: "imp"}, true
	}
	if tagHas(tags, "subjunctive") {
		switch {
		case tagHas(tags, "present"):
			return bundle{group: "sbjv", tense: "prs"}, true
		case tagHas(tags, "future"):
			return bundle{group: "sbjv", tense: "fut"}, true
		case tagHas(tags, "imperfect"):
			if tagHas(tags, "imperfect-se") {
				return bundle{group: "sbjv", tense: "se"}, true
			}
			return bundle{group: "sbjv", tense: "ra"}, true
		default:
			return bundle{}, false
		}
	}
	if tagHas(tags, "conditional") {
		return bundle{group: "ind", tense: "cond"}, true
	}
	if tagHas(tags, "indicative") {
		switch {
		case tagHas(tags, "present"):
			return bundle{group: "ind", tense: "prs"}, true
		case tagHas(tags, "imperfect"):
			return bundle{group: "ind", tense: "ipfv"}, true
		case tagHas(tags, "preterite"):
			return bundle{group: "ind", tense: "pfv"}, true
		case tagHas(tags, "future"):
			return bundle{group: "ind", tense: "fut"}, true
		}
	}
	return bundle{}, false
}

func kaikkiPersonSlot(tags []string) (slot int, fallback, ok bool) {
	formal := tagHas(tags, "formal") || tagHas(tags, "second-person-semantically")
	switch {
	case tagHas(tags, "first-person") && tagHas(tags, "singular"):
		return 0, false, true
	case tagHas(tags, "first-person") && tagHas(tags, "plural"):
		return 3, false, true
	case tagHas(tags, "second-person") && tagHas(tags, "singular"):
		if formal {
			return 2, true, true
		}
		// tú and vos both land in slot 1; better() prefers tuteo.
		return 1, false, true
	case tagHas(tags, "second-person") && tagHas(tags, "plural"):
		if formal {
			return 5, true, true
		}
		return 4, false, true
	case tagHas(tags, "third-person") && tagHas(tags, "singular"):
		return 2, false, true
	case tagHas(tags, "third-person") && tagHas(tags, "plural"):
		return 5, false, true
	default:
		return 0, false, false
	}
}

func tagHas(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
