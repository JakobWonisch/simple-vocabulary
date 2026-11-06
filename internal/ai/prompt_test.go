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
}
