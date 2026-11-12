// Package analytics keeps anonymous daily usage counters: page views, unique
// visitors, and named actions. Nothing per person is stored; unique visitors
// are counted with an in-memory hash of IP and user agent salted with a
// random value that is replaced every day and never written to disk.
package analytics

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"sort"
	"sync"
	"time"
)

// Counter names. Server-side actions are recorded by the API handlers;
// ClientEvents lists the names the frontend may report via /api/events.
const (
	PageViews = "pageviews"
	Visitors  = "visitors"

	SetGenerated    = "set_generated"
	SetShared       = "set_shared"
	SharedSetOpened = "shared_set_opened"
	SignupStarted   = "signup_started"
	AccountCreated  = "account_created"
	Login           = "login"

	dayLayout = "2006-01-02"
)

// ClientEvents are the counters the frontend may increment.
var ClientEvents = map[string]bool{
	"card_studied":        true,
	"verb_studied":        true,
	"shared_set_imported": true,
	"export":              true,
	"import":              true,
}

const schema = `
CREATE TABLE IF NOT EXISTS daily_counts (
	day   TEXT NOT NULL,
	name  TEXT NOT NULL,
	count INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (day, name)
);
`

// Store records counters in the daily_counts table. Days are UTC.
type Store struct {
	db  *sql.DB
	now func() time.Time

	mu   sync.Mutex
	day  string
	salt [16]byte
	seen map[[32]byte]struct{}
}

func NewStore(db *sql.DB) (*Store, error) {
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) today() string { return s.now().UTC().Format(dayLayout) }

// Add increments the named counter for today by n.
func (s *Store) Add(name string, n int) error {
	if n <= 0 {
		return nil
	}
	_, err := s.db.Exec(`INSERT INTO daily_counts (day, name, count) VALUES (?, ?, ?)
		ON CONFLICT(day, name) DO UPDATE SET count = count + excluded.count`,
		s.today(), name, n)
	return err
}

// PageView counts a page load and, the first time today, its visitor.
// A restart forgets today's visitors, so they may be counted twice that day.
func (s *Store) PageView(ip, userAgent string) error {
	if err := s.Add(PageViews, 1); err != nil {
		return err
	}
	if s.firstVisitToday(ip, userAgent) {
		return s.Add(Visitors, 1)
	}
	return nil
}

func (s *Store) firstVisitToday(ip, userAgent string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if day := s.today(); day != s.day || s.seen == nil {
		s.day = day
		_, _ = rand.Read(s.salt[:])
		s.seen = map[[32]byte]struct{}{}
	}
	h := sha256.New()
	h.Write(s.salt[:])
	h.Write([]byte(ip))
	h.Write([]byte{0})
	h.Write([]byte(userAgent))
	var key [32]byte
	copy(key[:], h.Sum(nil))
	if _, ok := s.seen[key]; ok {
		return false
	}
	s.seen[key] = struct{}{}
	return true
}

// Day is one row of the report: all counters recorded that day.
type Day struct {
	Day    string         `json:"day"`
	Counts map[string]int `json:"counts"`
}

// Report covers the last days days including today, newest first.
type Report struct {
	Daily  []Day          `json:"daily"`
	Totals map[string]int `json:"totals"`
	Names  []string       `json:"names"`
}

func (s *Store) Report(days int) (Report, error) {
	if days < 1 {
		days = 1
	}
	today := s.now().UTC()
	from := today.AddDate(0, 0, -(days - 1)).Format(dayLayout)
	rows, err := s.db.Query(`SELECT day, name, count FROM daily_counts WHERE day >= ?`, from)
	if err != nil {
		return Report{}, err
	}
	defer rows.Close()

	byDay := map[string]map[string]int{}
	totals := map[string]int{}
	for rows.Next() {
		var (
			day, name string
			n         int
		)
		if err := rows.Scan(&day, &name, &n); err != nil {
			return Report{}, err
		}
		if byDay[day] == nil {
			byDay[day] = map[string]int{}
		}
		byDay[day][name] = n
		totals[name] += n
	}
	if err := rows.Err(); err != nil {
		return Report{}, err
	}

	rep := Report{Totals: totals}
	for i := 0; i < days; i++ {
		day := today.AddDate(0, 0, -i).Format(dayLayout)
		counts := byDay[day]
		if counts == nil {
			counts = map[string]int{}
		}
		rep.Daily = append(rep.Daily, Day{Day: day, Counts: counts})
	}
	rep.Names = []string{PageViews, Visitors}
	var rest []string
	for name := range totals {
		if name != PageViews && name != Visitors {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	rep.Names = append(rep.Names, rest...)
	return rep, nil
}
