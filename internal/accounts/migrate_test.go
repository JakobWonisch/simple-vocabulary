package accounts

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeLegacy(t *testing.T, dir string, acc Account) {
	t.Helper()
	body, err := json.Marshal(acc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, tokenHash(acc.Email)+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestImportLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	hash, err := newPasswordHash("password-123")
	if err != nil {
		t.Fatal(err)
	}
	updated := time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)
	writeLegacy(t, dir, Account{
		Email: "old@example.com", PasswordHash: hash, Verified: true,
		State:     json.RawMessage(`{"version":1,"sets":[{"id":"a"},{"id":"b"}]}`),
		CreatedAt: updated.Add(-24 * time.Hour), UpdatedAt: updated,
	})
	writeLegacy(t, dir, Account{
		Email: "pending@example.com", PasswordHash: hash, VerifyCode: "123456",
		VerifyExpires: time.Now().Add(time.Hour), CreatedAt: updated, UpdatedAt: updated,
	})
	sessions := `{"sessions":{
		"live-token":{"email":"old@example.com","expires":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"},
		"dead-token":{"email":"old@example.com","expires":"2000-01-01T00:00:00Z"}}}`
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), []byte(sessions), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := NewStore(dir)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	acc, err := s.AccountBySession("live-token")
	if err != nil {
		t.Fatalf("migrated session: %v", err)
	}
	if acc.Email != "old@example.com" || !acc.Verified || !acc.CreatedAt.Equal(updated.Add(-24*time.Hour)) {
		t.Fatalf("migrated account = %+v", acc)
	}
	if _, err := s.AccountBySession("dead-token"); !errors.Is(err, ErrSession) {
		t.Fatalf("expired session should not migrate: %v", err)
	}
	if _, _, err := s.Login("old@example.com", "password-123"); err != nil {
		t.Fatalf("login with migrated password: %v", err)
	}
	if _, err := s.Verify("pending@example.com", "123456", testInitial); err != nil {
		t.Fatalf("verify migrated pending signup: %v", err)
	}

	left, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(left) != 0 {
		t.Fatalf("legacy files not moved: %v", left)
	}
	backup, _ := filepath.Glob(filepath.Join(dir, LegacyDir, "*.json"))
	if len(backup) != 3 {
		t.Fatalf("backup files = %v", backup)
	}
	s.Close()

	// Reopening is a no-op.
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	st, err := s2.Stats(time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if st.Users != 2 || st.Unverified != 0 {
		t.Fatalf("stats after reopen = %+v", st)
	}
}

func TestStats(t *testing.T) {
	s := newStore(t)
	for i, email := range []string{"a@example.com", "b@example.com", "c@example.com"} {
		code, err := s.Signup(email, "password-123", nil)
		if err != nil {
			t.Fatal(err)
		}
		if i == 2 {
			continue // stays unverified
		}
		if _, err := s.Verify(email, code, testInitial); err != nil {
			t.Fatal(err)
		}
	}
	_, token, err := s.Login("a@example.com", "password-123")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutState(token, json.RawMessage(`{"sets":[1,2,3,4,5]}`)); err != nil {
		t.Fatal(err)
	}
	// b was last seen long ago.
	if _, err := s.db.Exec(`UPDATE users SET last_seen_at = '2020-01-01 00:00:00' WHERE email = 'b@example.com'`); err != nil {
		t.Fatal(err)
	}

	st, err := s.Stats(time.Now().Add(-7 * 24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	want := Stats{Users: 2, Unverified: 1, Active: 1, Inactive: 1, New: 2,
		Collections: &CollectionStats{Avg: 3, Median: 3, Min: 1, Max: 5}}
	if st.Users != want.Users || st.Unverified != want.Unverified || st.Active != want.Active ||
		st.Inactive != want.Inactive || st.New != want.New || *st.Collections != *want.Collections {
		t.Fatalf("Stats = %+v %+v", st, st.Collections)
	}
}
