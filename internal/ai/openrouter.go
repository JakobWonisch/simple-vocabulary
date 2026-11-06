package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultOpenRouterURL = "https://openrouter.ai/api/v1/chat/completions"

// OpenRouter is a Provider backed by OpenRouter's chat completions API.
type OpenRouter struct {
	APIKey     string
	Model      string
	HTTPClient *http.Client
	BaseURL    string
	SiteURL    string
	SiteName   string
}

// NewOpenRouterFromEnv builds a provider from environment variables.
// OPENROUTER_API_KEY is required; OPENROUTER_MODEL defaults to a free model.
func NewOpenRouterFromEnv() (*OpenRouter, error) {
	key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY"))
	if key == "" {
		return nil, fmt.Errorf("OPENROUTER_API_KEY is not set")
	}
	model := strings.TrimSpace(os.Getenv("OPENROUTER_MODEL"))
	if model == "" {
		model = "google/gemma-2-9b-it:free"
	}
	return &OpenRouter{
		APIKey: key,
		Model:  model,
		HTTPClient: &http.Client{
			Timeout: 90 * time.Second,
		},
		BaseURL:  defaultOpenRouterURL,
		SiteURL:  os.Getenv("OPENROUTER_SITE_URL"),
		SiteName: firstNonEmpty(os.Getenv("OPENROUTER_SITE_NAME"), "simple-vocabulary"),
	}, nil
}

func (o *OpenRouter) Name() string { return "openrouter:" + o.Model }

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

	prompt := fmt.Sprintf(`You are a vocabulary list generator for language learners.
Topic: %q
Produce exactly %d useful %s lemmas (base forms) related to that topic.
Prefer concrete, high-frequency words a beginner/intermediate learner would want.
Include a mix of nouns (with gender when known), verbs (infinitive), and adjectives when natural.

Respond with ONLY a JSON array of objects, no markdown, no commentary. Schema:
[{"lemma":"...","translation":"...","gender":"m|f|n|","pos":"n|v|adj|adv"}]
- lemma: word in %s
- translation: short gloss in %s (single word or short phrase)
- gender: only for nouns; omit or empty otherwise
- pos: part of speech`, req.Topic, req.Count, langName(req.SourceLang), langName(req.SourceLang), langName(req.TargetLang))

	body := map[string]any{
		"model": o.Model,
		"messages": []map[string]string{
			{"role": "user", "content": prompt},
		},
		"temperature": 0.4,
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
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openrouter %s: %s", resp.Status, truncate(string(raw), 400))
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
	if len(words) > req.Count {
		words = words[:req.Count]
	}
	return words, nil
}

func parseWordJSON(content string) ([]WordSuggestion, error) {
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	// Extract array if model wrapped it in prose.
	if i := strings.Index(content, "["); i >= 0 {
		if j := strings.LastIndex(content, "]"); j > i {
			content = content[i : j+1]
		}
	}

	var words []WordSuggestion
	if err := json.Unmarshal([]byte(content), &words); err != nil {
		// Try array of strings.
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
