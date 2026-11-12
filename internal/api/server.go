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

	"github.com/jakob/simple-vocabulary/internal/accounts"
	"github.com/jakob/simple-vocabulary/internal/ai"
	"github.com/jakob/simple-vocabulary/internal/analytics"
	"github.com/jakob/simple-vocabulary/internal/conjugation"
	"github.com/jakob/simple-vocabulary/internal/dictionary"
	"github.com/jakob/simple-vocabulary/internal/mail"
	"github.com/jakob/simple-vocabulary/internal/shared"
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
	Dict      *dictionary.Dictionary
	Conj      *conjugation.Table
	AI        ai.Provider
	Shared    *shared.Store
	Accounts  *accounts.Store
	Mail      mail.Sender
	Analytics *analytics.Store
	// AdminPassword enables /admin (HTTP Basic auth) when non-empty.
	AdminPassword string
	WebDir        string
	InitialPath   string
	mux           *http.ServeMux
}

func New(dict *dictionary.Dictionary, provider ai.Provider, webDir string) *Server {
	s := &Server{Dict: dict, AI: provider, WebDir: webDir, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler {
	return withCORS(withLogging(s.withPageViews(s.mux)))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/dict/lookup", s.handleLookup)
	s.mux.HandleFunc("GET /api/dict/search", s.handleSearch)
	s.mux.HandleFunc("POST /api/conj/paradigms", s.handleConjParadigms)
	s.mux.HandleFunc("GET /api/initial", s.handleInitial)
	s.mux.HandleFunc("POST /api/sets/generate", s.handleGenerate)
	s.mux.HandleFunc("POST /api/sets/share", s.handleSharePut)
	s.mux.HandleFunc("GET /api/sets/share/{id}", s.handleShareGet)
	s.mux.HandleFunc("POST /api/auth/signup", s.handleSignup)
	s.mux.HandleFunc("POST /api/auth/verify", s.handleVerify)
	s.mux.HandleFunc("POST /api/auth/resend", s.handleResend)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("GET /api/auth/me", s.handleMe)
	s.mux.HandleFunc("GET /api/account/state", s.handleAccountStateGet)
	s.mux.HandleFunc("PUT /api/account/state", s.handleAccountStatePut)
	s.mux.HandleFunc("POST /api/account/state", s.handleAccountStatePut)
	s.mux.HandleFunc("POST /api/events", s.handleEvents)
	s.mux.HandleFunc("GET /admin", s.handleAdminPage)
	s.mux.HandleFunc("GET /api/admin/stats", s.handleAdminStats)
	s.mux.Handle("GET /", http.FileServer(http.Dir(s.WebDir)))
}

// handleInitial returns the seed configuration for browsers that have no
// saved state. It never writes client storage; the frontend applies it only
// when localStorage is empty.
func (s *Server) handleInitial(w http.ResponseWriter, r *http.Request) {
	if s.InitialPath == "" {
		http.NotFound(w, r)
		return
	}
	body, err := os.ReadFile(s.InitialPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
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

const (
	defaultSearchLimit = 20
	maxSearchLimit     = 50
)

// handleSearch returns card-ready dictionary matches for typeahead add-to-set.
// kind=conjugation keeps only verbs that also have a conjugation paradigm and
// includes that paradigm on each result.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind != "" && kind != "vocab" && kind != ai.KindConjugation {
		writeError(w, http.StatusBadRequest, "kind must be vocab or conjugation")
		return
	}
	limit := defaultSearchLimit
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = n
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
		return
	}

	if kind == ai.KindConjugation {
		if s.Conj == nil {
			writeError(w, http.StatusServiceUnavailable, "conjugation data is not loaded")
			return
		}
		cards := s.Dict.Search(q, limit, func(card dictionary.Card) bool {
			_, ok := s.Conj.Lookup(card.Source)
			return ok
		})
		results := make([]conjugationCard, 0, len(cards))
		for _, card := range cards {
			p, ok := s.Conj.Lookup(card.Source)
			if !ok {
				continue
			}
			results = append(results, conjugationCard{Card: card, Paradigm: p})
		}
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"results": s.Dict.Search(q, limit, nil),
	})
}

type conjParadigmsRequest struct {
	Lemmas []string `json:"lemmas"`
}

// handleConjParadigms returns current paradigms for the requested infinitives.
// Study sets store a copy at generation time; this lets them pick up forms
// added later, such as the past participle.
func (s *Server) handleConjParadigms(w http.ResponseWriter, r *http.Request) {
	var body conjParadigmsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	out := map[string]conjugation.Paradigm{}
	if s.Conj != nil {
		n := 0
		for _, lemma := range body.Lemmas {
			if n == 200 {
				break
			}
			lemma = strings.TrimSpace(lemma)
			if lemma == "" {
				continue
			}
			n++
			p, ok := s.Conj.Lookup(lemma)
			if !ok {
				continue
			}
			out[lemma] = p
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"paradigms": out})
}

type generateRequest struct {
	Topic      string `json:"topic"`
	Count      int    `json:"count"`
	Kind       string `json:"kind"`
	Difficulty string `json:"difficulty"`
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
		body.Count = 25
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

	difficulty, ok := ai.NormalizeDifficulty(body.Difficulty)
	if !ok {
		writeError(w, http.StatusBadRequest, "difficulty must be easy, medium, or hard")
		return
	}

	var (
		cards     any
		suggested int
		err       error
	)
	if kind == ai.KindConjugation {
		cards, suggested, err = s.conjugationCards(r.Context(), body.Topic, body.Count, difficulty)
	} else {
		cards, suggested, err = s.dictionaryCards(r.Context(), body.Topic, body.Count, difficulty)
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

	s.track(analytics.SetGenerated)
	writeJSON(w, http.StatusOK, map[string]any{
		"topic":      body.Topic,
		"kind":       kindOrVocab(kind),
		"difficulty": difficulty,
		"requested":  body.Count,
		"suggested":  suggested,
		"cards":      cards,
		"ai":         s.AI.Name(),
		"createdAt":  time.Now().UTC().Format(time.RFC3339),
	})
}

func kindOrVocab(kind string) string {
	if kind == ai.KindConjugation {
		return ai.KindConjugation
	}
	return "vocab"
}

func (s *Server) handleSharePut(w http.ResponseWriter, r *http.Request) {
	if s.Shared == nil {
		writeError(w, http.StatusServiceUnavailable, "sharing is not configured")
		return
	}
	var body shared.Set
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	set, err := s.Shared.Put(body)
	if err != nil {
		if errors.Is(err, shared.ErrInvalid) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("share put: %v", err)
		writeError(w, http.StatusInternalServerError, "could not store shared set")
		return
	}
	s.track(analytics.SetShared)
	writeJSON(w, http.StatusOK, set)
}

func (s *Server) handleShareGet(w http.ResponseWriter, r *http.Request) {
	if s.Shared == nil {
		writeError(w, http.StatusServiceUnavailable, "sharing is not configured")
		return
	}
	id := r.PathValue("id")
	set, err := s.Shared.Get(id)
	if err != nil {
		if errors.Is(err, shared.ErrNotFound) || errors.Is(err, shared.ErrInvalid) {
			writeError(w, http.StatusNotFound, "shared set not found")
			return
		}
		log.Printf("share get: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load shared set")
		return
	}
	s.track(analytics.SharedSetOpened)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, set)
}

// dictionaryCards asks the model for more lemmas than the set needs, keeps only
// dictionary headwords, and uses the spare suggestions to replace misses.
// Follow-up requests use the same spare when one batch cannot fill the set.
func (s *Server) dictionaryCards(ctx context.Context, topic string, want int, difficulty string) ([]dictionary.Card, int, error) {
	return s.fill(ctx, topic, want, "", difficulty, func(dictionary.Card) bool { return true })
}

func (s *Server) conjugationCards(ctx context.Context, topic string, want int, difficulty string) ([]conjugationCard, int, error) {
	raw, suggested, err := s.fill(ctx, topic, want, ai.KindConjugation, difficulty, func(card dictionary.Card) bool {
		if s.Conj == nil {
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

func (s *Server) fill(ctx context.Context, topic string, want int, kind, difficulty string, keep func(dictionary.Card) bool) ([]dictionary.Card, int, error) {
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
			Difficulty: difficulty,
		})
		if err != nil {
			return nil, suggested, err
		}
		suggested += len(batch)

		pairs := make([]dictionary.LemmaGloss, 0, len(batch))
		for _, sug := range batch {
			lemma := strings.TrimSpace(sug.Lemma)
			if lemma == "" {
				continue
			}
			exclude = append(exclude, lemma)
			pairs = append(pairs, dictionary.LemmaGloss{
				Lemma: lemma,
				Gloss: sug.Translation,
			})
		}

		for _, card := range s.Dict.EnrichLemmaGlosses(pairs) {
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
