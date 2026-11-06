package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jakob/simple-vocabulary/internal/ai"
	"github.com/jakob/simple-vocabulary/internal/conjugation"
	"github.com/jakob/simple-vocabulary/internal/dictionary"
)

const (
	// spareLemmas is how many extra lemmas are requested so dictionary misses
	// can be replaced without shrinking the set.
	spareLemmas = 10
	// maxFillRounds caps follow-up requests when the spare lemmas are not enough.
	maxFillRounds = 3
)

// errNotEnoughMatches means the model did not yield enough dictionary headwords
// to fill the requested set.
var errNotEnoughMatches = errors.New("not enough dictionary matches")

// Server hosts the API and static frontend.
type Server struct {
	Dict   *dictionary.Dictionary
	Conj   *conjugation.Table
	AI     ai.Provider
	WebDir string
	mux    *http.ServeMux
}

func New(dict *dictionary.Dictionary, provider ai.Provider, webDir string) *Server {
	s := &Server{Dict: dict, AI: provider, WebDir: webDir, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return withCORS(withLogging(s.mux))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/dict/lookup", s.handleLookup)
	s.mux.HandleFunc("POST /api/sets/generate", s.handleGenerate)
	s.mux.Handle("GET /", http.FileServer(http.Dir(s.WebDir)))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	aiName := "disabled"
	if s.AI != nil {
		aiName = s.AI.Name()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"dictionary": s.Dict.Size(),
		"sourceLang": s.Dict.SourceLang,
		"targetLang": s.Dict.TargetLang,
		"ai":         aiName,
	})
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "missing q")
		return
	}
	dir := r.URL.Query().Get("dir")
	var (
		entry dictionary.Entry
		ok    bool
	)
	if dir == "de-es" {
		entry, ok = s.Dict.LookupReverse(q)
	} else {
		entry, ok = s.Dict.Lookup(q)
	}
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

type generateRequest struct {
	Topic string `json:"topic"`
	Count int    `json:"count"`
	Kind  string `json:"kind"`
}

// conjugationCard is a dictionary verb plus the paradigm the study view quizzes.
type conjugationCard struct {
	dictionary.Card
	Paradigm conjugation.Paradigm `json:"paradigm"`
}

func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	if s.AI == nil {
		writeError(w, http.StatusServiceUnavailable, "AI provider not configured (set OPENROUTER_API_KEY)")
		return
	}
	var body generateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	body.Topic = strings.TrimSpace(body.Topic)
	if body.Topic == "" {
		writeError(w, http.StatusBadRequest, "topic is required")
		return
	}
	if body.Count <= 0 {
		body.Count = 50
	}
	if body.Count > 100 {
		body.Count = 100
	}

	kind := ""
	if body.Kind == ai.KindConjugation {
		kind = ai.KindConjugation
		if s.Conj == nil {
			writeError(w, http.StatusServiceUnavailable, "conjugation data is not loaded")
			return
		}
	} else if body.Kind != "" && body.Kind != "vocab" {
		writeError(w, http.StatusBadRequest, "kind must be vocab or conjugation")
		return
	}

	var (
		cards     any
		suggested int
		err       error
	)
	if kind == ai.KindConjugation {
		cards, suggested, err = s.conjugationCards(r.Context(), body.Topic, body.Count)
	} else {
		cards, suggested, err = s.dictionaryCards(r.Context(), body.Topic, body.Count)
	}
	if err != nil {
		log.Printf("generate: %v", err)
		if errors.Is(err, errNotEnoughMatches) {
			msg := "Not enough of the suggested words are in the dictionary. Try a broader topic."
			if kind == ai.KindConjugation {
				msg = "Not enough of the suggested verbs are in the dictionary. Try a broader topic."
			}
			writeError(w, http.StatusBadGateway, msg)
			return
		}
		msg := "vocabulary generation failed"
		status := http.StatusBadGateway
		if errors.Is(err, ai.ErrRateLimited) {
			status = http.StatusTooManyRequests
			msg = "AI providers are rate-limited right now. Wait a minute and retry, or set OPENROUTER_MODEL to another model."
		} else if strings.Contains(strings.ToLower(err.Error()), "rate") {
			status = http.StatusTooManyRequests
			msg = "AI rate limit hit. Retry shortly, or change OPENROUTER_MODEL."
		}
		writeError(w, status, msg)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"topic":     body.Topic,
		"kind":      kindOrVocab(kind),
		"requested": body.Count,
		"suggested": suggested,
		"cards":     cards,
		"ai":        s.AI.Name(),
		"createdAt": time.Now().UTC().Format(time.RFC3339),
	})
}

func kindOrVocab(kind string) string {
	if kind == ai.KindConjugation {
		return ai.KindConjugation
	}
	return "vocab"
}

// dictionaryCards asks the model for more lemmas than the set needs, keeps only
// dictionary headwords, and uses the spare suggestions to replace misses.
// Follow-up requests use the same spare when one batch cannot fill the set.
func (s *Server) dictionaryCards(ctx context.Context, topic string, want int) ([]dictionary.Card, int, error) {
	return s.fill(ctx, topic, want, "", func(dictionary.Card) bool { return true })
}

func (s *Server) conjugationCards(ctx context.Context, topic string, want int) ([]conjugationCard, int, error) {
	raw, suggested, err := s.fill(ctx, topic, want, ai.KindConjugation, func(card dictionary.Card) bool {
		if s.Conj == nil || (card.POS != "" && card.POS != "v") {
			return false
		}
		_, ok := s.Conj.Lookup(card.Source)
		return ok
	})
	if err != nil {
		return nil, suggested, err
	}
	cards := make([]conjugationCard, 0, len(raw))
	for _, card := range raw {
		p, ok := s.Conj.Lookup(card.Source)
		if !ok {
			continue
		}
		cards = append(cards, conjugationCard{Card: card, Paradigm: p})
	}
	if len(cards) < want {
		return nil, suggested, errNotEnoughMatches
	}
	return cards, suggested, nil
}

func (s *Server) fill(ctx context.Context, topic string, want int, kind string, keep func(dictionary.Card) bool) ([]dictionary.Card, int, error) {
	cards := make([]dictionary.Card, 0, want)
	seen := map[string]struct{}{}
	var exclude []string
	suggested := 0

	for round := 0; round < maxFillRounds && len(cards) < want; round++ {
		ask := want - len(cards) + spareLemmas
		batch, err := s.AI.GenerateVocabulary(ctx, ai.GenerateRequest{
			Topic:      topic,
			Count:      ask,
			SourceLang: s.Dict.SourceLang,
			TargetLang: s.Dict.TargetLang,
			Exclude:    exclude,
			Kind:       kind,
		})
		if err != nil {
			return nil, suggested, err
		}
		suggested += len(batch)

		lemmas := make([]string, 0, len(batch))
		for _, sug := range batch {
			lemma := strings.TrimSpace(sug.Lemma)
			if lemma == "" {
				continue
			}
			exclude = append(exclude, lemma)
			lemmas = append(lemmas, lemma)
		}

		for _, card := range s.Dict.EnrichWords(lemmas) {
			if !keep(card) {
				continue
			}
			key := strings.ToLower(strings.TrimSpace(card.Source))
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			cards = append(cards, card)
			if len(cards) == want {
				break
			}
		}
	}

	if len(cards) < want {
		return nil, suggested, errNotEnoughMatches
	}
	return cards, suggested, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}

// AddrFromEnv returns listen address, default :8080.
func AddrFromEnv() string {
	if p := os.Getenv("PORT"); p != "" {
		if strings.HasPrefix(p, ":") {
			return p
		}
		if _, err := strconv.Atoi(p); err == nil {
			return ":" + p
		}
		return p
	}
	return ":8080"
}

// ResolvePath joins relative paths against an optional base (e.g. repo root).
func ResolvePath(base, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(base, p)
}
