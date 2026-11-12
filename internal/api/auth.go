package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/jakob/simple-vocabulary/internal/accounts"
	"github.com/jakob/simple-vocabulary/internal/analytics"
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
	return json.RawMessage(`{"version":1,"theme":null,"direction":"es-de","conjugation":{"mode":"ind","tense":"prs","order":"default"},"generate":{"count":25,"difficulty":"easy"},"sets":[]}`)
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
	s.track(analytics.SignupStarted)
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
	s.track(analytics.AccountCreated)
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
	s.track(analytics.Login)
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
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(state)
}

// handleAccountStatePut accepts PUT (fetch) and POST (sendBeacon flush on
// pagehide — sendBeacon always POSTs).
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
	state, err := readStateBody(w, r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid state")
		return
	}
	if err := s.Accounts.PutState(token, state); err != nil {
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
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
