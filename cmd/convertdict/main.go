// convertdict converts FreeDict TEI (spa-deu) into a compact JSON dictionary,
// optionally supplemented with WikDict es-de lemmas FreeDict lacks.
package main

import (
	"encoding/json"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jakob/simple-vocabulary/internal/dictionary"
)

type Entry struct {
	Lemma        string            `json:"lemma"`
	Gender       string            `json:"gender,omitempty"` // m, f, n
	POS          dictionary.PosTags `json:"pos,omitempty"`
	Translations []string          `json:"translations"`
	TargetGender string            `json:"targetGender,omitempty"`
}

type DictFile struct {
	SourceLang string           `json:"sourceLang"`
	TargetLang string           `json:"targetLang"`
	License    string           `json:"license"`
	Entries    map[string]Entry `json:"entries"` // key = lowercase lemma
}

func main() {
	inPath := flag.String("in", "", "FreeDict spa-deu TEI (optional if -base is set)")
	basePath := flag.String("base", "", "existing spa-deu.json to start from (optional if -in is set)")
	wikdictPath := flag.String("wikdict", "", "optional WikDict es-de .sqlite3 / .tsv / .jsonl to merge")
	outPath := flag.String("out", "", "output spa-deu.json")
	flag.Parse()
	if *outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: convertdict -in spa-deu.tei [-wikdict es-de.sqlite3] -out data/spa-deu.json")
		fmt.Fprintln(os.Stderr, "       convertdict -base data/spa-deu.json -wikdict es-de.sqlite3 -out data/spa-deu.json")
		os.Exit(1)
	}
	if err := mustHaveSource(*inPath, *basePath); err != nil {
		fatal(err)
	}

	entries := map[string]Entry{}
	fromFreeDict := false
	fromWikdict := false

	if *basePath != "" {
		base, _, err := loadBaseJSON(*basePath)
		if err != nil {
			fatal(err)
		}
		entries = base
		fromFreeDict = true
	}

	if *inPath != "" {
		f, err := os.Open(*inPath)
		if err != nil {
			fatal(err)
		}
		tei, err := parseTEI(f)
		f.Close()
		if err != nil {
			fatal(err)
		}
		if *basePath == "" {
			entries = tei
		} else {
			// TEI replaces/extends the base (FreeDict rebuild wins on shared keys).
			for k, e := range tei {
				entries[k] = e
			}
		}
		fromFreeDict = true
	}

	if *wikdictPath != "" {
		rows, err := loadWikdict(*wikdictPath)
		if err != nil {
			fatal(err)
		}
		added, skipped := mergeWikdictInto(entries, rows)
		fromWikdict = true
		fmt.Printf("wikdict: +%d lemmas (%d rows skipped/existing)\n", added, skipped)
	}

	dict := DictFile{
		SourceLang: "es",
		TargetLang: "de",
		License:    licenseFor(fromFreeDict, fromWikdict),
		Entries:    entries,
	}

	out, err := os.Create(*outPath)
	if err != nil {
		fatal(err)
	}
	defer out.Close()

	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(dict); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %d entries to %s\n", len(entries), *outPath)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func parseTEI(r io.Reader) (map[string]Entry, error) {
	dec := xml.NewDecoder(r)
	dec.Strict = false

	merged := make(map[string]Entry)

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "entry" {
			continue
		}
		var raw entryXML
		if err := dec.DecodeElement(&raw, &se); err != nil {
			continue
		}
		e := raw.toEntry()
		if e.Lemma == "" || len(e.Translations) == 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(e.Lemma))
		if existing, ok := merged[key]; ok {
			existing.Translations = uniqAppend(existing.Translations, e.Translations...)
			if existing.Gender == "" {
				existing.Gender = e.Gender
			}
			existing.POS = mergePOS(existing.POS, e.POS...)
			if existing.TargetGender == "" {
				existing.TargetGender = e.TargetGender
			}
			merged[key] = existing
		} else {
			merged[key] = e
		}
	}
	return merged, nil
}

type entryXML struct {
	Form  formXML    `xml:"form"`
	Gram  *gramXML   `xml:"gramGrp"`
	Sense []senseXML `xml:"sense"`
}

type formXML struct {
	Orth string   `xml:"orth"`
	Gram *gramXML `xml:"gramGrp"`
}

type gramXML struct {
	POS string `xml:"pos"`
	Gen string `xml:"gen"`
}

type senseXML struct {
	Cit  []citXML `xml:"cit"`
	Gram *gramXML `xml:"gramGrp"`
}

type citXML struct {
	Type  string `xml:"type,attr"`
	Quote string `xml:"quote"`
}

func (e entryXML) toEntry() Entry {
	out := Entry{Lemma: strings.TrimSpace(e.Form.Orth)}
	gram := e.Form.Gram
	if gram == nil {
		gram = e.Gram
	}
	if gram != nil {
		if pos := normalizePOS(gram.POS); pos != "" {
			out.POS = dictionary.PosTags{pos}
		}
		out.Gender = normalizeGender(gram.Gen)
	}

	seen := map[string]struct{}{}
	for _, s := range e.Sense {
		for _, c := range s.Cit {
			if c.Type != "" && c.Type != "trans" {
				continue
			}
			t := strings.TrimSpace(c.Quote)
			if t == "" {
				continue
			}
			if _, ok := seen[t]; ok {
				continue
			}
			seen[t] = struct{}{}
			out.Translations = append(out.Translations, t)
		}
		if out.TargetGender == "" && s.Gram != nil {
			out.TargetGender = normalizeGender(s.Gram.Gen)
		}
	}
	return out
}

func normalizeGender(g string) string {
	g = strings.ToLower(strings.TrimSpace(g))
	switch g {
	case "m", "masc", "masculine":
		return "m"
	case "f", "fem", "feminine":
		return "f"
	case "n", "neut", "neuter":
		return "n"
	default:
		return ""
	}
}

func normalizePOS(p string) string {
	p = strings.ToLower(strings.TrimSpace(p))
	switch p {
	case "n", "noun", "pn":
		return "n"
	case "v", "verb":
		return "v"
	case "adj", "a":
		return "adj"
	case "adv":
		return "adv"
	default:
		return p
	}
}

func mergePOS(dst dictionary.PosTags, add ...string) dictionary.PosTags {
	out := append(dictionary.PosTags(nil), dst...)
	seen := map[string]struct{}{}
	for _, t := range out {
		seen[t] = struct{}{}
	}
	for _, t := range add {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func uniqAppend(dst []string, add ...string) []string {
	seen := map[string]struct{}{}
	for _, s := range dst {
		seen[s] = struct{}{}
	}
	for _, s := range add {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		dst = append(dst, s)
	}
	return dst
}
