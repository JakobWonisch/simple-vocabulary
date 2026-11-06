package api

import (
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
	"github.com/jakob/simple-vocabulary/internal/dictionary"
)

// Server hosts the API and static frontend.
type Server struct {
	Dict     *dictionary.Dictionary
	AI       ai.Provider
	WebDir   string
	mux      *http.ServeMux
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

	ctx := r.Context()
	suggestions, err := s.AI.GenerateVocabulary(ctx, ai.GenerateRequest{
		Topic:      body.Topic,
		Count:      body.Count,
		SourceLang: s.Dict.SourceLang,
		TargetLang: s.Dict.TargetLang,
	})
	if err != nil {
		log.Printf("generate: %v", err)
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

	lemmas := make([]string, 0, len(suggestions))
	fallback := make(map[string]string, len(suggestions))
	for _, sug := range suggestions {
		lemmas = append(lemmas, sug.Lemma)
		if sug.Translation != "" {
			fallback[sug.Lemma] = sug.Translation
			fallback[strings.ToLower(sug.Lemma)] = sug.Translation
		}
	}

	cards := s.Dict.EnrichWords(lemmas, fallback)
	writeJSON(w, http.StatusOK, map[string]any{
		"topic":      body.Topic,
		"requested":  body.Count,
		"suggested":  len(suggestions),
		"cards":      cards,
		"ai":         s.AI.Name(),
		"createdAt":  time.Now().UTC().Format(time.RFC3339),
	})
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
