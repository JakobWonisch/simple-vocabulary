package ai

import (
	"context"
	"strings"
)

// WordSuggestion is a lemma proposed by an AI provider.
// Gender and POS are accepted but not applied to cards. Translation is used only
// to pick among dictionary senses when it matches a FreeDict gloss; card text
// always comes from the dictionary.
type WordSuggestion struct {
	Lemma       string `json:"lemma"`
	Translation string `json:"translation,omitempty"`
	Gender      string `json:"gender,omitempty"` // m/f/n if known
	POS         string `json:"pos,omitempty"`
}

// KindConjugation asks the model for verb infinitives only.
const KindConjugation = "conjugation"

const (
	DifficultyEasy   = "easy"
	DifficultyMedium = "medium"
	DifficultyHard   = "hard"
)

// GenerateRequest describes a vocabulary generation job.
type GenerateRequest struct {
	Topic      string
	Count      int
	SourceLang string   // e.g. "es"
	TargetLang string   // e.g. "de"
	Exclude    []string // lemmas already proposed; must not be repeated
	Kind       string   // KindConjugation, or empty for a mixed vocabulary list
	Difficulty string   // DifficultyEasy, DifficultyMedium, or DifficultyHard
}

// NormalizeDifficulty maps a request value to a known level.
// An empty value is medium. The bool is false when the value is unknown.
func NormalizeDifficulty(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", DifficultyMedium:
		return DifficultyMedium, true
	case DifficultyEasy:
		return DifficultyEasy, true
	case DifficultyHard:
		return DifficultyHard, true
	default:
		return "", false
	}
}

// Provider generates vocabulary lemmas for a topic.
// Implementations must be swappable (OpenRouter today, local/other later).
type Provider interface {
	Name() string
	GenerateVocabulary(ctx context.Context, req GenerateRequest) ([]WordSuggestion, error)
}
