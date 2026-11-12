// Package accounts persists user accounts: credentials, email verification,
// sessions, and each account's complete application state. One JSON file per
// account under Dir, plus a sessions index mapping token to email.
package accounts

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	MinPasswordLen = 8
	maxPasswordLen = 256
	pbkdf2Iter     = 210_000
	pbkdf2KeyLen   = 32
	saltLen        = 16
	codeLen        = 6

	VerifyCodeTTL     = 30 * time.Minute
	MaxVerifyAttempts = 10
	SessionTTL        = 30 * 24 * time.Hour
	MaxStateBytes     = 8 << 20
)

var (
	ErrNotFound         = errors.New("account not found")
	ErrEmailTaken       = errors.New("email already registered")
	ErrBadCredentials   = errors.New("wrong email or password")
	ErrNotVerified      = errors.New("email not verified")
	ErrBadCode          = errors.New("invalid verification code")
	ErrCodeExpired      = errors.New("verification code expired")
	ErrAttemptsExceeded = errors.New("too many code attempts")
	ErrSession          = errors.New("not logged in")
	ErrInvalidState     = errors.New("invalid state")
	ErrStateTooLarge    = errors.New("state too large")
)

// Account is one user's credentials plus the full frontend state blob.
type Account struct {
	Email          string          `json:"email"`
	PasswordHash   string          `json:"passwordHash"`
	Verified       bool            `json:"verified"`
	VerifyCode     string          `json:"verifyCode,omitempty"`
	VerifyExpires  time.Time       `json:"verifyExpires,omitempty"`
	VerifyAttempts int             `json:"verifyAttempts,omitempty"`
	PendingState   json.RawMessage `json:"pendingState,omitempty"`
	State          json.RawMessage `json:"state,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
}

// Store keeps accounts as one JSON file per normalized email under Dir.
type Store struct {
	Dir string
	mu  sync.Mutex
}

func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("accounts store directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{Dir: dir}, nil
}

// NormalizeEmail trims, lowercases, and validates an email address.
func NormalizeEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if len(email) > 254 {
		return "", fmt.Errorf("invalid email")
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Address != email {
		return "", fmt.Errorf("invalid email")
	}
	return email, nil
}

func ValidatePassword(pw string) error {
	if len(pw) < MinPasswordLen {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLen)
	}
	if len(pw) > maxPasswordLen {
		return fmt.Errorf("password too long")
	}
	return nil
}

// ValidateState accepts the frontend state blob: a JSON object whose sets
// field is an array (mirroring the client's import validation).
func ValidateState(raw json.RawMessage) error {
	if len(raw) > MaxStateBytes {
		return ErrStateTooLarge
	}
	var probe struct {
		Sets json.RawMessage `json:"sets"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return ErrInvalidState
	}
	var sets []json.RawMessage
	if err := json.Unmarshal(probe.Sets, &sets); err != nil {
		return ErrInvalidState
	}
	return nil
}

func hashPassword(pw string, salt []byte) string {
	key, _ := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iter, pbkdf2KeyLen)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s",
		pbkdf2Iter,
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(key))
}

func newPasswordHash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	return hashPassword(pw, salt), nil
}

func checkPassword(pw, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	var iter int
	if _, err := fmt.Sscanf(parts[1], "%d", &iter); err != nil || iter < 1 {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func newCode() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	n := int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	if n < 0 {
		n = -n
	}
	return fmt.Sprintf("%0*d", codeLen, n%1_000_000)
}

func newToken() string {
	var b [32]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// pathFor derives the account file name from the normalized email so no
// address appears on disk and no separate email index is needed.
func (s *Store) pathFor(email string) string {
	sum := sha256.Sum256([]byte(email))
	return filepath.Join(s.Dir, hex.EncodeToString(sum[:])+".json")
}

func (s *Store) sessionsPath() string {
	return filepath.Join(s.Dir, "sessions.json")
}

// load returns the account for email, or ErrNotFound.
// Callers must hold s.mu.
func (s *Store) load(email string) (*Account, error) {
	body, err := os.ReadFile(s.pathFor(email))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var acc Account
	if err := json.Unmarshal(body, &acc); err != nil {
		return nil, err
	}
	if acc.Email == "" {
		acc.Email = email
	}
	return &acc, nil
}

// write persists the account atomically. Callers must hold s.mu.
func (s *Store) write(acc *Account) error {
	acc.UpdatedAt = time.Now().UTC()
	body, err := json.MarshalIndent(acc, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	path := s.pathFor(acc.Email)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

type sessionEntry struct {
	Email   string    `json:"email"`
	Expires time.Time `json:"expires"`
}

type sessionsFile struct {
	Sessions map[string]sessionEntry `json:"sessions"`
}

func (s *Store) loadSessions() (*sessionsFile, error) {
	sf := &sessionsFile{Sessions: map[string]sessionEntry{}}
	body, err := os.ReadFile(s.sessionsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return sf, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(body, sf); err != nil {
		return nil, err
	}
	if sf.Sessions == nil {
		sf.Sessions = map[string]sessionEntry{}
	}
	return sf, nil
}

func (s *Store) writeSessions(sf *sessionsFile) error {
	body, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	path := s.sessionsPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// Signup creates or refreshes an unverified account and returns the
// verification code to send. A pending signup can be redone (new password,
// new code); a verified email is rejected with ErrEmailTaken.
func (s *Store) Signup(email, password string, pendingState json.RawMessage) (string, error) {
	hash, err := newPasswordHash(password)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	acc, err := s.load(email)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return "", err
		}
		acc = &Account{Email: email, CreatedAt: now}
	} else if acc.Verified {
		return "", ErrEmailTaken
	}

	acc.PasswordHash = hash
	acc.PendingState = pendingState
	acc.VerifyCode = newCode()
	acc.VerifyExpires = now.Add(VerifyCodeTTL)
	acc.VerifyAttempts = 0
	acc.Verified = false
	if err := s.write(acc); err != nil {
		return "", err
	}
	return acc.VerifyCode, nil
}

// Resend issues a fresh code for an unverified account.
func (s *Store) Resend(email string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	acc, err := s.load(email)
	if err != nil {
		return "", err
	}
	if acc.Verified {
		return "", ErrEmailTaken
	}
	acc.VerifyCode = newCode()
	acc.VerifyExpires = time.Now().UTC().Add(VerifyCodeTTL)
	acc.VerifyAttempts = 0
	if err := s.write(acc); err != nil {
		return "", err
	}
	return acc.VerifyCode, nil
}

// Verify checks the code and activates the account. The account's state is
// the pending local state when the user opted in at signup, else the server
// initial seed passed by the caller.
func (s *Store) Verify(email, code string, initialState json.RawMessage) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	acc, err := s.load(email)
	if err != nil {
		return nil, err
	}
	if acc.Verified {
		return nil, ErrEmailTaken
	}
	if acc.VerifyAttempts >= MaxVerifyAttempts {
		return nil, ErrAttemptsExceeded
	}
	code = strings.TrimSpace(code)
	match := len(code) == len(acc.VerifyCode) && code != "" &&
		subtle.ConstantTimeCompare([]byte(code), []byte(acc.VerifyCode)) == 1
	if !match {
		acc.VerifyAttempts++
		_ = s.write(acc)
		return nil, ErrBadCode
	}
	if time.Now().UTC().After(acc.VerifyExpires) {
		return nil, ErrCodeExpired
	}

	acc.Verified = true
	acc.VerifyCode = ""
	acc.VerifyAttempts = 0
	if len(acc.PendingState) > 0 {
		acc.State = acc.PendingState
	} else {
		acc.State = initialState
	}
	acc.PendingState = nil
	if err := s.write(acc); err != nil {
		return nil, err
	}
	return acc, nil
}

// Login checks credentials and starts a session. ErrNotVerified means the
// account exists but has not passed email verification yet.
func (s *Store) Login(email, password string) (*Account, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	acc, err := s.load(email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, "", ErrBadCredentials
		}
		return nil, "", err
	}
	if !acc.Verified {
		return nil, "", ErrNotVerified
	}
	if !checkPassword(password, acc.PasswordHash) {
		return nil, "", ErrBadCredentials
	}

	token, err := s.addSession(email)
	if err != nil {
		return nil, "", err
	}
	return acc, token, nil
}

// addSession stores a new session token. Callers must hold s.mu.
func (s *Store) addSession(email string) (string, error) {
	sf, err := s.loadSessions()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	for token, entry := range sf.Sessions {
		if now.After(entry.Expires) {
			delete(sf.Sessions, token)
		}
	}
	token := newToken()
	sf.Sessions[token] = sessionEntry{Email: email, Expires: now.Add(SessionTTL)}
	if err := s.writeSessions(sf); err != nil {
		return "", err
	}
	return token, nil
}

// sessionEmail resolves a token to its account email, pruning expired entries.
// Callers must hold s.mu.
func (s *Store) sessionEmail(token string) (string, error) {
	sf, err := s.loadSessions()
	if err != nil {
		return "", err
	}
	entry, ok := sf.Sessions[token]
	if !ok {
		return "", ErrSession
	}
	if time.Now().UTC().After(entry.Expires) {
		delete(sf.Sessions, token)
		_ = s.writeSessions(sf)
		return "", ErrSession
	}
	return entry.Email, nil
}

// NewSession starts a session for a verified account, e.g. right after
// email verification where no password was entered.
func (s *Store) NewSession(email string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	acc, err := s.load(email)
	if err != nil {
		return "", err
	}
	if !acc.Verified {
		return "", ErrNotVerified
	}
	return s.addSession(email)
}

// AccountBySession returns the account behind a session token.
func (s *Store) AccountBySession(token string) (*Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	email, err := s.sessionEmail(token)
	if err != nil {
		return nil, err
	}
	return s.load(email)
}

// PutState replaces the state blob of the account behind the session token.
func (s *Store) PutState(token string, state json.RawMessage) error {
	if err := ValidateState(state); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	email, err := s.sessionEmail(token)
	if err != nil {
		return err
	}
	acc, err := s.load(email)
	if err != nil {
		return err
	}
	acc.State = append(json.RawMessage(nil), state...)
	return s.write(acc)
}

// Logout drops the session token; it is fine if the token is already gone.
func (s *Store) Logout(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sf, err := s.loadSessions()
	if err != nil {
		return err
	}
	if _, ok := sf.Sessions[token]; !ok {
		return nil
	}
	delete(sf.Sessions, token)
	return s.writeSessions(sf)
}
