package dictionary

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"
)

// Entry is one dictionary headword.
type Entry struct {
	Lemma        string   `json:"lemma"`
	Gender       string   `json:"gender,omitempty"`
	POS          PosTags  `json:"pos,omitempty"`
	Translations []string `json:"translations"`
	TargetGender string   `json:"targetGender,omitempty"`
}

const (
	maxCardTranslations = 3
	// maxSearchExactTranslation is exclusive: exact DE matches may use the first
	// four glosses (so "wollen" on querer still hits) but not deeper senses.
	maxSearchExactTranslation = maxCardTranslations + 1
)

// Card is a flashcard-ready pair with articles.
type Card struct {
	Source        string   `json:"source"`
	SourceArticle string   `json:"sourceArticle,omitempty"`
	Target        string   `json:"target"`
	TargetArticle string   `json:"targetArticle,omitempty"`
	Translations  []string `json:"translations,omitempty"`
	POS           string   `json:"pos,omitempty"` // primary tag for display/storage
	FoundInDict   bool     `json:"foundInDict"`
	// Matched is the German gloss that matched a DE-side search, when any.
	Matched string `json:"matched,omitempty"`

	posTags PosTags // full FreeDict tag set; used for verb filters in-process
}

// HasPOS reports whether the underlying entry carries the given POS tag.
// Untagged cards (no tags) are treated as compatible with any tag filter that
// historically allowed empty POS through.
func (c Card) HasPOS(tag string) bool {
	if len(c.posTags) == 0 {
		return c.POS == "" || c.POS == tag
	}
	return c.posTags.Has(tag)
}

// AllowsVerb reports whether this card may be used in conjugation flows.
func (c Card) AllowsVerb() bool {
	if len(c.posTags) == 0 {
		return c.POS == "" || c.POS == "v"
	}
	return c.posTags.AllowsVerb()
}

// LemmaGloss pairs a lemma with an optional preferred German gloss (e.g. from AI).
type LemmaGloss struct {
	Lemma string
	Gloss string
}

type fileShape struct {
	SourceLang string           `json:"sourceLang"`
	TargetLang string           `json:"targetLang"`
	Entries    map[string]Entry `json:"entries"`
}

// Dictionary holds in-memory lookup tables for both directions.
type Dictionary struct {
	SourceLang string
	TargetLang string
	forward    map[string]Entry // es → de
	reverse    map[string]Entry // de → es (best-effort)
}

// LoadJSON loads a converted FreeDict JSON file.
func LoadJSON(path string) (*Dictionary, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var raw fileShape
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if len(raw.Entries) == 0 {
		return nil, fmt.Errorf("dictionary %s has no entries", path)
	}

	d := &Dictionary{
		SourceLang: raw.SourceLang,
		TargetLang: raw.TargetLang,
		forward:    make(map[string]Entry, len(raw.Entries)),
		reverse:    make(map[string]Entry, len(raw.Entries)),
	}
	for k, e := range raw.Entries {
		key := normalizeKey(k)
		if e.Lemma == "" {
			e.Lemma = k
		}
		d.forward[key] = e
		if len(e.Translations) == 0 {
			continue
		}
		// Prefer first translation as reverse headword.
		rk := normalizeKey(e.Translations[0])
		if _, exists := d.reverse[rk]; !exists {
			d.reverse[rk] = Entry{
				Lemma:        e.Translations[0],
				Gender:       e.TargetGender,
				POS:          append(PosTags(nil), e.POS...),
				Translations: []string{e.Lemma},
				TargetGender: e.Gender,
			}
		}
	}
	return d, nil
}

// Lookup finds an entry by lemma in the Spanish→German map.
func (d *Dictionary) Lookup(lemma string) (Entry, bool) {
	e, ok := d.forward[normalizeKey(lemma)]
	if ok {
		return e, true
	}
	// Try without diacritics as soft fallback.
	e, ok = d.forward[stripDiacritics(normalizeKey(lemma))]
	return e, ok
}

// LookupReverse finds German→Spanish.
func (d *Dictionary) LookupReverse(lemma string) (Entry, bool) {
	e, ok := d.reverse[normalizeKey(lemma)]
	if ok {
		return e, true
	}
	e, ok = d.reverse[stripDiacritics(normalizeKey(lemma))]
	return e, ok
}

// EnrichWords builds cards from lemmas that exist in the dictionary.
// Lemmas with no dictionary entry are omitted. Translations always come from
// the dictionary entry, never from an external suggestion.
func (d *Dictionary) EnrichWords(lemmas []string) []Card {
	pairs := make([]LemmaGloss, 0, len(lemmas))
	for _, lemma := range lemmas {
		pairs = append(pairs, LemmaGloss{Lemma: lemma})
	}
	return d.EnrichLemmaGlosses(pairs)
}

// EnrichLemmaGlosses is EnrichWords with optional preferred glosses. A gloss is
// promoted to the card target only when it matches a dictionary translation;
// otherwise FreeDict order is kept and other senses still appear below.
func (d *Dictionary) EnrichLemmaGlosses(pairs []LemmaGloss) []Card {
	cards := make([]Card, 0, len(pairs))
	seen := map[string]struct{}{}
	for _, pair := range pairs {
		lemma := strings.TrimSpace(pair.Lemma)
		if lemma == "" {
			continue
		}
		e, ok := d.Lookup(lemma)
		if !ok || len(e.Translations) == 0 {
			continue
		}
		key := normalizeKey(e.Lemma)
		if key == "" {
			key = normalizeKey(lemma)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		card, ok := cardFromEntry(e, lemma, pair.Gloss)
		if !ok {
			continue
		}
		cards = append(cards, card)
	}
	return cards
}

const (
	searchScoreExactLemma = iota
	searchScoreExactTranslation
	searchScoreExactTranslationWord // "wollen" inside "Wie viel wollen Sie?"
	searchScoreLemmaPrefix
	searchScoreLemmaFolded
	searchScoreTranslationPrefix
	searchScoreStemTranslation
)

type searchHit struct {
	card    Card
	score   int
	key     string
	viaDE   bool
	trIndex int
}

// Search finds dictionary headwords by Spanish lemma and German translation at
// the same time (diacritic-soft prefixes). Exact matches rank above prefixes;
// German hits prefer primary senses. Infinitive stems (testen→test) are a
// weaker DE fallback. Spanish and German hits are interleaved so one language
// cannot fill the whole limit alone. If keep is non-nil, only matching cards
// are kept.
func (d *Dictionary) Search(query string, limit int, keep func(Card) bool) []Card {
	q := normalizeKey(query)
	if q == "" || limit <= 0 || d == nil {
		return nil
	}
	qFold := stripDiacritics(q)
	stems := germanSearchStems(q)

	var esHits, deHits []searchHit
	for _, e := range d.forward {
		m := scoreEntry(e, q, qFold, stems)
		if m.score < 0 {
			continue
		}
		card, ok := cardFromEntry(e, e.Lemma, m.gloss)
		if !ok {
			continue
		}
		if keep != nil && !keep(card) {
			continue
		}
		h := searchHit{card: card, score: m.score, key: m.key, viaDE: m.viaDE, trIndex: m.trIndex}
		if m.viaDE {
			deHits = append(deHits, h)
		} else {
			esHits = append(esHits, h)
		}
	}

	sortSearchHits(esHits)
	sortSearchHits(deHits)
	merged := interleaveSearchHits(esHits, deHits, limit)
	out := make([]Card, len(merged))
	for i, h := range merged {
		out[i] = h.card
	}
	return out
}

// lemmaSearchBoost prefers core learner verbs when several entries share the
// same German gloss (e.g. wollen → querer over pretender).
var lemmaSearchBoost = map[string]int{
	"querer": 4,
	"tener":  3,
	"poder":  3,
	"poner":  2,
	"venir":  2,
	"hacer":  2,
	"decir":  2,
	"ir":     2,
	"ser":    2,
	"estar":  2,
	"haber":  2,
	"saber":  2,
	"dar":    2,
	"ver":    2,
}

func sortSearchHits(hits []searchHit) {
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		ri := hits[i].trIndex - lemmaSearchBoost[hits[i].key]
		rj := hits[j].trIndex - lemmaSearchBoost[hits[j].key]
		if ri != rj {
			return ri < rj
		}
		if len(hits[i].key) != len(hits[j].key) {
			return len(hits[i].key) < len(hits[j].key)
		}
		return hits[i].key < hits[j].key
	})
}

// interleaveSearchHits keeps strong exact matches first, then alternates
// remaining ES/DE hits so both languages stay visible within limit.
func interleaveSearchHits(es, de []searchHit, limit int) []searchHit {
	out := make([]searchHit, 0, limit)
	seen := map[string]struct{}{}
	take := func(h searchHit) bool {
		if _, ok := seen[h.key]; ok {
			return false
		}
		seen[h.key] = struct{}{}
		out = append(out, h)
		return true
	}
	isStrong := func(h searchHit) bool {
		return h.score <= searchScoreExactTranslationWord
	}

	i, j := 0, 0
	for len(out) < limit && (i < len(es) || j < len(de)) {
		for i < len(es) && isStrong(es[i]) && len(out) < limit {
			take(es[i])
			i++
		}
		for j < len(de) && isStrong(de[j]) && len(out) < limit {
			take(de[j])
			j++
		}
		if len(out) >= limit {
			break
		}
		added := false
		if i < len(es) {
			if take(es[i]) {
				added = true
			}
			i++
		}
		if len(out) >= limit {
			break
		}
		if j < len(de) {
			if take(de[j]) {
				added = true
			}
			j++
		}
		if !added && i >= len(es) && j >= len(de) {
			break
		}
		if !added {
			continue
		}
	}
	return out
}

type entryMatch struct {
	score   int
	key     string
	viaDE   bool
	trIndex int
	gloss   string
}

func scoreEntry(e Entry, q, qFold string, stems []string) entryMatch {
	lemma := normalizeKey(e.Lemma)
	if lemma == "" {
		return entryMatch{score: -1}
	}
	lemmaFold := stripDiacritics(lemma)
	switch {
	case lemma == q || lemmaFold == qFold:
		return entryMatch{score: searchScoreExactLemma, key: lemma}
	case strings.HasPrefix(lemma, q):
		return entryMatch{score: searchScoreLemmaPrefix, key: lemma}
	case strings.HasPrefix(lemmaFold, qFold):
		return entryMatch{score: searchScoreLemmaFolded, key: lemma}
	}

	best := entryMatch{score: -1, key: lemma, viaDE: true}
	for i, tr := range e.Translations {
		tn := normalizeKey(tr)
		if tn == "" {
			continue
		}
		tnFold := stripDiacritics(tn)
		gloss := strings.TrimSpace(tr)

		if i < maxSearchExactTranslation && (tn == q || tnFold == qFold) {
			cand := entryMatch{score: searchScoreExactTranslation, key: lemma, viaDE: true, trIndex: i, gloss: gloss}
			if best.score < 0 || cand.score < best.score || (cand.score == best.score && cand.trIndex < best.trIndex) {
				best = cand
			}
			continue
		}
		if i < maxSearchExactTranslation && translationHasWord(tnFold, qFold) {
			cand := entryMatch{score: searchScoreExactTranslationWord, key: lemma, viaDE: true, trIndex: i, gloss: gloss}
			if best.score < 0 || cand.score < best.score || (cand.score == best.score && cand.trIndex < best.trIndex) {
				best = cand
			}
			continue
		}

		// Prefix matches only on primary senses — buried glosses are too noisy.
		if i < maxCardTranslations && (strings.HasPrefix(tn, q) || strings.HasPrefix(tnFold, qFold) || translationHasPrefixWord(tnFold, qFold)) {
			cand := entryMatch{score: searchScoreTranslationPrefix, key: lemma, viaDE: true, trIndex: i, gloss: gloss}
			if best.score < 0 || cand.score < best.score || (cand.score == best.score && cand.trIndex < best.trIndex) {
				best = cand
			}
			continue
		}

		// Stem matches may use deeper glosses (testen→Test on prueba) but stay
		// weaker than exact/prefix primary hits.
		for _, stem := range stems {
			stemFold := stripDiacritics(stem)
			if stemFold == "" {
				continue
			}
			if tn == stem || tnFold == stemFold || translationHasWord(tnFold, stemFold) {
				cand := entryMatch{score: searchScoreStemTranslation, key: lemma, viaDE: true, trIndex: i, gloss: gloss}
				if best.score < 0 || cand.score < best.score || (cand.score == best.score && cand.trIndex < best.trIndex) {
					best = cand
				}
				break
			}
		}
	}
	return best
}

// germanSearchStems returns weaker DE lookup keys for infinitive-like queries
// (testen→test, wollen→woll).
func germanSearchStems(q string) []string {
	if len([]rune(q)) < 5 {
		return nil
	}
	if strings.HasSuffix(q, "en") {
		stem := strings.TrimSuffix(q, "en")
		if stem != "" && stem != q {
			return []string{stem}
		}
	}
	return nil
}

func translationHasWord(hayFold, needleFold string) bool {
	if needleFold == "" {
		return false
	}
	for _, w := range translationWords(hayFold) {
		if w == needleFold {
			return true
		}
	}
	return false
}

func translationHasPrefixWord(hayFold, needleFold string) bool {
	if needleFold == "" {
		return false
	}
	for _, w := range translationWords(hayFold) {
		if strings.HasPrefix(w, needleFold) {
			return true
		}
	}
	return false
}

func translationWords(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return !(unicode.IsLetter(r) || r == '-')
	})
	out := fields[:0]
	for _, f := range fields {
		f = strings.Trim(f, "-")
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

func cardFromEntry(e Entry, requested, preferredGloss string) (Card, bool) {
	matched := matchDictionaryGloss(e.Translations, preferredGloss)
	translations := topTranslations(e.Translations, maxCardTranslations)
	if matched != "" {
		translations = promoteTranslation(translations, e.Translations, matched)
	}
	if len(translations) == 0 {
		return Card{}, false
	}
	return Card{
		Source:        displayLemma(e.Lemma, requested),
		SourceArticle: Article("es", e.Gender, e.POS, displayLemma(e.Lemma, requested)),
		Target:        translations[0],
		TargetArticle: Article("de", e.TargetGender, e.POS, ""),
		Translations:  translations,
		POS:           e.POS.Primary(),
		FoundInDict:   true,
		Matched:       matched,
		posTags:       append(PosTags(nil), e.POS...),
	}, true
}

// matchDictionaryGloss returns the dictionary's own gloss string when preferred
// matches a sense (case-insensitive, diacritic-soft). Empty if no dict sense matches.
func matchDictionaryGloss(all []string, preferred string) string {
	g := strings.TrimSpace(preferred)
	if g == "" {
		return ""
	}
	gFold := stripDiacritics(strings.ToLower(g))
	for _, item := range all {
		t := strings.TrimSpace(item)
		if t == "" {
			continue
		}
		if strings.EqualFold(t, g) {
			return t
		}
	}
	for _, item := range all {
		t := strings.TrimSpace(item)
		if t == "" {
			continue
		}
		if stripDiacritics(strings.ToLower(t)) == gFold {
			return t
		}
	}
	return ""
}

func promoteTranslation(top, all []string, gloss string) []string {
	g := strings.TrimSpace(gloss)
	if g == "" {
		return top
	}
	out := make([]string, 0, maxCardTranslations)
	seen := map[string]struct{}{}
	add := func(item string) {
		item = strings.TrimSpace(item)
		if item == "" {
			return
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	add(g)
	for _, item := range top {
		if len(out) == maxCardTranslations {
			return out
		}
		add(item)
	}
	for _, item := range all {
		if len(out) == maxCardTranslations {
			return out
		}
		add(item)
	}
	return out
}

// Size returns number of forward entries.
func (d *Dictionary) Size() int {
	return len(d.forward)
}

// Article returns the definite article for a language/gender/pos, or empty.
// lemma is used only for Spanish feminine nouns that take el before stressed á.
func Article(lang, gender string, pos PosTags, lemma string) string {
	if len(pos) > 0 && !pos.Has("n") {
		return ""
	}
	switch lang {
	case "es":
		switch gender {
		case "m":
			return "el"
		case "f":
			if feminineTakesEl(lemma) {
				return "el"
			}
			return "la"
		}
	case "de":
		switch gender {
		case "m":
			return "der"
		case "f":
			return "die"
		case "n":
			return "das"
		}
	}
	return ""
}

// Feminine nouns whose citation form begins with stressed /a/ (or /ha/) take
// the article el while remaining feminine (el agua fría).
var feminineElAllowlist = map[string]struct{}{
	"agua": {}, "aguas": {},
	"alma": {}, "almas": {},
	"arma": {}, "armas": {},
	"arpa": {}, "arpas": {},
	"aula": {}, "aulas": {},
	"ave": {}, "aves": {},
	"ancla": {}, "anclas": {},
	"asa": {}, "asas": {},
	"asma": {},
	"habla": {},
	"hada": {}, "hadas": {},
	"hacha": {}, "hachas": {},
	"hambre": {},
	"haya": {}, "hayas": {},
}

func feminineTakesEl(lemma string) bool {
	w := strings.ToLower(strings.TrimSpace(lemma))
	if w == "" || w == "a" || w == "hache" {
		return false
	}
	if _, ok := feminineElAllowlist[w]; ok {
		return true
	}
	return strings.HasPrefix(w, "á") || strings.HasPrefix(w, "há")
}

func normalizeKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func stripDiacritics(s string) string {
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
		case 'ç':
			b.WriteRune('c')
		default:
			if unicode.IsLetter(r) || unicode.IsSpace(r) || r == '-' {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func topTranslations(in []string, n int) []string {
	out := make([]string, 0, n)
	seen := map[string]struct{}{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		key := strings.ToLower(item)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
		if len(out) == n {
			break
		}
	}
	return out
}

func displayLemma(canonical, requested string) string {
	if canonical != "" {
		return canonical
	}
	return requested
}
