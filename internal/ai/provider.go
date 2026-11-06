package ai

import "context"

// WordSuggestion is a lemma proposed by an AI provider.
// Translation, gender, and part of speech are accepted from the model response
// but are not used for cards. Card text comes from the dictionary.
type WordSuggestion struct {
	Lemma       string `json:"lemma"`
	Translation string `json:"translation,omitempty"`
	Gender      string `json:"gender,omitempty"` // m/f/n if known
	POS         string `json:"pos,omitempty"`
}

// KindConjugation asks the model for verb infinitives only.
const KindConjugation = "conjugation"

// GenerateRequest describes a vocabulary generation job.
type GenerateRequest struct {
	Topic      string
	Count      int
	SourceLang string   // e.g. "es"
	TargetLang string   // e.g. "de"
	Exclude    []string // lemmas already proposed; must not be repeated
	Kind       string   // KindConjugation, or empty for a mixed vocabulary list
}

// Provider generates vocabulary lemmas for a topic.
// Implementations must be swappable (OpenRouter today, local/other later).
type Provider interface {
	Name() string
	GenerateVocabulary(ctx context.Context, req GenerateRequest) ([]WordSuggestion, error)
}
