package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jakob/simple-vocabulary/internal/shared"
)

func TestSharePutAndGetStripsProgress(t *testing.T) {
	store, err := shared.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(testDict(t), nil, t.TempDir())
	srv.Shared = store

	body := `{
		"id":"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"title":"Café",
		"kind":"vocab",
		"difficulty":"easy",
		"createdAt":"2026-01-01T00:00:00Z",
		"turn":42,
		"cards":[{
			"id":"11111111-2222-3333-4444-555555555555",
			"source":"casa",
			"sourceArticle":"la",
			"target":"Haus",
			"targetArticle":"das",
			"translations":["Haus","Heim"],
			"pos":"n",
			"foundInDict":true,
			"exposure":{"seen":9,"lastTurn":40},
			"times":[1200,800,900],
			"glossChecked":true
		}]
	}`
	putReq := httptest.NewRequest(http.MethodPost, "/api/sets/share", strings.NewReader(body))
	putRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(putRec, putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("put status %d body %s", putRec.Code, putRec.Body.String())
	}

	var stored shared.Set
	if err := json.Unmarshal(putRec.Body.Bytes(), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.PublishedAt == "" {
		t.Fatal("expected publishedAt")
	}
	if len(stored.Cards) != 1 || stored.Cards[0].Source != "casa" {
		t.Fatalf("unexpected cards: %+v", stored.Cards)
	}
	raw := putRec.Body.String()
	for _, banned := range []string{`"turn"`, `"exposure"`, `"times"`, `"glossChecked"`} {
		if strings.Contains(raw, banned) {
			t.Fatalf("progress field %s leaked into share payload: %s", banned, raw)
		}
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/sets/share/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee", nil)
	getRec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get status %d body %s", getRec.Code, getRec.Body.String())
	}
	var loaded shared.Set
	if err := json.Unmarshal(getRec.Body.Bytes(), &loaded); err != nil {
		t.Fatal(err)
	}
	if loaded.Title != "Café" || loaded.Cards[0].Target != "Haus" {
		t.Fatalf("unexpected get payload: %+v", loaded)
	}
}

func TestShareGetMissing(t *testing.T) {
	store, err := shared.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(testDict(t), nil, t.TempDir())
	srv.Shared = store

	req := httptest.NewRequest(http.MethodGet, "/api/sets/share/missing-id", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestSharePutRejectsEmpty(t *testing.T) {
	store, err := shared.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := New(testDict(t), nil, t.TempDir())
	srv.Shared = store

	req := httptest.NewRequest(http.MethodPost, "/api/sets/share", strings.NewReader(`{"id":"ok-id","title":"x","kind":"vocab","cards":[]}`))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}
