package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultOpenRouterURL = "https://openrouter.ai/api/v1/chat/completions"

// Prefer fast instruction models. Heavy reasoning models (Ultra) are optional via env.
var defaultFreeModels = []string{
	"nvidia/nemotron-3.5-lightning:free",
	"google/gemma-4-26b-a4b-it:free",
	"google/gemma-4-31b-it:free",
	"xingchen-agi/xing4.0-29b-a4b:free",
}

// ErrRateLimited means every tried OpenRouter model was rate-limited.
var ErrRateLimited = errors.New("openrouter rate limited")

// OpenRouter is a Provider backed by OpenRouter's chat completions API.
type OpenRouter struct {
	APIKey          string
	Models          []string
	HTTPClient      *http.Client
	BaseURL         string
	SiteURL         string
	SiteName        string
	PerModelTimeout time.Duration
	MaxAttempts     int
}

// NewOpenRouterFromEnv builds a provider from environment variables.
func NewOpenRouterFromEnv() (*OpenRouter, error) {
	key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY is not set")
	}

	preferred := strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
	if preferred == "" {
		preferred = defaultFreeModels[0]
	}

	fallbacks := splitCSV(os.Getenv("OPENROUTER_FALLBACK_MODELS"))
	if len(fallbacks) == 0 {
		fallbacks = defaultFreeModels[1:]
	}

	perModel := 25 * time.Second
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_PER_MODEL_TIMEOUT_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			perModel = time.Duration(n) * time.Second
		}
	}

	maxAttempts := 3
	if v := strings.TrimSpace(os.Getenv("OPENROUTER_MAX_ATTEMPTS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxAttempts = n
		}
	}

	models := uniqModels(append([]string{preferred}, fallbacks...))
	if len(models) > maxAttempts {
		models = models[:maxAttempts]
	}

	return &OpenRouter{
		APIKey: key,
		Models: models,
		HTTPClient: &http.Client{
			// Overall client timeout; per-attempt uses a tighter context.
			Timeout: perModel + 5*time.Second,
		},
		BaseURL:         defaultOpenRouterURL,
		SiteURL:         os.Getenv("OPENROUTER_SITE_URL"),
		SiteName:        firstNonEmpty(os.Getenv("OPENROUTER_SITE_NAME"), "simple-vocabulary"),
		PerModelTimeout: perModel,
		MaxAttempts:     maxAttempts,
	}, nil
}

func (o *OpenRouter) Name() string {
	if len(o.Models) == 0 {
		return "openrouter"
	}
	return "openrouter:" + o.Models[0]
}

func (o *OpenRouter) GenerateVocabulary(ctx context.Context, req GenerateRequest) ([]WordSuggestion, error) {
	if req.Count <= 0 {
		req.Count = 50
	}
	if req.SourceLang == "" {
		req.SourceLang = "es"
	}
	if req.TargetLang == "" {
		req.TargetLang = "de"
	}
	if len(o.Models) == 0 {
		return nil, fmt.Errorf("no OpenRouter models configured")
	}

	prompt := buildPrompt(req)

	var lastErr error
	rateLimitedAll := true
	for i, model := range o.Models {
		log.Printf("openrouter: trying %s (%d/%d)", model, i+1, len(o.Models))
		attemptCtx, cancel := context.WithTimeout(ctx, o.PerModelTimeout)
		words, err := o.complete(attemptCtx, model, prompt, req.Count)
		cancel()
		if err == nil {
			if i > 0 {
				log.Printf("openrouter: succeeded with fallback model %s", model)
			}
			return words, nil
		}
		lastErr = err
		if errors.Is(attemptCtx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			log.Printf("openrouter: %s timed out after %s", model, o.PerModelTimeout)
			rateLimitedAll = false
			continue
		}
		if isRateLimited(err) {
			log.Printf("openrouter: %s rate-limited, trying next", model)
			continue
		}
		rateLimitedAll = false
		if isRetryable(err) {
			log.Printf("openrouter: %s failed (%v), trying next", model, err)
			continue
		}
		return nil, err
	}
	if rateLimitedAll {
		return nil, fmt.Errorf("%w: all models busy — retry in a minute, or set OPENROUTER_MODEL to another free/paid model", ErrRateLimited)
	}
	if lastErr != nil {
		return nil, fmt.Errorf("vocabulary generation failed after %d model(s): %w", len(o.Models), lastErr)
	}
	return nil, fmt.Errorf("vocabulary generation failed")
}

func (o *OpenRouter) complete(ctx context.Context, model, prompt string, count int) ([]WordSuggestion, error) {
	body := map[string]any{
		"model": model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.3,
		// Keep free reasoning models from burning the whole timeout on chain-of-thought.
		"reasoning": map[string]any{
			"effort": "low",
		},
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, o.BaseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+o.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")
	if o.SiteURL != "" {
		httpReq.Header.Set("HTTP-Referer", o.SiteURL)
	}
	if o.SiteName != "" {
		httpReq.Header.Set("X-Title", o.SiteName)
	}

	resp, err := o.HTTPClient.Do(httpReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if resp.StatusCode >= 300 {
		msg := truncate(string(raw), 400)
		err := fmt.Errorf("openrouter %s (%s): %s", resp.Status, model, msg)
		if resp.StatusCode == http.StatusTooManyRequests || looksRateLimited(msg) {
			return nil, fmt.Errorf("%w: %v", ErrRateLimited, err)
		}
		return nil, err
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("decode openrouter response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("openrouter returned no choices")
	}
	content := strings.TrimSpace(parsed.Choices[0].Message.Content)
	words, err := parseWordJSON(content)
	if err != nil {
		return nil, err
	}
	if len(words) > count {
		words = words[:count]
	}
	return words, nil
}

func parseWordJSON(content string) ([]WordSuggestion, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	if i := strings.Index(content, "["); i >= 0 {
		if j := strings.LastIndex(content, "]"); j > i {
			content = content[i : j+1]
		}
	}

	var words []WordSuggestion
	if err := json.Unmarshal([]byte(content), &words); err != nil {
		var lemmas []string
		if err2 := json.Unmarshal([]byte(content), &lemmas); err2 != nil {
			return nil, fmt.Errorf("parse vocabulary JSON: %w (content: %s)", err, truncate(content, 200))
		}
		for _, l := range lemmas {
			l = strings.TrimSpace(l)
			if l != "" {
				words = append(words, WordSuggestion{Lemma: l})
			}
		}
	}

	out := make([]WordSuggestion, 0, len(words))
	seen := map[string]struct{}{}
	for _, w := range words {
		w.Lemma = strings.TrimSpace(w.Lemma)
		if w.Lemma == "" {
			continue
		}
		key := strings.ToLower(w.Lemma)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, w)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no vocabulary words parsed from model output")
	}
	return out, nil
}

func isRateLimited(err error) bool {
	return errors.Is(err, ErrRateLimited) || looksRateLimited(err.Error())
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, " 502") ||
		strings.Contains(msg, " 503") ||
		strings.Contains(msg, " 504") ||
		strings.Contains(msg, "bad gateway") ||
		strings.Contains(msg, "unavailable") ||
		errors.Is(err, context.DeadlineExceeded)
}

func looksRateLimited(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "rate-limited") ||
		strings.Contains(m, "rate limited") ||
		strings.Contains(m, "too many requests") ||
		strings.Contains(m, `"code":429`)
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func uniqModels(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, m := range in {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	return out
}

func buildPrompt(req GenerateRequest) string {
	lang := langName(req.SourceLang)
	var prompt string
	if req.Kind == KindConjugation {
		prompt = fmt.Sprintf(`You are a vocabulary list generator for language learners.
Topic: %q
Produce exactly %d useful %s verbs related to that topic.
Every item must be one verb in the infinitive (citation form ending in -ar, -er, or -ir). No nouns, no adjectives, no phrases, and no conjugated forms.
Do not attach reflexive pronouns (enviar, not enviarse).
Prefer concrete, high-frequency verbs a beginner or intermediate learner would use for that topic.

Respond with ONLY a JSON array of objects, no markdown, no commentary. Schema:
[{"lemma":"...","pos":"v"}]
- lemma: %s infinitive
- pos: always v`, req.Topic, req.Count, lang, lang)
	} else {
		prompt = fmt.Sprintf(`You are a vocabulary list generator for language learners.
Topic: %q
Produce exactly %d useful %s lemmas (base forms) related to that topic.
Prefer concrete, high-frequency words a beginner/intermediate learner would want.
Each lemma must be a single dictionary headword in citation form: singular noun, infinitive verb, or base adjective. No phrases and no inflected forms.
Include a mix of nouns (with gender when known), verbs (infinitive), and adjectives when natural.

Respond with ONLY a JSON array of objects, no markdown, no commentary. Schema:
[{"lemma":"...","gender":"m|f|n|","pos":"n|v|adj|adv"}]
- lemma: word in %s
- gender: only for nouns; omit or empty otherwise
- pos: part of speech`, req.Topic, req.Count, lang, lang)
	}
	if len(req.Exclude) > 0 {
		prompt += "\nDo not include any of these lemmas: " + strings.Join(req.Exclude, ", ")
	}
	return prompt
}

func langName(code string) string {
	switch strings.ToLower(code) {
	case "es", "spa", "spanish":
		return "Spanish"
	case "de", "deu", "german":
		return "German"
	default:
		return code
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
