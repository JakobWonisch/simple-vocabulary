package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// mergeWikdictInto adds WikDict-only lemmas. Existing FreeDict keys are left
// unchanged (FreeDict wins on conflicts).
func mergeWikdictInto(dst map[string]Entry, rows []wikdictRow) (added, skipped int) {
	for _, row := range rows {
		if !keepWikdictLemma(row.WrittenRep) {
			skipped++
			continue
		}
		translations := splitWikdictTranslations(row.TransList)
		if len(translations) == 0 {
			skipped++
			continue
		}
		lemma := strings.TrimSpace(row.WrittenRep)
		key := strings.ToLower(lemma)
		if key == "" {
			skipped++
			continue
		}
		if _, exists := dst[key]; exists {
			skipped++
			continue
		}
		dst[key] = Entry{
			Lemma:        lemma,
			Translations: translations,
		}
		added++
	}
	return added, skipped
}

func loadBaseJSON(path string) (map[string]Entry, DictFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, DictFile{}, err
	}
	var raw DictFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, DictFile{}, err
	}
	if raw.Entries == nil {
		raw.Entries = map[string]Entry{}
	}
	out := make(map[string]Entry, len(raw.Entries))
	for k, e := range raw.Entries {
		key := strings.ToLower(strings.TrimSpace(k))
		if key == "" {
			continue
		}
		if e.Lemma == "" {
			e.Lemma = k
		}
		out[key] = e
	}
	return out, raw, nil
}

func licenseFor(freedict, wikdict bool) string {
	switch {
	case freedict && wikdict:
		return "FreeDict spa-deu + WikDict es-de — see data/LICENSE.dictionary"
	case wikdict:
		return "WikDict es-de — see data/LICENSE.dictionary"
	default:
		return "FreeDict spa-deu — see data/LICENSE.dictionary"
	}
}

func mustHaveSource(inPath, basePath string) error {
	if strings.TrimSpace(inPath) == "" && strings.TrimSpace(basePath) == "" {
		return fmt.Errorf("provide -in (FreeDict TEI) and/or -base (existing spa-deu.json)")
	}
	return nil
}
