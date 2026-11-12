package accounts

import (
	"encoding/json"
	"errors"
	"testing"
)

var testInitial = json.RawMessage(`{"version":1,"sets":[{"id":"seed"}]}`)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return s
}

func TestNormalizeEmail(t *testing.T) {
	got, err := NormalizeEmail("  User@Example.COM ")
	if err != nil || got != "user@example.com" {
		t.Fatalf("NormalizeEmail = %q, %v", got, err)
	}
	for _, bad := range []string{"", "nope", "a@b@c", " spaces@x .de"} {
		if _, err := NormalizeEmail(bad); err == nil {
			t.Fatalf("NormalizeEmail(%q) should fail", bad)
		}
	}
}

func TestSignupVerifyLoginFlow(t *testing.T) {
	s := newStore(t)
	email := "ada@example.com"
	pw := "super-secret-pw"

	code, err := s.Signup(email, pw, nil)
	if err != nil {
		t.Fatalf("Signup: %v", err)
	}
	if len(code) != codeLen {
		t.Fatalf("code length = %d", len(code))
	}

	// Wrong code counts as an attempt and fails.
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if _, err := s.Verify(email, wrong, testInitial); !errors.Is(err, ErrBadCode) {
		t.Fatalf("Verify wrong code: %v", err)
	}
	// Login before verification is refused.
	if _, _, err := s.Login(email, pw); !errors.Is(err, ErrNotVerified) {
		t.Fatalf("Login unverified: %v", err)
	}

	acc, err := s.Verify(email, code, testInitial)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	var state struct {
		Sets []map[string]any `json:"sets"`
	}
	if err := json.Unmarshal(acc.State, &state); err != nil || len(state.Sets) != 1 {
		t.Fatalf("initial state not applied: %s", acc.State)
	}

	// Second signup for a verified email is rejected.
	if _, err := s.Signup(email, pw, nil); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("Signup verified email: %v", err)
	}
	if _, _, err := s.Login(email, "wrong-password"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("Login wrong password: %v", err)
	}
	if _, _, err := s.Login("nobody@example.com", pw); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("Login unknown email: %v", err)
	}

	_, token, err := s.Login(email, pw)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	if token == "" {
		t.Fatal("empty session token")
	}
	if _, err := s.AccountBySession(token); err != nil {
		t.Fatalf("AccountBySession: %v", err)
	}

	newState := json.RawMessage(`{"version":1,"sets":[{"id":"mine"}],"theme":"dark"}`)
	if _, err := s.PutState(token, newState, -1); err != nil {
		t.Fatalf("PutState: %v", err)
	}
	acc, err = s.AccountBySession(token)
	if err != nil {
		t.Fatalf("AccountBySession after PutState: %v", err)
	}
	var gotState map[string]any
	if err := json.Unmarshal(acc.State, &gotState); err != nil || gotState["theme"] != "dark" {
		t.Fatalf("state = %s", acc.State)
	}
	if _, err := s.PutState(token, json.RawMessage(`{"noSets":true}`), -1); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("PutState invalid: %v", err)
	}

	if err := s.Logout(token); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if _, err := s.AccountBySession(token); !errors.Is(err, ErrSession) {
		t.Fatalf("session should be gone: %v", err)
	}
}

func TestSignupPendingStateUsedOnVerify(t *testing.T) {
	s := newStore(t)
	email := "bob@example.com"
	pending := json.RawMessage(`{"version":1,"sets":[{"id":"local"}]}`)

	code, err := s.Signup(email, "password-123", pending)
	if err != nil {
		t.Fatalf("Signup: %v", err)
	}
	// Re-signup while pending replaces the code.
	code2, err := s.Signup(email, "password-456", pending)
	if err != nil {
		t.Fatalf("Signup pending again: %v", err)
	}
	if code == code2 {
		t.Fatal("code was not rotated")
	}
	if _, err := s.Verify(email, code, testInitial); !errors.Is(err, ErrBadCode) {
		t.Fatalf("old code should fail: %v", err)
	}
	acc, err := s.Verify(email, code2, testInitial)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	var gotState struct {
		Sets []map[string]any `json:"sets"`
	}
	if err := json.Unmarshal(acc.State, &gotState); err != nil || len(gotState.Sets) != 1 || gotState.Sets[0]["id"] != "local" {
		t.Fatalf("pending state not used: %s", acc.State)
	}
	// New password from the re-signup applies.
	if _, _, err := s.Login(email, "password-123"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("old password should fail: %v", err)
	}
	if _, _, err := s.Login(email, "password-456"); err != nil {
		t.Fatalf("new password: %v", err)
	}
}

func TestPutStateRevisionConflictAndNotify(t *testing.T) {
	s := newStore(t)
	email := "sync@example.com"
	pw := "super-secret-pw"

	code, err := s.Signup(email, pw, nil)
	if err != nil {
		t.Fatalf("Signup: %v", err)
	}
	acc, err := s.Verify(email, code, testInitial)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if acc.StateRev != 1 {
		t.Fatalf("StateRev after verify = %d, want 1", acc.StateRev)
	}
	_, token, err := s.Login(email, pw)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	events, unsubscribe := s.Subscribe(email)
	defer unsubscribe()

	rev, err := s.PutState(token, json.RawMessage(`{"version":1,"sets":[]}`), int64(acc.StateRev))
	if err != nil {
		t.Fatalf("PutState: %v", err)
	}
	if rev != acc.StateRev+1 {
		t.Fatalf("rev = %d, want %d", rev, acc.StateRev+1)
	}
	select {
	case ev := <-events:
		if ev.Rev != rev || ev.Src != SessionID(token) {
			t.Fatalf("event = %+v, want rev %d src %q", ev, rev, SessionID(token))
		}
	default:
		t.Fatal("subscriber was not notified")
	}

	// A write based on a stale revision is refused and reports the current one.
	got, err := s.PutState(token, json.RawMessage(`{"version":1,"sets":[]}`), int64(acc.StateRev))
	if !errors.Is(err, ErrStateConflict) {
		t.Fatalf("stale PutState: %v", err)
	}
	if got != rev {
		t.Fatalf("conflict rev = %d, want %d", got, rev)
	}
	// Without a base revision the write is unconditional.
	if _, err := s.PutState(token, json.RawMessage(`{"version":1,"sets":[]}`), -1); err != nil {
		t.Fatalf("unconditional PutState: %v", err)
	}
}

func TestResend(t *testing.T) {
	s := newStore(t)
	email := "carol@example.com"
	if _, err := s.Resend(email); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Resend unknown: %v", err)
	}
	code, err := s.Signup(email, "password-123", nil)
	if err != nil {
		t.Fatalf("Signup: %v", err)
	}
	code2, err := s.Resend(email)
	if err != nil {
		t.Fatalf("Resend: %v", err)
	}
	if code == code2 {
		t.Fatal("code was not rotated on resend")
	}
	if _, err := s.Verify(email, code2, testInitial); err != nil {
		t.Fatalf("Verify with resent code: %v", err)
	}
	if _, err := s.Resend(email); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("Resend verified: %v", err)
	}
}
