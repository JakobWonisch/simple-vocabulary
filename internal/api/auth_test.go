package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jakob/simple-vocabulary/internal/accounts"
)

type captureMail struct{ codes map[string]string }

func newCaptureMail() *captureMail { return &captureMail{codes: map[string]string{}} }

func (m *captureMail) Name() string { return "test" }

func (m *captureMail) SendVerificationCode(_ context.Context, to, code string) error {
	m.codes[to] = code
	return nil
}

func newAuthServer(t *testing.T) (*Server, *captureMail) {
	t.Helper()
	store, err := accounts.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mail := newCaptureMail()
	srv := New(testDict(t), nil, t.TempDir())
	srv.Accounts = store
	srv.Mail = mail
	return srv, mail
}

func authRequest(t *testing.T, srv *Server, method, path, body, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookieName && c.Value != "" {
			return c.Name + "=" + c.Value
		}
	}
	t.Fatalf("no session cookie in %v", rec.Result().Cookies())
	return ""
}

func TestSignupVerifyStateLoginLogout(t *testing.T) {
	srv, mailer := newAuthServer(t)
	srv.InitialPath = "" // exercise the built-in default state

	// me without session is 401.
	if rec := authRequest(t, srv, http.MethodGet, "/api/auth/me", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("me: %d", rec.Code)
	}

	local := `{"email":"d@example.com","password":"password-1","copyLocal":true,"state":{"version":1,"sets":[{"id":"local-set"}]}}`
	rec := authRequest(t, srv, http.MethodPost, "/api/auth/signup", local, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("signup: %d %s", rec.Code, rec.Body.String())
	}
	code := mailer.codes["d@example.com"]
	if code == "" {
		t.Fatal("no verification code sent")
	}

	// Wrong code rejected.
	rec = authRequest(t, srv, http.MethodPost, "/api/auth/verify", `{"email":"d@example.com","code":"999999"}`, "")
	if code == "999999" {
		rec = authRequest(t, srv, http.MethodPost, "/api/auth/verify", `{"email":"d@example.com","code":"000000"}`, "")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad verify: %d", rec.Code)
	}

	// Correct code logs in and returns the copied local state.
	rec = authRequest(t, srv, http.MethodPost, "/api/auth/verify", `{"email":"d@example.com","code":"`+code+`"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", rec.Code, rec.Body.String())
	}
	cookie := sessionCookie(t, rec)
	var verifyRes struct {
		Email string          `json:"email"`
		State json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &verifyRes); err != nil {
		t.Fatal(err)
	}
	if verifyRes.Email != "d@example.com" || !strings.Contains(string(verifyRes.State), "local-set") {
		t.Fatalf("verify response: %s", rec.Body.String())
	}

	// me + state round-trip with the cookie.
	rec = authRequest(t, srv, http.MethodGet, "/api/auth/me", "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "d@example.com") {
		t.Fatalf("me: %d %s", rec.Code, rec.Body.String())
	}
	rec = authRequest(t, srv, http.MethodPut, "/api/account/state", `{"version":1,"sets":[{"id":"synced"}]}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("put state: %d %s", rec.Code, rec.Body.String())
	}
	rec = authRequest(t, srv, http.MethodGet, "/api/account/state", "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "synced") {
		t.Fatalf("get state: %d %s", rec.Code, rec.Body.String())
	}
	// Unauthenticated state access is refused.
	rec = authRequest(t, srv, http.MethodGet, "/api/account/state", "", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("get state without session: %d", rec.Code)
	}

	// Logout clears the session.
	rec = authRequest(t, srv, http.MethodPost, "/api/auth/logout", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("logout: %d", rec.Code)
	}
	rec = authRequest(t, srv, http.MethodGet, "/api/auth/me", "", cookie)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("me after logout: %d", rec.Code)
	}

	// Login returns the synced state and a fresh cookie.
	rec = authRequest(t, srv, http.MethodPost, "/api/auth/login", `{"email":"d@example.com","password":"password-1"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("login: %d %s", rec.Code, rec.Body.String())
	}
	cookie = sessionCookie(t, rec)
	if !strings.Contains(rec.Body.String(), "synced") {
		t.Fatalf("login response missing state: %s", rec.Body.String())
	}
	rec = authRequest(t, srv, http.MethodGet, "/api/auth/me", "", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("me after login: %d", rec.Code)
	}
}

func TestLoginUnverifiedResendsCode(t *testing.T) {
	srv, mailer := newAuthServer(t)
	authRequest(t, srv, http.MethodPost, "/api/auth/signup", `{"email":"e@example.com","password":"password-1"}`, "")
	mailer.codes["e@example.com"] = ""

	rec := authRequest(t, srv, http.MethodPost, "/api/auth/login", `{"email":"e@example.com","password":"password-1"}`, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("login unverified: %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["needsVerify"] != true {
		t.Fatalf("expected needsVerify: %s", rec.Body.String())
	}
	if mailer.codes["e@example.com"] == "" {
		t.Fatal("expected a fresh code to be sent")
	}
}

func TestSignupDuplicateAndBadInput(t *testing.T) {
	srv, mailer := newAuthServer(t)
	authRequest(t, srv, http.MethodPost, "/api/auth/signup", `{"email":"f@example.com","password":"password-1"}`, "")
	code := mailer.codes["f@example.com"]
	authRequest(t, srv, http.MethodPost, "/api/auth/verify", `{"email":"f@example.com","code":"`+code+`"}`, "")

	// Verified email cannot sign up again.
	rec := authRequest(t, srv, http.MethodPost, "/api/auth/signup", `{"email":"f@example.com","password":"password-1"}`, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("dup signup: %d", rec.Code)
	}
	rec = authRequest(t, srv, http.MethodPost, "/api/auth/signup", `{"email":"not-an-email","password":"password-1"}`, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad email: %d", rec.Code)
	}
	rec = authRequest(t, srv, http.MethodPost, "/api/auth/signup", `{"email":"g@example.com","password":"short"}`, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("short password: %d", rec.Code)
	}
	rec = authRequest(t, srv, http.MethodPost, "/api/auth/login", `{"email":"f@example.com","password":"nope-nope"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", rec.Code)
	}
}
