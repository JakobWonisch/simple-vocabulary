// convertconj turns a UniMorph Spanish TSV into the compact paradigm file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jakob/simple-vocabulary/internal/conjugation"
	"github.com/jakob/simple-vocabulary/internal/dictionary"
)

type file struct {
	Source  string                          `json:"source"`
	License string                          `json:"license"`
	Verbs   map[string]conjugation.Paradigm `json:"verbs"`
}

func main() {
	inPath := flag.String("in", "", "UniMorph Spanish TSV (lemma, form, features)")
	dictPath := flag.String("dict", "", "optional spa-deu.json; keep only verbs that are dictionary headwords")
	outPath := flag.String("out", "", "compact JSON output")
	flag.Parse()
	if *inPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: convertconj -in spa -dict data/spa-deu.json -out data/spa-conj.json")
		os.Exit(1)
	}

	f, err := os.Open(*inPath)
	if err != nil {
		fatal(err)
	}
	defer f.Close()

	parsed, err := conjugation.ParseUniMorph(f)
	if err != nil {
		fatal(err)
	}

	verbs := parsed
	if *dictPath != "" {
		dict, err := dictionary.LoadJSON(*dictPath)
		if err != nil {
			fatal(err)
		}
		verbs = map[string]conjugation.Paradigm{}
		for lemma, p := range parsed {
			// Keep UniMorph paradigms for any FreeDict headword. Dual-POS lemmas
			// (poder n+v) and noun-tagged verbs whose TEI verb entries omit POS
			// (cantar) must not be dropped — UniMorph already selects verbs.
			if _, ok := dict.Lookup(lemma); !ok {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(lemma))
			verbs[key] = p
		}
	}

	out, err := os.Create(*outPath)
	if err != nil {
		fatal(err)
	}
	defer out.Close()

	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(file{
		Source:  "UniMorph Spanish (unimorph/spa), from English Wiktionary. See data/LICENSE.conjugation.",
		License: "CC BY-SA 3.0",
		Verbs:   verbs,
	}); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %d verbs to %s (parsed %d)\n", len(verbs), *outPath, len(parsed))
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
