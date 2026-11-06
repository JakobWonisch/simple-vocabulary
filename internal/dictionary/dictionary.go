package dictionary

import (
	"encoding/json"
	"fmt"
	"os"
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

// Card is a flashcard-ready pair with articles.
type Card struct {
	Source         string `json:"source"`
	SourceArticle  string `json:"sourceArticle,omitempty"`
	Target         string `json:"target"`
	TargetArticle  string `json:"targetArticle,omitempty"`
	POS            string `json:"pos,omitempty"`
	FoundInDict    bool   `json:"foundInDict"`
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

// EnrichWords builds cards from Spanish lemmas using the dictionary.
// unknownTranslations may supply German glosses for lemmas missing from the dict.
func (d *Dictionary) EnrichWords(lemmas []string, unknownTranslations map[string]string) []Card {
	cards := make([]Card, 0, len(lemmas))
	seen := map[string]struct{}{}
	for _, lemma := range lemmas {
		lemma = strings.TrimSpace(lemma)
		if lemma == "" {
			continue
		}
		key := normalizeKey(lemma)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		if e, ok := d.Lookup(lemma); ok && len(e.Translations) > 0 {
			cards = append(cards, Card{
				Source:        displayLemma(e.Lemma, lemma),
				SourceArticle: Article("es", e.Gender, e.POS),
				Target:        e.Translations[0],
				TargetArticle: Article("de", e.TargetGender, e.POS),
				POS:           e.POS,
				FoundInDict:   true,
			})
			continue
		}

		fallback, hasFallback := unknownTranslations[lemma]
		if !hasFallback {
			fallback, hasFallback = unknownTranslations[key]
		}
		if !hasFallback || strings.TrimSpace(fallback) == "" {
			continue
		}
		cards = append(cards, Card{
			Source:      lemma,
			Target:      strings.TrimSpace(fallback),
			FoundInDict: false,
		})
	}
	return cards
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

func displayLemma(canonical, requested string) string {
	if canonical != "" {
		return canonical
	}
	return requested
}
