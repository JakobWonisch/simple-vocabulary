package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"html/template"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jakob/simple-vocabulary/internal/accounts"
	"github.com/jakob/simple-vocabulary/internal/analytics"
)

const (
	defaultStatsDays = 30
	maxStatsDays     = 365
	maxEventCount    = 500
	maxEventsBody    = 4 << 10
)

// track increments a server-side counter; analytics failures never fail the
// request.
func (s *Server) track(name string) {
	if s.Analytics == nil {
		return
	}
	if err := s.Analytics.Add(name, 1); err != nil {
		log.Printf("analytics %s: %v", name, err)
	}
}

// withPageViews counts loads of the app page (not assets or API calls).
func (s *Server) withPageViews(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Analytics != nil && r.Method == http.MethodGet &&
			(r.URL.Path == "/" || r.URL.Path == "/index.html") && !isBot(r.UserAgent()) {
			if err := s.Analytics.PageView(clientIP(r), r.UserAgent()); err != nil {
				log.Printf("analytics pageview: %v", err)
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isBot(ua string) bool {
	if ua == "" {
		return true
	}
	ua = strings.ToLower(ua)
	for _, marker := range []string{"bot", "crawl", "spider", "slurp", "curl", "wget", "python-", "go-http-client", "headless", "monitor", "preview"} {
		if strings.Contains(ua, marker) {
			return true
		}
	}
	return false
}

// clientIP prefers the proxy headers set by the reverse proxy in front of
// the app; it is only used for anonymous visitor counting.
func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		first, _, _ := strings.Cut(fwd, ",")
		return strings.TrimSpace(first)
	}
	if real := r.Header.Get("X-Real-IP"); real != "" {
		return strings.TrimSpace(real)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type eventsRequest struct {
	Counts map[string]int `json:"counts"`
}

// handleEvents takes batched anonymous action counts from the frontend.
// Unknown names are ignored; counts are capped per request.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if s.Analytics == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var body eventsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxEventsBody)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	for name, n := range body.Counts {
		if !analytics.ClientEvents[name] {
			continue
		}
		if err := s.Analytics.Add(name, min(n, maxEventCount)); err != nil {
			log.Printf("analytics %s: %v", name, err)
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// requireAdmin checks HTTP Basic auth against AdminPassword (any user name).
// Without a configured password the admin routes do not exist.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.AdminPassword == "" {
		http.NotFound(w, r)
		return false
	}
	_, pass, ok := r.BasicAuth()
	got := sha256.Sum256([]byte(pass))
	want := sha256.Sum256([]byte(s.AdminPassword))
	if ok && subtle.ConstantTimeCompare(got[:], want[:]) == 1 {
		return true
	}
	if ok {
		// Slow down password guessing.
		time.Sleep(time.Second)
	}
	w.Header().Set("WWW-Authenticate", `Basic realm="simple-vocabulary admin", charset="UTF-8"`)
	writeError(w, http.StatusUnauthorized, "unauthorized")
	return false
}

type adminStats struct {
	Days     int               `json:"days"`
	Accounts *accounts.Stats   `json:"accounts,omitempty"`
	Traffic  *analytics.Report `json:"traffic,omitempty"`
}

func statsDays(r *http.Request) int {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || days < 1 {
		return defaultStatsDays
	}
	return min(days, maxStatsDays)
}

func (s *Server) collectStats(days int) (adminStats, error) {
	out := adminStats{Days: days}
	if s.Accounts != nil {
		st, err := s.Accounts.Stats(time.Now().Add(-time.Duration(days) * 24 * time.Hour))
		if err != nil {
			return out, err
		}
		out.Accounts = &st
	}
	if s.Analytics != nil {
		rep, err := s.Analytics.Report(days)
		if err != nil {
			return out, err
		}
		out.Traffic = &rep
	}
	return out, nil
}

func (s *Server) handleAdminStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	stats, err := s.collectStats(statsDays(r))
	if err != nil {
		log.Printf("admin stats: %v", err)
		writeError(w, http.StatusInternalServerError, "could not load stats")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleAdminPage(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	stats, err := s.collectStats(statsDays(r))
	if err != nil {
		log.Printf("admin stats: %v", err)
		http.Error(w, "could not load stats", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	if err := adminTemplate.Execute(w, stats); err != nil {
		log.Printf("admin page: %v", err)
	}
}

var adminTemplate = template.Must(template.New("admin").Funcs(template.FuncMap{
	"count": func(counts map[string]int, name string) int { return counts[name] },
	"label": func(name string) string { return strings.ReplaceAll(name, "_", " ") },
}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>Admin · Simple Vocabulary</title>
<style>
  :root { color-scheme: light dark; font-family: system-ui, sans-serif; }
  body { max-width: 60rem; margin: 2rem auto; padding: 0 1rem; line-height: 1.4; }
  h1 { font-size: 1.4rem; } h2 { font-size: 1.1rem; margin-top: 2rem; }
  .cards { display: grid; grid-template-columns: repeat(auto-fill, minmax(9rem, 1fr)); gap: .75rem; }
  .card { border: 1px solid #8884; border-radius: .5rem; padding: .75rem; }
  .card b { display: block; font-size: 1.6rem; }
  .card span { font-size: .85rem; opacity: .75; }
  table { border-collapse: collapse; width: 100%; font-variant-numeric: tabular-nums; }
  th, td { padding: .3rem .5rem; border-bottom: 1px solid #8883; text-align: right; white-space: nowrap; }
  th:first-child, td:first-child { text-align: left; }
  .scroll { overflow-x: auto; }
  nav a { margin-right: .75rem; }
  .note { font-size: .85rem; opacity: .75; }
</style>
</head>
<body>
<h1>Simple Vocabulary · usage</h1>
<nav>Period:
  <a href="?days=1">today</a><a href="?days=7">7 days</a><a href="?days=30">30 days</a><a href="?days=90">90 days</a><a href="?days=365">365 days</a>
  <a href="/api/admin/stats?days={{.Days}}">JSON</a>
</nav>
<p class="note">Last {{.Days}} day(s). Days are UTC.</p>

{{with .Accounts}}
<h2>Accounts</h2>
<div class="cards">
  <div class="card"><b>{{.Users}}</b><span>users (verified)</span></div>
  <div class="card"><b>{{.Active}}</b><span>active in period</span></div>
  <div class="card"><b>{{.Inactive}}</b><span>inactive in period</span></div>
  <div class="card"><b>{{.New}}</b><span>new in period</span></div>
  <div class="card"><b>{{.Unverified}}</b><span>unverified signups</span></div>
</div>
<p class="note">Active = logged-in app use or a saved change within the period.</p>
<h2>Collections per user</h2>
{{with .Collections}}
<div class="cards">
  <div class="card"><b>{{.Avg}}</b><span>average</span></div>
  <div class="card"><b>{{.Median}}</b><span>median</span></div>
  <div class="card"><b>{{.Min}}</b><span>minimum</span></div>
  <div class="card"><b>{{.Max}}</b><span>maximum</span></div>
</div>
{{else}}<p>No verified users yet.</p>{{end}}
{{end}}

{{with .Traffic}}
<h2>Totals</h2>
<div class="cards">
  {{range .Names}}<div class="card"><b>{{count $.Traffic.Totals .}}</b><span>{{label .}}</span></div>{{end}}
</div>
<p class="note">Visitors are unique per day (anonymous, IP + browser hashed with a daily salt); the total is the sum of daily visitors. Includes logged-out use.</p>
<h2>Per day</h2>
<div class="scroll">
<table>
  <thead><tr><th>Day</th>{{range .Names}}<th>{{label .}}</th>{{end}}</tr></thead>
  <tbody>
  {{range .Daily}}{{$day := .}}<tr><td>{{.Day}}</td>{{range $.Traffic.Names}}<td>{{count $day.Counts .}}</td>{{end}}</tr>
  {{end}}
  </tbody>
</table>
</div>
{{end}}
</body>
</html>
`))
