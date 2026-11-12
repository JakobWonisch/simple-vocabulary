package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jakob/simple-vocabulary/internal/analytics"
)

func newAdminServer(t *testing.T) *Server {
	t.Helper()
	srv, _ := newAuthServer(t)
	usage, err := analytics.NewStore(srv.Accounts.DB())
	if err != nil {
		t.Fatal(err)
	}
	srv.Analytics = usage
	srv.AdminPassword = "letmein-admin"
	return srv
}

func TestAdminRequiresPassword(t *testing.T) {
	srv := newAdminServer(t)
	for _, path := range []string{"/admin", "/api/admin/stats"} {
		rec := authRequest(t, srv, http.MethodGet, path, "", "")
		if rec.Code != http.StatusUnauthorized || rec.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("%s without auth: %d", path, rec.Code)
		}
	}
	srv.AdminPassword = ""
	if rec := authRequest(t, srv, http.MethodGet, "/api/admin/stats", "", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled admin: %d", rec.Code)
	}
}

func TestEventsAndPageViewsReachAdminStats(t *testing.T) {
	srv := newAdminServer(t)

	page := httptest.NewRequest(http.MethodGet, "/", nil)
	page.Header.Set("User-Agent", "Mozilla/5.0 Firefox")
	page.Header.Set("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), page)
	bot := httptest.NewRequest(http.MethodGet, "/", nil)
	bot.Header.Set("User-Agent", "Googlebot/2.1")
	srv.Handler().ServeHTTP(httptest.NewRecorder(), bot)

	rec := authRequest(t, srv, http.MethodPost, "/api/events",
		`{"counts":{"card_studied":3,"verb_studied":100000,"evil":5}}`, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("events: %d %s", rec.Code, rec.Body)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/admin/stats?days=7", nil)
	req.SetBasicAuth("admin", "letmein-admin")
	out := httptest.NewRecorder()
	srv.Handler().ServeHTTP(out, req)
	if out.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", out.Code, out.Body)
	}
	var stats adminStats
	if err := json.Unmarshal(out.Body.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	totals := stats.Traffic.Totals
	if totals["pageviews"] != 1 || totals["visitors"] != 1 || totals["card_studied"] != 3 ||
		totals["verb_studied"] != maxEventCount || totals["evil"] != 0 {
		t.Fatalf("totals = %v", totals)
	}
	if stats.Days != 7 || stats.Accounts == nil || len(stats.Traffic.Daily) != 7 {
		t.Fatalf("stats = %+v", stats)
	}

	req = httptest.NewRequest(http.MethodGet, "/admin", nil)
	req.SetBasicAuth("", "letmein-admin")
	out = httptest.NewRecorder()
	srv.Handler().ServeHTTP(out, req)
	if out.Code != http.StatusOK || !strings.Contains(out.Body.String(), "card studied") {
		t.Fatalf("admin page: %d %s", out.Code, out.Body)
	}
}
