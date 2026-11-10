package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/jakob/simple-vocabulary/internal/conjugation"
	"github.com/jakob/simple-vocabulary/internal/dictionary"
)

func searchTestDict(t *testing.T) *dictionary.Dictionary {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dict.json")
	raw := `{
		"sourceLang":"es","targetLang":"de",
		"entries":{
			"casa":{"lemma":"casa","gender":"f","pos":"n","translations":["Haus","Gebäude"],"targetGender":"n"},
			"casar":{"lemma":"casar","pos":"v","translations":["heiraten"]},
			"hablar":{"lemma":"hablar","pos":"v","translations":["sprechen"]},
			"comer":{"lemma":"comer","translations":["essen"]},
			"libro":{"lemma":"libro","gender":"m","pos":"n","translations":["Buch"],"targetGender":"n"},
			"niño":{"lemma":"niño","gender":"m","pos":"n","translations":["Kind"],"targetGender":"n"}
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := dictionary.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestDictSearchVocabPrefix(t *testing.T) {
	srv := New(searchTestDict(t), &scriptedAI{}, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/api/dict/search?q=cas&kind=vocab&limit=10", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []dictionary.Card `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 2 {
		t.Fatalf("results %+v", resp.Results)
	}
	if resp.Results[0].Source != "casa" || resp.Results[1].Source != "casar" {
		t.Fatalf("order %+v", resp.Results)
	}
	if resp.Results[0].Target != "Haus" || !resp.Results[0].FoundInDict {
		t.Fatalf("casa card %+v", resp.Results[0])
	}
}

func TestDictSearchConjugationFiltersParadigms(t *testing.T) {
	srv := New(searchTestDict(t), &scriptedAI{}, t.TempDir())
	srv.Conj = conjugation.New(map[string]conjugation.Paradigm{
		"hablar": {Ger: "hablando", Ptcp: "hablado"},
		"comer":  {Ger: "comiendo"},
		// casar is a verb in the dictionary but has no paradigm here.
	})

	req := httptest.NewRequest(http.MethodGet, "/api/dict/search?q=c&kind=conjugation&limit=10", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Results []struct {
			Source   string `json:"source"`
			POS      string `json:"pos"`
			Paradigm struct {
				Ger string `json:"ger"`
			} `json:"paradigm"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Source != "comer" {
		t.Fatalf("results %+v", resp.Results)
	}
	if resp.Results[0].Paradigm.Ger != "comiendo" {
		t.Fatalf("paradigm %+v", resp.Results[0])
	}

	// Nouns must never appear for conjugation search.
	req = httptest.NewRequest(http.MethodGet, "/api/dict/search?q=casa&kind=conjugation", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 0 {
		t.Fatalf("noun leaked: %+v", resp.Results)
	}
}

func TestDictSearchEmptyQueryAndValidation(t *testing.T) {
	srv := New(searchTestDict(t), &scriptedAI{}, t.TempDir())

	req := httptest.NewRequest(http.MethodGet, "/api/dict/search?q=", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty q status %d", rec.Code)
	}
	var empty struct {
		Results []dictionary.Card `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil {
		t.Fatal(err)
	}
	if len(empty.Results) != 0 {
		t.Fatalf("empty results %+v", empty.Results)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/dict/search?q=casa&kind=nonsense", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad kind status %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/dict/search?q=hab&kind=conjugation", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing conj status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestDictSearchDiacriticSoftMatch(t *testing.T) {
	srv := New(searchTestDict(t), &scriptedAI{}, t.TempDir())
	req := httptest.NewRequest(http.MethodGet, "/api/dict/search?q=nino", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var resp struct {
		Results []dictionary.Card `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Results) != 1 || resp.Results[0].Source != "niño" {
		t.Fatalf("results %+v", resp.Results)
	}
}
