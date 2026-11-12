// Package accounts persists user accounts: credentials, email verification,
// sessions, and each account's complete application state, in a SQLite
// database (vocab.db) under Dir.
package accounts

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
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

	// DBFile is the SQLite database file name inside the store directory.
	DBFile = "vocab.db"
	// timeLayout matches SQLite's datetime() so stored times compare directly
	// against expressions like datetime('now','-30 day').
	timeLayout = "2006-01-02 15:04:05"
	// lastSeenResolution limits how often reads bump last_seen_at.
	lastSeenResolution = time.Minute
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
	ErrStateConflict    = errors.New("state changed on another device")
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
	StateRev       uint64          `json:"stateRev,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	LastSeenAt     time.Time       `json:"-"`
}

// StateEvent is pushed to an account's subscribers whenever its state blob is
// replaced. Src identifies the writing session so that session's clients can
// ignore their own echo.
type StateEvent struct {
	Rev uint64
	Src string
}

// Store keeps accounts in a SQLite database under Dir. The mutex serializes
// read-modify-write sequences; SQLite itself only sees one writer at a time.
type Store struct {
	Dir  string
	db   *sql.DB
	mu   sync.Mutex
	subs map[string]map[chan StateEvent]struct{}
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	email           TEXT PRIMARY KEY,
	password_hash   TEXT NOT NULL,
	verified        INTEGER NOT NULL DEFAULT 0,
	verify_code     TEXT NOT NULL DEFAULT '',
	verify_expires  TEXT,
	verify_attempts INTEGER NOT NULL DEFAULT 0,
	pending_state   TEXT,
	state           TEXT,
	state_rev       INTEGER NOT NULL DEFAULT 0,
	created_at      TEXT NOT NULL,
	updated_at      TEXT NOT NULL,
	last_seen_at    TEXT
);
CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	email      TEXT NOT NULL REFERENCES users(email) ON DELETE CASCADE,
	created_at TEXT NOT NULL,
	expires    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS sessions_email ON sessions(email);
`

// NewStore opens (or creates) Dir/vocab.db.
func NewStore(dir string) (*Store, error) {
	if dir == "" {
		return nil, fmt.Errorf("accounts store directory is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := OpenDB(filepath.Join(dir, DBFile))
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("create accounts schema: %w", err)
	}
	return &Store{Dir: dir, db: db}, nil
}

// OpenDB opens a SQLite database with WAL and a busy timeout, limited to one
// connection so writers never contend.
func OpenDB(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// DB exposes the underlying database so other stores (e.g. analytics) can
// share the same file.
func (s *Store) DB() *sql.DB { return s.db }

func (s *Store) Close() error { return s.db.Close() }

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

// SessionID returns a short opaque id for a session token. State events carry
// it so the writing device can ignore its own echo; it cannot be reversed to
// the token.
func SessionID(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

// Subscribe registers a listener for state changes of email's account. The
// returned channel gets the latest event; it holds one pending notification,
// so bursts collapse into a single pull. Call the unsubscribe func to detach.
func (s *Store) Subscribe(email string) (<-chan StateEvent, func()) {
	ch := make(chan StateEvent, 1)
	s.mu.Lock()
	if s.subs == nil {
		s.subs = map[string]map[chan StateEvent]struct{}{}
	}
	if s.subs[email] == nil {
		s.subs[email] = map[chan StateEvent]struct{}{}
	}
	s.subs[email][ch] = struct{}{}
	s.mu.Unlock()
	return ch, func() {
		s.mu.Lock()
		delete(s.subs[email], ch)
		if len(s.subs[email]) == 0 {
			delete(s.subs, email)
		}
		s.mu.Unlock()
	}
}

// notifyState pushes ev to the account's subscribers, dropping the oldest
// pending notification when one is already queued. Callers must hold s.mu.
func (s *Store) notifyState(email string, ev StateEvent) {
	for ch := range s.subs[email] {
		select {
		case ch <- ev:
		default:
		}
	}
}

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(v sql.NullString) time.Time {
	if !v.Valid || v.String == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation(timeLayout, v.String, time.UTC)
	if err != nil {
		return time.Time{}
	}
	return t
}

func nullTime(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return formatTime(t)
}

func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

// tokenHash is what the sessions table stores, so a leaked database does not
// leak usable session cookies.
func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

type queryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

const accountColumns = `email, password_hash, verified, verify_code, verify_expires,
	verify_attempts, pending_state, state, state_rev, created_at, updated_at, last_seen_at`

// load returns the account for email, or ErrNotFound.
// Callers must hold s.mu.
func (s *Store) load(email string) (*Account, error) {
	return loadAccount(s.db, email)
}

func loadAccount(q queryer, email string) (*Account, error) {
	var (
		acc                                         Account
		verified                                    int
		verifyExpires, createdAt, updatedAt, seenAt sql.NullString
		pending, state                              sql.NullString
	)
	err := q.QueryRow(`SELECT `+accountColumns+` FROM users WHERE email = ?`, email).Scan(
		&acc.Email, &acc.PasswordHash, &verified, &acc.VerifyCode, &verifyExpires,
		&acc.VerifyAttempts, &pending, &state, &acc.StateRev, &createdAt, &updatedAt, &seenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	acc.Verified = verified != 0
	acc.VerifyExpires = parseTime(verifyExpires)
	acc.CreatedAt = parseTime(createdAt)
	acc.UpdatedAt = parseTime(updatedAt)
	acc.LastSeenAt = parseTime(seenAt)
	if pending.Valid {
		acc.PendingState = json.RawMessage(pending.String)
	}
	if state.Valid {
		acc.State = json.RawMessage(state.String)
	}
	return &acc, nil
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// write upserts the account. Callers must hold s.mu.
func (s *Store) write(acc *Account) error {
	acc.UpdatedAt = time.Now().UTC()
	return upsertAccount(s.db, acc)
}

func upsertAccount(e execer, acc *Account) error {
	verified := 0
	if acc.Verified {
		verified = 1
	}
	if acc.CreatedAt.IsZero() {
		acc.CreatedAt = time.Now().UTC()
	}
	if acc.UpdatedAt.IsZero() {
		acc.UpdatedAt = acc.CreatedAt
	}
	_, err := e.Exec(`INSERT INTO users (`+accountColumns+`)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(email) DO UPDATE SET
			password_hash = excluded.password_hash,
			verified = excluded.verified,
			verify_code = excluded.verify_code,
			verify_expires = excluded.verify_expires,
			verify_attempts = excluded.verify_attempts,
			pending_state = excluded.pending_state,
			state = excluded.state,
			state_rev = excluded.state_rev,
			updated_at = excluded.updated_at,
			last_seen_at = excluded.last_seen_at`,
		acc.Email, acc.PasswordHash, verified, acc.VerifyCode, nullTime(acc.VerifyExpires),
		acc.VerifyAttempts, nullJSON(acc.PendingState), nullJSON(acc.State), acc.StateRev,
		formatTime(acc.CreatedAt), formatTime(acc.UpdatedAt), nullTime(acc.LastSeenAt))
	return err
}

// touch records activity for email, at most once per lastSeenResolution.
// Callers must hold s.mu.
func (s *Store) touch(email string) {
	now := time.Now().UTC()
	_, _ = s.db.Exec(`UPDATE users SET last_seen_at = ?
		WHERE email = ? AND (last_seen_at IS NULL OR last_seen_at < ?)`,
		formatTime(now), email, formatTime(now.Add(-lastSeenResolution)))
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
	acc.StateRev++
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

// addSession stores a new session token and counts as activity.
// Callers must hold s.mu.
func (s *Store) addSession(email string) (string, error) {
	now := time.Now().UTC()
	if _, err := s.db.Exec(`DELETE FROM sessions WHERE expires < ?`, formatTime(now)); err != nil {
		return "", err
	}
	token := newToken()
	if _, err := s.db.Exec(`INSERT INTO sessions (token_hash, email, created_at, expires) VALUES (?, ?, ?, ?)`,
		tokenHash(token), email, formatTime(now), formatTime(now.Add(SessionTTL))); err != nil {
		return "", err
	}
	s.touch(email)
	return token, nil
}

// sessionEmail resolves a token to its account email, pruning an expired
// entry. Callers must hold s.mu.
func (s *Store) sessionEmail(token string) (string, error) {
	if token == "" {
		return "", ErrSession
	}
	key := tokenHash(token)
	var (
		email   string
		expires sql.NullString
	)
	err := s.db.QueryRow(`SELECT email, expires FROM sessions WHERE token_hash = ?`, key).Scan(&email, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrSession
	}
	if err != nil {
		return "", err
	}
	if time.Now().UTC().After(parseTime(expires)) {
		_, _ = s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, key)
		return "", ErrSession
	}
	return email, nil
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
	s.touch(email)
	return s.load(email)
}

// PutState replaces the state blob of the account behind the session token
// and returns the new revision. baseRev >= 0 makes the write conditional:
// ErrStateConflict is returned (with the current revision) when another
// session saved in between. Subscribers are notified of the new revision.
func (s *Store) PutState(token string, state json.RawMessage, baseRev int64) (uint64, error) {
	if err := ValidateState(state); err != nil {
		return 0, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	email, err := s.sessionEmail(token)
	if err != nil {
		return 0, err
	}
	acc, err := s.load(email)
	if err != nil {
		return 0, err
	}
	if baseRev >= 0 && uint64(baseRev) != acc.StateRev {
		return acc.StateRev, ErrStateConflict
	}
	acc.State = append(json.RawMessage(nil), state...)
	acc.LastSeenAt = time.Now().UTC()
	acc.StateRev++
	if err := s.write(acc); err != nil {
		return 0, err
	}
	s.notifyState(email, StateEvent{Rev: acc.StateRev, Src: SessionID(token)})
	return acc.StateRev, nil
}

// Logout drops the session token; it is fine if the token is already gone.
func (s *Store) Logout(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, tokenHash(token))
	return err
}

// Stats summarizes accounts for the admin page. Active means the account was
// seen (session use or state save) at or after since.
type Stats struct {
	Users       int              `json:"users"`
	Unverified  int              `json:"unverified"`
	Active      int              `json:"active"`
	Inactive    int              `json:"inactive"`
	New         int              `json:"new"`
	Collections *CollectionStats `json:"collections"`
}

// CollectionStats describes how many sets verified users have.
type CollectionStats struct {
	Avg    float64 `json:"avg"`
	Median float64 `json:"median"`
	Min    int     `json:"min"`
	Max    int     `json:"max"`
}

func (s *Store) Stats(since time.Time) (Stats, error) {
	var st Stats
	cut := formatTime(since)
	err := s.db.QueryRow(`SELECT
			COALESCE(SUM(verified = 1), 0),
			COALESCE(SUM(verified = 0), 0),
			COALESCE(SUM(verified = 1 AND last_seen_at >= ?), 0),
			COALESCE(SUM(verified = 1 AND created_at >= ?), 0)
		FROM users`, cut, cut).Scan(&st.Users, &st.Unverified, &st.Active, &st.New)
	if err != nil {
		return st, err
	}
	st.Inactive = st.Users - st.Active

	rows, err := s.db.Query(`SELECT COALESCE(json_array_length(state, '$.sets'), 0)
		FROM users WHERE verified = 1 AND json_valid(state)`)
	if err != nil {
		return st, err
	}
	defer rows.Close()
	var counts []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return st, err
		}
		counts = append(counts, n)
	}
	if err := rows.Err(); err != nil {
		return st, err
	}
	st.Collections = collectionStats(counts)
	return st, nil
}

func collectionStats(counts []int) *CollectionStats {
	if len(counts) == 0 {
		return nil
	}
	sort.Ints(counts)
	sum := 0
	for _, n := range counts {
		sum += n
	}
	mid := len(counts) / 2
	median := float64(counts[mid])
	if len(counts)%2 == 0 {
		median = float64(counts[mid-1]+counts[mid]) / 2
	}
	return &CollectionStats{
		Avg:    math.Round(float64(sum)/float64(len(counts))*10) / 10,
		Median: median,
		Min:    counts[0],
		Max:    counts[len(counts)-1],
	}
}
