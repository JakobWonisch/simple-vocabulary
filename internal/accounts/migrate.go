package accounts

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LegacyDir is where imported JSON account files are moved, as a backup.
const LegacyDir = "legacy-json"

type legacySessions struct {
	Sessions map[string]struct {
		Email   string    `json:"email"`
		Expires time.Time `json:"expires"`
	} `json:"sessions"`
}

// importLegacyJSON moves accounts from the old one-file-per-account layout
// (<sha256(email)>.json plus sessions.json) into SQLite, then moves the files
// to Dir/legacy-json. Existing rows win, so a rerun never overwrites newer
// data. Session tokens are kept so nobody is logged out by the migration.
func (s *Store) importLegacyJSON() error {
	files, err := filepath.Glob(filepath.Join(s.Dir, "*.json"))
	if err != nil || len(files) == 0 {
		return err
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var moved []string
	imported := 0
	sessionsPath := filepath.Join(s.Dir, "sessions.json")
	for _, path := range files {
		if path == sessionsPath {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		if len(name) != 64 {
			continue
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var acc Account
		if err := json.Unmarshal(body, &acc); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		if acc.Email == "" {
			log.Printf("accounts migration: skipping %s (no email)", filepath.Base(path))
			continue
		}
		if _, err := loadAccount(tx, acc.Email); err == nil {
			moved = append(moved, path)
			continue
		}
		// Last state save is the best available signal of activity.
		if acc.Verified {
			acc.LastSeenAt = acc.UpdatedAt
		}
		if err := upsertAccount(tx, &acc); err != nil {
			return fmt.Errorf("%s: %w", filepath.Base(path), err)
		}
		imported++
		moved = append(moved, path)
	}

	if body, err := os.ReadFile(sessionsPath); err == nil {
		var sf legacySessions
		if err := json.Unmarshal(body, &sf); err != nil {
			return fmt.Errorf("sessions.json: %w", err)
		}
		now := time.Now().UTC()
		for token, entry := range sf.Sessions {
			if now.After(entry.Expires) {
				continue
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO sessions (token_hash, email, created_at, expires)
				SELECT ?, email, ?, ? FROM users WHERE email = ?`,
				tokenHash(token), formatTime(entry.Expires.Add(-SessionTTL)), formatTime(entry.Expires), entry.Email); err != nil {
				return fmt.Errorf("sessions.json: %w", err)
			}
		}
		moved = append(moved, sessionsPath)
	}

	if err := tx.Commit(); err != nil {
		return err
	}

	backup := filepath.Join(s.Dir, LegacyDir)
	if err := os.MkdirAll(backup, 0o700); err != nil {
		return err
	}
	for _, path := range moved {
		if err := os.Rename(path, filepath.Join(backup, filepath.Base(path))); err != nil {
			return err
		}
	}
	if imported > 0 {
		log.Printf("accounts migration: imported %d JSON accounts into %s (originals in %s)", imported, DBFile, backup)
	}
	return nil
}
