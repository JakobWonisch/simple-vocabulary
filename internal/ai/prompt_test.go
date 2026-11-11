package ai

import (
	"strings"
	"testing"
)

func TestConjugationPromptAsksForVerbsOnly(t *testing.T) {
	p := buildPrompt(GenerateRequest{Topic: "cocina", Count: 12, SourceLang: "es", Kind: KindConjugation})
	if !strings.Contains(p, "infinitive") || !strings.Contains(p, "No nouns") {
		t.Fatalf("verb prompt missing constraints:\n%s", p)
	}
	if strings.Contains(p, "mix of nouns") {
		t.Fatal("conjugation prompt reused the vocabulary prompt")
	}
	if !strings.Contains(p, `"pos":"v"`) {
		t.Fatal("expected verb schema")
	}
}

func TestVocabularyPromptStaysMixed(t *testing.T) {
	p := buildPrompt(GenerateRequest{Topic: "cocina", Count: 12, SourceLang: "es"})
	if !strings.Contains(p, "mix of nouns") {
		t.Fatal(p)
	}
	if !strings.Contains(p, "intermediate level") {
		t.Fatal(p)
	}
	if !strings.Contains(p, `"translation"`) {
		t.Fatal("vocab prompt should ask for a German gloss in the same response")
	}
}

func TestPromptDifficultyChangesLemmaLevel(t *testing.T) {
	easy := buildPrompt(GenerateRequest{Topic: "cocina", Count: 8, SourceLang: "es", Difficulty: DifficultyEasy})
	hard := buildPrompt(GenerateRequest{Topic: "cocina", Count: 8, SourceLang: "es", Kind: KindConjugation, Difficulty: DifficultyHard})
	if !strings.Contains(easy, "beginner") || strings.Contains(easy, "proficient learner") {
		t.Fatalf("easy prompt:\n%s", easy)
	}
	if !strings.Contains(hard, "proficient learner") || !strings.Contains(hard, "verbs") {
		t.Fatalf("hard prompt:\n%s", hard)
	}
	if strings.Contains(hard, "mix of nouns") {
		t.Fatal("hard conjugation prompt reused the vocabulary prompt")
	}
}
