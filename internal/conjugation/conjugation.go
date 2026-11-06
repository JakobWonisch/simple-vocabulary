// Package conjugation loads Spanish verb paradigms derived from UniMorph.
// Person slots are fixed:
//
//	0 yo, 1 tú, 2 él/ella/usted, 3 nosotros, 4 vosotros, 5 ellos/ellas/ustedes
package conjugation

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

const PersonCount = 6

// Paradigm is the slice of a verb this app can quiz.
// Indicative tenses: prs, ipfv, pfv, fut, cond.
// Subjunctive tenses: prs, ra, se, fut.
// Imp is the affirmative imperative in person-slot order (yo is usually empty).
// Ger is the gerund.
type Paradigm struct {
	Ind  map[string][]string `json:"ind,omitempty"`
	Sbjv map[string][]string `json:"sbjv,omitempty"`
	Imp  []string            `json:"imp,omitempty"`
	Ger  string              `json:"ger,omitempty"`
}

// HasForms reports whether any quiz form was extracted.
func (p Paradigm) HasForms() bool {
	if p.Ger != "" || len(p.Imp) > 0 {
		return true
	}
	for _, row := range p.Ind {
		if rowHasForm(row) {
			return true
		}
	}
	for _, row := range p.Sbjv {
		if rowHasForm(row) {
			return true
		}
	}
	return false
}

func rowHasForm(row []string) bool {
	for _, f := range row {
		if f != "" {
			return true
		}
	}
	return false
}

// Table is an in-memory lemma → paradigm map.
type Table struct {
	verbs map[string]Paradigm
	bare  map[string]string
}

// LoadJSON loads a compact paradigm file written by cmd/convertconj.
func LoadJSON(path string) (*Table, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Verbs map[string]Paradigm `json:"verbs"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if len(raw.Verbs) == 0 {
		return nil, fmt.Errorf("conjugations %s have no verbs", path)
	}
	return New(raw.Verbs), nil
}

// New indexes paradigms by lowercase lemma.
func New(verbs map[string]Paradigm) *Table {
	t := &Table{
		verbs: make(map[string]Paradigm, len(verbs)),
		bare:  map[string]string{},
	}
	for lemma, p := range verbs {
		key := normalize(lemma)
		if key == "" || !p.HasForms() {
			continue
		}
		t.verbs[key] = p
	}
	for key := range t.verbs {
		folded := foldAccents(key)
		if _, exists := t.verbs[folded]; exists {
			continue
		}
		if prev, ok := t.bare[folded]; ok && prev != key {
			continue
		}
		t.bare[folded] = key
	}
	return t
}

// Size returns the number of indexed lemmas.
func (t *Table) Size() int {
	if t == nil {
		return 0
	}
	return len(t.verbs)
}

// Lookup finds a paradigm by infinitive.
func (t *Table) Lookup(lemma string) (Paradigm, bool) {
	if t == nil {
		return Paradigm{}, false
	}
	key := normalize(lemma)
	if p, ok := t.verbs[key]; ok {
		return p, true
	}
	if canon, ok := t.bare[foldAccents(key)]; ok {
		p, ok := t.verbs[canon]
		return p, ok
	}
	return Paradigm{}, false
}

type bundle struct {
	group string
	tense string
}

var bundles = map[string]bundle{
	"V;IND;PRS":          {group: "ind", tense: "prs"},
	"V;IND;PST;IPFV":     {group: "ind", tense: "ipfv"},
	"V;IND;PST;PFV":      {group: "ind", tense: "pfv"},
	"V;IND;FUT":          {group: "ind", tense: "fut"},
	"V;COND":             {group: "ind", tense: "cond"},
	"V;SBJV;PRS":         {group: "sbjv", tense: "prs"},
	"V;SBJV;PST;LGSPEC1": {group: "sbjv", tense: "ra"},
	"V;SBJV;PST;LGSPEC2": {group: "sbjv", tense: "se"},
	"V;SBJV;FUT":         {group: "sbjv", tense: "fut"},
	"V;POS;IMP":          {group: "imp"},
}

var dropTag = map[string]struct{}{
	"1": {}, "2": {}, "3": {},
	"SG": {}, "PL": {},
	"INFM": {}, "FORM": {},
}

// ParseUniMorph reads a UniMorph Spanish TSV (lemma, form, features)
// and keeps the finite forms, affirmative imperative, and gerund this app shows.
// When several spellings share a slot, tuteo wins over voseo.
func ParseUniMorph(r io.Reader) (map[string]Paradigm, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	drafts := map[string]*verbDraft{}
	for sc.Scan() {
		line := sc.Text()
		lemma, form, feats, ok := splitTSV(line)
		if !ok || !strings.HasPrefix(feats, "V") {
			continue
		}
		if strings.Contains(form, " ") || strings.Contains(feats, "PRO") || strings.Contains(feats, "ACC") || strings.Contains(feats, "DAT") {
			continue
		}
		d := drafts[lemma]
		if d == nil {
			d = newVerbDraft()
			drafts[lemma] = d
		}
		if feats == "V;V.CVB;PRS" {
			d.ger = better(d.ger, form)
			continue
		}
		b, ok := bundles[bundleKey(feats)]
		if !ok {
			continue
		}
		slot, fallback, ok := personSlot(feats)
		if !ok {
			continue
		}
		d.row(b).set(slot, form, fallback)
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

func splitTSV(line string) (lemma, form, feats string, ok bool) {
	a, rest, ok := strings.Cut(line, "\t")
	if !ok {
		return "", "", "", false
	}
	b, c, ok := strings.Cut(rest, "\t")
	if !ok {
		return "", "", "", false
	}
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	c = strings.TrimSpace(c)
	if a == "" || b == "" || c == "" {
		return "", "", "", false
	}
	return a, b, c, true
}

func bundleKey(feats string) string {
	parts := strings.Split(feats, ";")
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if _, drop := dropTag[p]; drop {
			continue
		}
		kept = append(kept, p)
	}
	return strings.Join(kept, ";")
}

func personSlot(feats string) (slot int, fallback, ok bool) {
	var person, number string
	formal := false
	for _, tag := range strings.Split(feats, ";") {
		switch tag {
		case "1", "2", "3":
			person = tag
		case "SG", "PL":
			number = tag
		case "FORM":
			formal = true
		}
	}
	if person == "" || number == "" {
		return 0, false, false
	}
	switch {
	case person == "2" && number == "SG" && formal:
		return 2, true, true
	case person == "2" && number == "PL" && formal:
		return 5, true, true
	case person == "2" && number == "SG":
		return 1, false, true
	case person == "2" && number == "PL":
		return 4, false, true
	case person == "1" && number == "SG":
		return 0, false, true
	case person == "3" && number == "SG":
		return 2, false, true
	case person == "1" && number == "PL":
		return 3, false, true
	case person == "3" && number == "PL":
		return 5, false, true
	default:
		return 0, false, false
	}
}

type verbDraft struct {
	ind  map[string]*formRow
	sbjv map[string]*formRow
	imp  *formRow
	ger  string
}

func newVerbDraft() *verbDraft {
	return &verbDraft{
		ind:  map[string]*formRow{},
		sbjv: map[string]*formRow{},
	}
}

func (d *verbDraft) row(b bundle) *formRow {
	if b.group == "imp" {
		if d.imp == nil {
			d.imp = &formRow{}
		}
		return d.imp
	}
	table := d.ind
	if b.group == "sbjv" {
		table = d.sbjv
	}
	r := table[b.tense]
	if r == nil {
		r = &formRow{}
		table[b.tense] = r
	}
	return r
}

func (d *verbDraft) paradigm() Paradigm {
	p := Paradigm{Ger: d.ger}
	if row, ok := d.imp.forms(); ok {
		p.Imp = row
	}
	p.Ind = publish(d.ind)
	p.Sbjv = publish(d.sbjv)
	return p
}

func publish(rows map[string]*formRow) map[string][]string {
	if len(rows) == 0 {
		return nil
	}
	out := make(map[string][]string, len(rows))
	for tense, row := range rows {
		forms, ok := row.forms()
		if ok {
			out[tense] = forms
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

type formRow struct {
	form    [PersonCount]string
	usted   string
	ustedes string
}

func (r *formRow) set(slot int, form string, fallback bool) {
	if r == nil || form == "" {
		return
	}
	if fallback {
		switch slot {
		case 2:
			r.usted = better(r.usted, form)
		case 5:
			r.ustedes = better(r.ustedes, form)
		}
		return
	}
	if slot < 0 || slot >= PersonCount {
		return
	}
	r.form[slot] = better(r.form[slot], form)
}

func (r *formRow) forms() ([]string, bool) {
	if r == nil {
		return nil, false
	}
	out := r.form
	if out[2] == "" {
		out[2] = r.usted
	}
	if out[5] == "" {
		out[5] = r.ustedes
	}
	any := false
	copied := make([]string, PersonCount)
	for i := 0; i < PersonCount; i++ {
		copied[i] = out[i]
		if copied[i] != "" {
			any = true
		}
	}
	if !any {
		return nil, false
	}
	return copied, true
}

// better picks one spelling when UniMorph lists two forms for the same slot.
// Voseo (hablás, andá, sos) loses to tuteo. Hiatus accents (fías) are kept.
// A shorter remaining form wins (ha over hay, vamos over vayamos).
func better(current, next string) string {
	if current == "" {
		return next
	}
	if next == "" || next == current {
		return current
	}
	curVoseo := lastVowelAccented(current)
	nextVoseo := lastVowelAccented(next)
	if curVoseo != nextVoseo {
		if curVoseo {
			return next
		}
		return current
	}
	if current == "sos" && next != "sos" {
		return next
	}
	if next == "sos" && current != "sos" {
		return current
	}
	if foldAccents(current) == foldAccents(next) {
		if hasAccent(next) && !hasAccent(current) {
			return next
		}
		return current
	}
	if utf8.RuneCountInString(next) < utf8.RuneCountInString(current) {
		return next
	}
	if utf8.RuneCountInString(current) < utf8.RuneCountInString(next) {
		return current
	}
	if next < current {
		return next
	}
	return current
}

func lastVowelAccented(s string) bool {
	rs := []rune(s)
	for i := len(rs) - 1; i >= 0; i-- {
		if !isVowel(rs[i]) {
			continue
		}
		switch rs[i] {
		case 'á', 'é', 'í', 'ó', 'ú':
			return true
		default:
			return false
		}
	}
	return false
}

func hasAccent(s string) bool {
	for _, r := range s {
		switch r {
		case 'á', 'é', 'í', 'ó', 'ú':
			return true
		}
	}
	return false
}

func isVowel(r rune) bool {
	switch r {
	case 'a', 'e', 'i', 'o', 'u', 'á', 'é', 'í', 'ó', 'ú', 'ü':
		return true
	default:
		return false
	}
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func foldAccents(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 'á', 'à', 'ä', 'â':
			b.WriteRune('a')
		case 'é', 'è', 'ë', 'ê':
			b.WriteRune('e')
		case 'í', 'ì', 'ï', 'î':
			b.WriteRune('i')
		case 'ó', 'ò', 'ö', 'ô':
			b.WriteRune('o')
		case 'ú', 'ù', 'ü', 'û':
			b.WriteRune('u')
		case 'ñ':
			b.WriteRune('n')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
