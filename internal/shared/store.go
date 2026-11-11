// Package shared persists published word sets for link-based sharing.
// Only set content is stored — never study progress or timing stats.
package shared

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jakob/simple-vocabulary/internal/conjugation"
)

const (
	maxCards   = 500
	maxIDLen   = 80
	maxTitle   = 200
	maxWordLen = 200
)

var (
	ErrNotFound = errors.New("shared set not found")
	ErrInvalid  = errors.New("invalid shared set")
)

// Card is a shareable flashcard without progress fields.
type Card struct {
	ID            string                 `json:"id"`
	Source        string                 `json:"source"`
	SourceArticle string                 `json:"sourceArticle,omitempty"`
	Target        string                 `json:"target"`
	TargetArticle string                 `json:"targetArticle,omitempty"`
	Translations  []string               `json:"translations,omitempty"`
	POS           string                 `json:"pos,omitempty"`
	FoundInDict   bool                   `json:"foundInDict"`
	Paradigm      *conjugation.Paradigm  `json:"paradigm,omitempty"`
}

// Set is a published vocab or conjugation collection.
type Set struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Kind        string `json:"kind"`
	Difficulty  string `json:"difficulty,omitempty"`
	CreatedAt   string `json:"createdAt,omitempty"`
	PublishedAt string `json:"publishedAt"`
	Cards       []Card `json:"cards"`
}

// Store keeps shared sets as one JSON file per id under Dir.
type Store struct {
	Dir string
	mu  sync.Mutex
}

func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("shared store directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

func ValidID(id string) bool {
	id = strings.TrimSpace(id)
	if id == "" || len(id) > maxIDLen {
		return false
	}
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func (s *Store) pathFor(id string) (string, error) {
	if !ValidID(id) {
		return "", ErrInvalid
	}
	return filepath.Join(s.Dir, id+".json"), nil
}

// Put sanitizes and writes a shared set, keyed by set.ID.
func (s *Store) Put(raw Set) (Set, error) {
	clean, err := Sanitize(raw)
	if err != nil {
		return Set{}, err
	}
	clean.PublishedAt = time.Now().UTC().Format(time.RFC3339)

	path, err := s.pathFor(clean.ID)
	if err != nil {
		return Set{}, err
	}

	body, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return Set{}, err
	}
	body = append(body, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return Set{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return Set{}, err
	}
	return clean, nil
}

func (s *Store) Get(id string) (Set, error) {
	path, err := s.pathFor(id)
	if err != nil {
		return Set{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Set{}, ErrNotFound
		}
		return Set{}, err
	}
	var set Set
	if err := json.Unmarshal(body, &set); err != nil {
		return Set{}, err
	}
	if set.ID == "" {
		set.ID = id
	}
	return set, nil
}

// Sanitize keeps only shareable content and drops progress/stats fields.
func Sanitize(raw Set) (Set, error) {
	id := strings.TrimSpace(raw.ID)
	if !ValidID(id) {
		return Set{}, fmt.Errorf("%w: id", ErrInvalid)
	}
	title := strings.TrimSpace(raw.Title)
	if title == "" {
		return Set{}, fmt.Errorf("%w: title", ErrInvalid)
	}
	if len(title) > maxTitle {
		title = title[:maxTitle]
	}
	kind := strings.TrimSpace(raw.Kind)
	if kind == "" {
		kind = "vocab"
	}
	if kind != "vocab" && kind != "conjugation" {
		return Set{}, fmt.Errorf("%w: kind", ErrInvalid)
	}
	if len(raw.Cards) == 0 {
		return Set{}, fmt.Errorf("%w: cards empty", ErrInvalid)
	}
	if len(raw.Cards) > maxCards {
		return Set{}, fmt.Errorf("%w: too many cards", ErrInvalid)
	}

	difficulty := strings.TrimSpace(raw.Difficulty)
	switch difficulty {
	case "", "easy", "medium", "hard":
	default:
		difficulty = ""
	}

	cards := make([]Card, 0, len(raw.Cards))
	seen := map[string]struct{}{}
	for i, c := range raw.Cards {
		cardID := strings.TrimSpace(c.ID)
		if !ValidID(cardID) {
			cardID = fmt.Sprintf("card-%d", i+1)
		}
		if _, ok := seen[cardID]; ok {
			cardID = fmt.Sprintf("%s-%d", cardID, i+1)
		}
		seen[cardID] = struct{}{}

		source := clipWord(c.Source)
		target := clipWord(c.Target)
		if source == "" {
			continue
		}
		translations := cleanTranslations(c.Translations, target)
		var paradigm *conjugation.Paradigm
		if kind == "conjugation" && c.Paradigm != nil {
			p := *c.Paradigm
			paradigm = &p
		}
		cards = append(cards, Card{
			ID:            cardID,
			Source:        source,
			SourceArticle: clipWord(c.SourceArticle),
			Target:        target,
			TargetArticle: clipWord(c.TargetArticle),
			Translations:  translations,
			POS:           clipWord(c.POS),
			FoundInDict:   c.FoundInDict,
			Paradigm:      paradigm,
		})
	}
	if len(cards) == 0 {
		return Set{}, fmt.Errorf("%w: cards empty", ErrInvalid)
	}

	return Set{
		ID:         id,
		Title:      title,
		Kind:       kind,
		Difficulty: difficulty,
		CreatedAt:  strings.TrimSpace(raw.CreatedAt),
		Cards:      cards,
	}, nil
}

func clipWord(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxWordLen {
		return s[:maxWordLen]
	}
	return s
}

func cleanTranslations(raw []string, fallback string) []string {
	out := make([]string, 0, 3)
	seen := map[string]struct{}{}
	for _, item := range raw {
		text := clipWord(item)
		if text == "" {
			continue
		}
		key := strings.ToLower(text)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, text)
		if len(out) == 3 {
			break
		}
	}
	if len(out) == 0 && fallback != "" {
		out = append(out, fallback)
	}
	return out
}
