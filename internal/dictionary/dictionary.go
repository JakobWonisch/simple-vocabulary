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
	POS          string   `json:"pos,omitempty"`
	Translations []string `json:"translations"`
	TargetGender string   `json:"targetGender,omitempty"`
}

const maxCardTranslations = 3

// Card is a flashcard-ready pair with articles.
type Card struct {
	Source        string   `json:"source"`
	SourceArticle string   `json:"sourceArticle,omitempty"`
	Target        string   `json:"target"`
	TargetArticle string   `json:"targetArticle,omitempty"`
	Translations  []string `json:"translations,omitempty"`
	POS           string   `json:"pos,omitempty"`
	FoundInDict   bool     `json:"foundInDict"`
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
				POS:          e.POS,
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
	cards := make([]Card, 0, len(lemmas))
	seen := map[string]struct{}{}
	for _, lemma := range lemmas {
		lemma = strings.TrimSpace(lemma)
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

		card, ok := cardFromEntry(e, lemma)
		if !ok {
			continue
		}
		cards = append(cards, card)
	}
	return cards
}

const (
	searchScoreExactLemma = iota
	searchScoreLemmaPrefix
	searchScoreLemmaFolded
	searchScoreTranslation
)

// Search finds dictionary headwords by Spanish lemma prefix (diacritic-soft)
// and, with lower priority, by German translation prefix. Results are ranked
// and capped at limit. If keep is non-nil, only matching cards are returned;
// the scan continues until limit keepers are found.
func (d *Dictionary) Search(query string, limit int, keep func(Card) bool) []Card {
	q := normalizeKey(query)
	if q == "" || limit <= 0 || d == nil {
		return nil
	}
	qFold := stripDiacritics(q)

	type hit struct {
		card  Card
		score int
		key   string
	}
	hits := make([]hit, 0, limit)

	for _, e := range d.forward {
		score, key := searchScore(e, q, qFold)
		if score < 0 {
			continue
		}
		card, ok := cardFromEntry(e, e.Lemma)
		if !ok {
			continue
		}
		if keep != nil && !keep(card) {
			continue
		}
		hits = append(hits, hit{card: card, score: score, key: key})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score < hits[j].score
		}
		if len(hits[i].key) != len(hits[j].key) {
			return len(hits[i].key) < len(hits[j].key)
		}
		return hits[i].key < hits[j].key
	})

	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Card, len(hits))
	for i, h := range hits {
		out[i] = h.card
	}
	return out
}

func searchScore(e Entry, q, qFold string) (score int, key string) {
	lemma := normalizeKey(e.Lemma)
	if lemma == "" {
		return -1, ""
	}
	lemmaFold := stripDiacritics(lemma)
	switch {
	case lemma == q || lemmaFold == qFold:
		return searchScoreExactLemma, lemma
	case strings.HasPrefix(lemma, q):
		return searchScoreLemmaPrefix, lemma
	case strings.HasPrefix(lemmaFold, qFold):
		return searchScoreLemmaFolded, lemma
	}
	for _, tr := range e.Translations {
		tn := normalizeKey(tr)
		if tn == "" {
			continue
		}
		if strings.HasPrefix(tn, q) || strings.HasPrefix(stripDiacritics(tn), qFold) {
			return searchScoreTranslation, lemma
		}
	}
	return -1, ""
}

func cardFromEntry(e Entry, requested string) (Card, bool) {
	translations := topTranslations(e.Translations, maxCardTranslations)
	if len(translations) == 0 {
		return Card{}, false
	}
	return Card{
		Source:        displayLemma(e.Lemma, requested),
		SourceArticle: Article("es", e.Gender, e.POS),
		Target:        translations[0],
		TargetArticle: Article("de", e.TargetGender, e.POS),
		Translations:  translations,
		POS:           e.POS,
		FoundInDict:   true,
	}, true
}

// Size returns number of forward entries.
func (d *Dictionary) Size() int {
	return len(d.forward)
}

// Article returns the definite article for a language/gender/pos, or empty.
func Article(lang, gender, pos string) string {
	if pos != "" && pos != "n" {
		return ""
	}
	switch lang {
	case "es":
		switch gender {
		case "m":
			return "el"
		case "f":
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
