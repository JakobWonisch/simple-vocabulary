package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jakob/simple-vocabulary/internal/accounts"
)

const sessionCookieName = "sv_session"

type signupRequest struct {
	Email     string          `json:"email"`
	Password  string          `json:"password"`
	CopyLocal bool            `json:"copyLocal"`
	State     json.RawMessage `json:"state"`
}

type verifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
}

type resendRequest struct {
	Email string `json:"email"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) accountsReady(w http.ResponseWriter) bool {
	if s.Accounts == nil || s.Mail == nil {
		writeError(w, http.StatusServiceUnavailable, "accounts are not configured")
		return false
	}
	return true
}

func secureCookie(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   secureCookie(r),
		MaxAge:   int(accounts.SessionTTL.Seconds()),
	})
}

func clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// requireSession resolves the session cookie to an account or writes 401.
func (s *Server) requireSession(w http.ResponseWriter, r *http.Request) *accounts.Account {
	token := sessionToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return nil
	}
	acc, err := s.Accounts.AccountBySession(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return nil
	}
	return acc
}

// initialState reads the seed state used for new accounts that did not import
// their local data — the same file anonymous browsers get.
func (s *Server) initialState() json.RawMessage {
	if s.InitialPath != "" {
		if body, err := os.ReadFile(s.InitialPath); err == nil {
			if json.Valid(body) {
				return body
			}
		}
	}
	return json.RawMessage(`{"version":1,"theme":null,"direction":"es-de","conjugation":{"mode":"ind","tense":"prs","order":"default"},"generate":{"count":25,"difficulty":"easy"},"setSort":"created","sets":[]}`)
}

func (s *Server) sendCode(r *http.Request, email, code string) bool {
	if err := s.Mail.SendVerificationCode(r.Context(), email, code); err != nil {
		log.Printf("send verification code to %s: %v", email, err)
		return false
	}
	return true
}

func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	if !s.accountsReady(w) {
		return
	}
	var body signupRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, accounts.MaxStateBytes+1<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email, err := accounts.NormalizeEmail(body.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	if err := accounts.ValidatePassword(body.Password); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var pending json.RawMessage
	if body.CopyLocal && len(body.State) > 0 {
		if err := accounts.ValidateState(body.State); err != nil {
			writeError(w, http.StatusBadRequest, "invalid state")
			return
		}
		pending = body.State
	}
	code, err := s.Accounts.Signup(email, body.Password, pending)
	if err != nil {
		if errors.Is(err, accounts.ErrEmailTaken) {
			writeError(w, http.StatusConflict, "email already registered")
			return
		}
		log.Printf("signup %s: %v", email, err)
		writeError(w, http.StatusInternalServerError, "could not create account")
		return
	}
	if !s.sendCode(r, email, code) {
		writeError(w, http.StatusBadGateway, "could not send verification email")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "email": email})
}

func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request) {
	if !s.accountsReady(w) {
		return
	}
	var body verifyRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email, err := accounts.NormalizeEmail(body.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	acc, err := s.Accounts.Verify(email, body.Code, s.initialState())
	if err != nil {
		switch {
		case errors.Is(err, accounts.ErrBadCode):
			writeError(w, http.StatusBadRequest, "invalid verification code")
		case errors.Is(err, accounts.ErrCodeExpired):
			writeError(w, http.StatusBadRequest, "verification code expired")
		case errors.Is(err, accounts.ErrAttemptsExceeded):
			writeError(w, http.StatusTooManyRequests, "too many code attempts")
		case errors.Is(err, accounts.ErrEmailTaken):
			writeError(w, http.StatusConflict, "email already verified — please log in")
		case errors.Is(err, accounts.ErrNotFound):
			writeError(w, http.StatusNotFound, "account not found — please sign up again")
		default:
			log.Printf("verify %s: %v", email, err)
			writeError(w, http.StatusInternalServerError, "verification failed")
		}
		return
	}
	s.startSession(w, r, acc)
}

func (s *Server) handleResend(w http.ResponseWriter, r *http.Request) {
	if !s.accountsReady(w) {
		return
	}
	var body resendRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email, err := accounts.NormalizeEmail(body.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	code, err := s.Accounts.Resend(email)
	if err != nil {
		if errors.Is(err, accounts.ErrEmailTaken) {
			writeError(w, http.StatusConflict, "email already verified — please log in")
			return
		}
		if errors.Is(err, accounts.ErrNotFound) {
			writeError(w, http.StatusNotFound, "account not found — please sign up again")
			return
		}
		log.Printf("resend %s: %v", email, err)
		writeError(w, http.StatusInternalServerError, "could not resend code")
		return
	}
	if !s.sendCode(r, email, code) {
		writeError(w, http.StatusBadGateway, "could not send verification email")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.accountsReady(w) {
		return
	}
	var body loginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email, err := accounts.NormalizeEmail(body.Email)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid email")
		return
	}
	acc, token, err := s.Accounts.Login(email, body.Password)
	if err != nil {
		if errors.Is(err, accounts.ErrNotVerified) {
			// Help the user finish signup: issue a fresh code right away.
			if code, rerr := s.Accounts.Resend(email); rerr == nil {
				_ = s.sendCode(r, email, code)
			}
			writeJSON(w, http.StatusForbidden, map[string]any{
				"error":       "email not verified",
				"needsVerify": true,
				"email":       email,
			})
			return
		}
		if errors.Is(err, accounts.ErrBadCredentials) {
			writeError(w, http.StatusUnauthorized, "wrong email or password")
			return
		}
		log.Printf("login %s: %v", email, err)
		writeError(w, http.StatusInternalServerError, "login failed")
		return
	}
	s.startSession(w, r, acc, token)
}

// startSession is used after verify (new session) and login (session made by
// the store). Two signatures to keep both call sites simple.
func (s *Server) startSession(w http.ResponseWriter, r *http.Request, acc *accounts.Account, tokens ...string) {
	token := ""
	if len(tokens) > 0 {
		token = tokens[0]
	}
	if token == "" {
		// Verify path: create the session here.
		var err error
		token, err = s.Accounts.NewSession(acc.Email)
		if err != nil {
			log.Printf("session %s: %v", acc.Email, err)
			writeError(w, http.StatusInternalServerError, "could not start session")
			return
		}
	}
	s.setSessionCookie(w, r, token)
	state := acc.State
	if len(state) == 0 {
		state = s.initialState()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"email": acc.Email,
		"state": state,
		"rev":   acc.StateRev,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !s.accountsReady(w) {
		return
	}
	if token := sessionToken(r); token != "" {
		if err := s.Accounts.Logout(token); err != nil {
			log.Printf("logout: %v", err)
		}
	}
	clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil {
		writeError(w, http.StatusServiceUnavailable, "accounts are not configured")
		return
	}
	acc := s.requireSession(w, r)
	if acc == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"email": acc.Email})
}

func (s *Server) handleAccountStateGet(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil {
		writeError(w, http.StatusServiceUnavailable, "accounts are not configured")
		return
	}
	acc := s.requireSession(w, r)
	if acc == nil {
		return
	}
	state := acc.State
	if len(state) == 0 {
		state = s.initialState()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-State-Rev", strconv.FormatUint(acc.StateRev, 10))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(state)
}

// handleAccountStatePut accepts PUT (fetch) and POST (sendBeacon flush on
// pagehide — sendBeacon always POSTs). The optional baseRev query parameter
// makes the write compare-and-swap: a 409 with the current revision is
// returned when another device saved in between.
func (s *Server) handleAccountStatePut(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil {
		writeError(w, http.StatusServiceUnavailable, "accounts are not configured")
		return
	}
	token := sessionToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	baseRev := int64(-1)
	if raw := r.URL.Query().Get("baseRev"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v < 0 {
			writeError(w, http.StatusBadRequest, "invalid baseRev")
			return
		}
		baseRev = v
	}
	state, err := readStateBody(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid state")
		return
	}
	rev, err := s.Accounts.PutState(token, state, baseRev)
	if err != nil {
		if errors.Is(err, accounts.ErrStateConflict) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"error": "state changed on another device",
				"rev":   rev,
			})
			return
		}
		if errors.Is(err, accounts.ErrSession) {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		if errors.Is(err, accounts.ErrInvalidState) || errors.Is(err, accounts.ErrStateTooLarge) {
			writeError(w, http.StatusBadRequest, "invalid state")
			return
		}
		log.Printf("save state: %v", err)
		writeError(w, http.StatusInternalServerError, "could not save state")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "rev": rev})
}

// handleAccountEvents streams state-change notifications as Server-Sent
// Events so a user's other devices can pull fresh state. The init event
// carries the current revision and this session's id; later events carry
// {rev, src} where src identifies the writing session.
func (s *Server) handleAccountEvents(w http.ResponseWriter, r *http.Request) {
	if s.Accounts == nil {
		writeError(w, http.StatusServiceUnavailable, "accounts are not configured")
		return
	}
	token := sessionToken(r)
	if token == "" {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	acc, err := s.Accounts.AccountBySession(token)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "not logged in")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "streaming not supported")
		return
	}
	events, unsubscribe := s.Accounts.Subscribe(acc.Email)
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprintf(w, "retry: 3000\nevent: init\ndata: {\"rev\":%d,\"src\":%q}\n\n", acc.StateRev, accounts.SessionID(token))
	flusher.Flush()

	keepAlive := time.NewTicker(25 * time.Second)
	defer keepAlive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-events:
			_, _ = fmt.Fprintf(w, "data: {\"rev\":%d,\"src\":%q}\n\n", ev.Rev, ev.Src)
			flusher.Flush()
		case <-keepAlive.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

// readStateBody reads a JSON object as raw bytes for storage.
func readStateBody(w http.ResponseWriter, r *http.Request) (json.RawMessage, error) {
	body := http.MaxBytesReader(w, r.Body, accounts.MaxStateBytes)
	var raw json.RawMessage
	dec := json.NewDecoder(body)
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(raw)) == "" {
		return nil, errors.New("empty")
	}
	return raw, nil
}
