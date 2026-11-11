package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jakob/simple-vocabulary/internal/ai"
	"github.com/jakob/simple-vocabulary/internal/conjugation"
	"github.com/jakob/simple-vocabulary/internal/dictionary"
)

type scriptedAI struct {
	batches [][]ai.WordSuggestion
	calls   []ai.GenerateRequest
}

func (s *scriptedAI) Name() string { return "script" }

func (s *scriptedAI) GenerateVocabulary(_ context.Context, req ai.GenerateRequest) ([]ai.WordSuggestion, error) {
	s.calls = append(s.calls, req)
	if len(s.batches) == 0 {
		return nil, errNotEnoughMatches
	}
	batch := s.batches[0]
	s.batches = s.batches[1:]
	return batch, nil
}

func testDict(t *testing.T) *dictionary.Dictionary {
	t.Helper()
	path := filepath.Join(t.TempDir(), "dict.json")
	raw := `{
		"sourceLang":"es","targetLang":"de",
		"entries":{
			"casa":{"lemma":"casa","gender":"f","pos":"n","translations":["Haus","Gebäude","Heim","Zuhause"],"targetGender":"n"},
			"perro":{"lemma":"perro","gender":"m","pos":"n","translations":["Hund"],"targetGender":"m"},
			"gato":{"lemma":"gato","gender":"m","pos":"n","translations":["Katze"],"targetGender":"f"},
			"libro":{"lemma":"libro","gender":"m","pos":"n","translations":["Buch"],"targetGender":"n"}
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

func TestGenerateKeepsExactDictionaryCount(t *testing.T) {
	stub := &scriptedAI{batches: [][]ai.WordSuggestion{{
		{Lemma: "casa", Translation: "Gebäude"},
		{Lemma: "inventado", Translation: "Erfunden"},
		{Lemma: "perro", Translation: "Hund falsch"},
		{Lemma: "otro", Translation: "Anderes"},
		{Lemma: "gato", Translation: "Katze falsch"},
		{Lemma: "libro", Translation: "Buch falsch"},
	}}}
	srv := New(testDict(t), stub, t.TempDir())
	req := httptest.NewRequest(http.MethodPost, "/api/sets/generate", strings.NewReader(`{"topic":"home","count":3}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Requested int               `json:"requested"`
		Cards     []dictionary.Card `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Requested != 3 || len(resp.Cards) != 3 {
		t.Fatalf("requested %d cards %d", resp.Requested, len(resp.Cards))
	}
	got := []string{resp.Cards[0].Target, resp.Cards[1].Target, resp.Cards[2].Target}
	// AI gloss "Gebäude" matches a dict sense and is promoted; unmatched AI
	// glosses keep FreeDict order ("Hund", "Katze").
	want := []string{"Gebäude", "Hund", "Katze"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("targets %v, want %v", got, want)
		}
		if !resp.Cards[i].FoundInDict {
			t.Fatalf("card %d not from dictionary", i)
		}
	}
	casa := resp.Cards[0].Translations
	if len(casa) != 3 || casa[0] != "Gebäude" || casa[1] != "Haus" || casa[2] != "Heim" {
		t.Fatalf("casa translations %v", casa)
	}
	if len(stub.calls) != 1 || stub.calls[0].Count != 13 {
		t.Fatalf("calls %+v", stub.calls)
	}
	if stub.calls[0].Difficulty != ai.DifficultyMedium {
		t.Fatalf("difficulty %q", stub.calls[0].Difficulty)
	}
}

func TestGenerateRefillsFromSpareRequest(t *testing.T) {
	stub := &scriptedAI{batches: [][]ai.WordSuggestion{
		{{Lemma: "casa", Translation: "Gebäude"}, {Lemma: "inventado", Translation: "Erfunden"}},
		{{Lemma: "perro"}, {Lemma: "gato"}, {Lemma: "libro"}},
	}}
	srv := New(testDict(t), stub, t.TempDir())

	cards, _, err := srv.dictionaryCards(context.Background(), "home", 3, ai.DifficultyMedium)
	if err != nil {
		t.Fatal(err)
	}
	if len(cards) != 3 {
		t.Fatalf("got %d cards", len(cards))
	}
	if cards[0].Source != "casa" || cards[1].Source != "perro" || cards[2].Source != "gato" {
		t.Fatalf("sources %+v", cards)
	}
	if len(stub.calls) != 2 {
		t.Fatalf("rounds %d", len(stub.calls))
	}
	if stub.calls[0].Count != 13 || stub.calls[1].Count != 12 {
		t.Fatalf("counts %d then %d", stub.calls[0].Count, stub.calls[1].Count)
	}
	if strings.Join(stub.calls[1].Exclude, ",") != "casa,inventado" {
		t.Fatalf("exclude %v", stub.calls[1].Exclude)
	}
}

func TestGenerateFailsWhenDictionaryCannotFillSet(t *testing.T) {
	misses := make([]ai.WordSuggestion, 0, 8)
	for _, lemma := range []string{"uno", "dos", "tres", "cuatro"} {
		misses = append(misses, ai.WordSuggestion{Lemma: lemma, Translation: "x"})
	}
	stub := &scriptedAI{batches: [][]ai.WordSuggestion{misses, misses, misses}}
	srv := New(testDict(t), stub, t.TempDir())

	_, _, err := srv.dictionaryCards(context.Background(), "nowhere", 2, "")
	if err != errNotEnoughMatches {
		t.Fatalf("err %v", err)
	}
}

func TestGenerateConjugationDropsNonVerbsAndMissingParadigms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dict.json")
	raw := `{
		"sourceLang":"es","targetLang":"de",
		"entries":{
			"casa":{"lemma":"casa","gender":"f","pos":"n","translations":["Haus"],"targetGender":"n"},
			"hablar":{"lemma":"hablar","pos":"v","translations":["sprechen"]},
			"comer":{"lemma":"comer","translations":["essen"]},
			"correr":{"lemma":"correr","pos":"v","translations":["laufen"]}
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	dict, err := dictionary.LoadJSON(path)
	if err != nil {
		t.Fatal(err)
	}
	stub := &scriptedAI{batches: [][]ai.WordSuggestion{{
		{Lemma: "casa"},
		{Lemma: "correr"},
		{Lemma: "inventado"},
		{Lemma: "comer"},
		{Lemma: "hablar"},
	}}}
	srv := New(dict, stub, t.TempDir())
	srv.Conj = conjugation.New(map[string]conjugation.Paradigm{
		"hablar": {Ger: "hablando", Ind: map[string][]string{"prs": {"hablo", "hablas", "habla", "hablamos", "habláis", "hablan"}}},
		"comer":  {Ger: "comiendo"},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/sets/generate", strings.NewReader(`{"topic":"reden","count":2,"kind":"conjugation","difficulty":"hard"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Kind       string `json:"kind"`
		Difficulty string `json:"difficulty"`
		Cards      []struct {
			Source   string `json:"source"`
			POS      string `json:"pos"`
			Paradigm struct {
				Ger string `json:"ger"`
			} `json:"paradigm"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Kind != "conjugation" || resp.Difficulty != ai.DifficultyHard || len(resp.Cards) != 2 || resp.Cards[0].Source != "comer" || resp.Cards[1].Source != "hablar" {
		t.Fatalf("resp %+v", resp)
	}
	if resp.Cards[1].Paradigm.Ger != "hablando" || resp.Cards[1].POS != "v" {
		t.Fatalf("paradigm %+v", resp.Cards[1])
	}
	if len(stub.calls) != 1 || stub.calls[0].Difficulty != ai.DifficultyHard || stub.calls[0].Kind != ai.KindConjugation {
		t.Fatalf("calls %+v", stub.calls)
	}
}

func TestConjParadigmsLookup(t *testing.T) {
	srv := New(testDict(t), &scriptedAI{}, t.TempDir())
	srv.Conj = conjugation.New(map[string]conjugation.Paradigm{
		"hablar": {Ger: "hablando", Ptcp: "hablado"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/conj/paradigms", strings.NewReader(`{"lemmas":["hablar","nada"]}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Paradigms map[string]conjugation.Paradigm `json:"paradigms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Paradigms["hablar"].Ptcp != "hablado" || resp.Paradigms["hablar"].Ger != "hablando" {
		t.Fatalf("paradigms %+v", resp.Paradigms)
	}
	if _, ok := resp.Paradigms["nada"]; ok {
		t.Fatal("unknown lemma returned")
	}
}

func TestGenerateRejectsUnknownDifficulty(t *testing.T) {
	srv := New(testDict(t), &scriptedAI{}, t.TempDir())
	req := httptest.NewRequest(http.MethodPost, "/api/sets/generate", strings.NewReader(`{"topic":"home","count":3,"difficulty":"brutal"}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}
