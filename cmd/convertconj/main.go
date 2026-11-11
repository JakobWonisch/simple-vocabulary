// convertconj turns Kaikki/Wiktextract Spanish verb JSONL (or UniMorph TSV)
// into the compact paradigm file the app loads.
package main

import (
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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
	inPath := flag.String("in", "", "Kaikki Spanish verb JSONL (.jsonl or .jsonl.gz), or UniMorph TSV")
	format := flag.String("format", "kaikki", "input format: kaikki (default) or unimorph")
	dictPath := flag.String("dict", "", "optional spa-deu.json; keep only verbs that are dictionary headwords")
	outPath := flag.String("out", "", "compact JSON output")
	flag.Parse()
	if *inPath == "" || *outPath == "" {
		fmt.Fprintln(os.Stderr, "usage: convertconj -in kaikki.org-dictionary-Spanish-by-pos-verb.jsonl.gz -dict data/spa-deu.json -out data/spa-conj.json")
		fmt.Fprintln(os.Stderr, "       convertconj -format unimorph -in spa -dict data/spa-deu.json -out data/spa-conj.json")
		os.Exit(1)
	}

	rc, err := openInput(*inPath)
	if err != nil {
		fatal(err)
	}
	defer rc.Close()

	var parsed map[string]conjugation.Paradigm
	var source, license string
	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "kaikki", "wiktextract":
		parsed, err = conjugation.ParseKaikki(rc)
		source = "Kaikki.org Spanish verbs (Wiktextract extract of English Wiktionary). See data/LICENSE.conjugation."
		license = "CC BY-SA 4.0"
	case "unimorph":
		parsed, err = conjugation.ParseUniMorph(rc)
		source = "UniMorph Spanish (unimorph/spa), from English Wiktionary. See data/LICENSE.conjugation."
		license = "CC BY-SA 3.0"
	default:
		fatal(fmt.Errorf("unknown -format %q (want kaikki or unimorph)", *format))
	}
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
			// Kaikki/UniMorph already select verbs. Keep any dictionary
			// headword so dual-POS and noun-tagged infinitives (poder, cantar)
			// are not dropped by POS filters.
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
		Source:  source,
		License: license,
		Verbs:   verbs,
	}); err != nil {
		fatal(err)
	}
	fmt.Printf("wrote %d verbs to %s (parsed %d, format %s)\n", len(verbs), *outPath, len(parsed), *format)
}

func openInput(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return chained{Reader: zr, closer: func() error {
			err1 := zr.Close()
			err2 := f.Close()
			if err1 != nil {
				return err1
			}
			return err2
		}}, nil
	}
	return f, nil
}

type chained struct {
	io.Reader
	closer func() error
}

func (c chained) Close() error { return c.closer() }

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
