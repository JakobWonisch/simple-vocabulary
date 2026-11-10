package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/jakob/simple-vocabulary/internal/conjugation"
	"github.com/jakob/simple-vocabulary/internal/dictionary"
)

func repoData(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "data", name)
}

func TestDictSearchAgainstRealData(t *testing.T) {
	d, err := dictionary.LoadJSON(repoData(t, "spa-deu.json"))
	if err != nil {
		t.Fatal(err)
	}
	c, err := conjugation.LoadJSON(repoData(t, "spa-conj.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New(d, &scriptedAI{}, t.TempDir())
	srv.Conj = c

	req := httptest.NewRequest(http.MethodGet, "/api/dict/search?q=hablar&kind=vocab&limit=5", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	var vocab struct {
		Results []dictionary.Card `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &vocab); err != nil {
		t.Fatal(err)
	}
	if len(vocab.Results) == 0 || vocab.Results[0].Source != "hablar" {
		t.Fatalf("vocab %+v", vocab.Results)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/dict/search?q=hablar&kind=conjugation&limit=5", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("conj status %d %s", rec.Code, rec.Body.String())
	}
	var conj struct {
		Results []struct {
			Source   string                 `json:"source"`
			Paradigm map[string]interface{} `json:"paradigm"`
		} `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conj); err != nil {
		t.Fatal(err)
	}
	if len(conj.Results) == 0 || conj.Results[0].Source != "hablar" || len(conj.Results[0].Paradigm) == 0 {
		t.Fatalf("conj %+v", conj.Results)
	}

	// Noun lemma that does not prefix a conjugable verb.
	req = httptest.NewRequest(http.MethodGet, "/api/dict/search?q=libro&kind=conjugation", nil)
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if err := json.Unmarshal(rec.Body.Bytes(), &conj); err != nil {
		t.Fatal(err)
	}
	if len(conj.Results) != 0 {
		t.Fatalf("noun leaked %+v", conj.Results)
	}
}
