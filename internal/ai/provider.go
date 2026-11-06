package ai

import "context"

// WordSuggestion is a lemma proposed by an AI provider, optionally with a gloss.
type WordSuggestion struct {
	Lemma       string `json:"lemma"`
	Translation string `json:"translation,omitempty"` // optional fallback gloss
	Gender      string `json:"gender,omitempty"`      // m/f/n if known
	POS         string `json:"pos,omitempty"`
}

// GenerateRequest describes a vocabulary generation job.
type GenerateRequest struct {
	Topic      string
	Count      int
	SourceLang string // e.g. "es"
	TargetLang string // e.g. "de"
}

// Provider generates vocabulary lemmas for a topic.
// Implementations must be swappable (OpenRouter today, local/other later).
type Provider interface {
	Name() string
	GenerateVocabulary(ctx context.Context, req GenerateRequest) ([]WordSuggestion, error)
}
