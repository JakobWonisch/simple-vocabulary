package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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
	var putRes struct {
		Rev uint64 `json:"rev"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &putRes); err != nil || putRes.Rev == 0 {
		t.Fatalf("put response missing rev: %s", rec.Body.String())
	}
	rec = authRequest(t, srv, http.MethodGet, "/api/account/state", "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "synced") {
		t.Fatalf("get state: %d %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("X-State-Rev") != strconv.FormatUint(putRes.Rev, 10) {
		t.Fatalf("X-State-Rev = %q, want %d", rec.Header().Get("X-State-Rev"), putRes.Rev)
	}

	// A write based on the current revision lands; a stale base is refused.
	rec = authRequest(t, srv, http.MethodPut,
		"/api/account/state?baseRev="+strconv.FormatUint(putRes.Rev, 10),
		`{"version":1,"sets":[{"id":"newer"}]}`, cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("put state with baseRev: %d %s", rec.Code, rec.Body.String())
	}
	rec = authRequest(t, srv, http.MethodPut,
		"/api/account/state?baseRev="+strconv.FormatUint(putRes.Rev, 10),
		`{"version":1,"sets":[{"id":"stale"}]}`, cookie)
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale put: %d %s", rec.Code, rec.Body.String())
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
	if !strings.Contains(rec.Body.String(), "newer") {
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

func TestAccountEvents(t *testing.T) {
	srv, mailer := newAuthServer(t)
	authRequest(t, srv, http.MethodPost, "/api/auth/signup", `{"email":"s@example.com","password":"password-1"}`, "")
	code := mailer.codes["s@example.com"]
	rec := authRequest(t, srv, http.MethodPost, "/api/auth/verify", `{"email":"s@example.com","code":"`+code+`"}`, "")
	cookie := sessionCookie(t, rec)

	// Unauthenticated streaming is refused.
	if rec := authRequest(t, srv, http.MethodGet, "/api/account/events", "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("events without session: %d", rec.Code)
	}

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	req, err := http.NewRequest(http.MethodGet, httpSrv.URL+"/api/account/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", cookie)
	resp, err := httpSrv.Client().Do(req)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events: %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("events content-type: %q", ct)
	}

	reader := bufio.NewReader(resp.Body)
	init := readEvent(t, reader)
	if !strings.Contains(init, "event: init") || !strings.Contains(init, `"rev":1`) || !strings.Contains(init, `"src"`) {
		t.Fatalf("init event: %q", init)
	}

	// A state write is pushed to stream subscribers with the new revision.
	authRequest(t, srv, http.MethodPut, "/api/account/state", `{"version":1,"sets":[]}`, cookie)
	ev := readEvent(t, reader)
	if !strings.Contains(ev, `"rev":2`) || !strings.Contains(ev, `"src"`) {
		t.Fatalf("state event: %q", ev)
	}
}

func TestAccountEventsCrossSession(t *testing.T) {
	srv, mailer := newAuthServer(t)
	authRequest(t, srv, http.MethodPost, "/api/auth/signup", `{"email":"x@example.com","password":"password-1"}`, "")
	code := mailer.codes["x@example.com"]
	authRequest(t, srv, http.MethodPost, "/api/auth/verify", `{"email":"x@example.com","code":"`+code+`"}`, "")

	// Two logins give the account two sessions — the streaming one must see
	// writes from the other with a src that differs from its own init src.
	recA := authRequest(t, srv, http.MethodPost, "/api/auth/login", `{"email":"x@example.com","password":"password-1"}`, "")
	cookieA := sessionCookie(t, recA)
	recB := authRequest(t, srv, http.MethodPost, "/api/auth/login", `{"email":"x@example.com","password":"password-1"}`, "")
	cookieB := sessionCookie(t, recB)

	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	req, err := http.NewRequest(http.MethodGet, httpSrv.URL+"/api/account/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", cookieB)
	resp, err := httpSrv.Client().Do(req)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)
	init := readEvent(t, reader)
	srcB := eventSrc(t, init)
	if srcB == "" {
		t.Fatalf("init event missing src: %q", init)
	}

	authRequest(t, srv, http.MethodPut, "/api/account/state", `{"version":1,"sets":[]}`, cookieA)
	ev := readEvent(t, reader)
	srcEv := eventSrc(t, ev)
	if srcEv == "" || srcEv == srcB {
		t.Fatalf("expected event from the other session, got %q (own %q): %q", srcEv, srcB, ev)
	}
}

// eventSrc extracts the src field of a data: {...} line in an SSE block.
func eventSrc(t *testing.T, block string) string {
	t.Helper()
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var data struct {
			Src string `json:"src"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &data); err != nil {
			t.Fatalf("bad event data %q: %v", line, err)
		}
		return data.Src
	}
	return ""
}

// readEvent reads one SSE block (lines up to the blank separator).
func readEvent(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	type result struct {
		s   string
		err error
	}
	done := make(chan result, 1)
	go func() {
		var b strings.Builder
		for {
			line, err := r.ReadString('\n')
			b.WriteString(line)
			if err != nil {
				done <- result{b.String(), err}
				return
			}
			if line == "\n" {
				done <- result{b.String(), nil}
				return
			}
		}
	}()
	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("read event: %v (%q)", res.err, res.s)
		}
		return res.s
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for event")
		return ""
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
