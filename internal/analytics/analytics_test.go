package analytics

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func newStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	return s, &now
}

func TestPageViewsAndVisitors(t *testing.T) {
	s, now := newStore(t)
	_ = s.PageView("1.2.3.4", "firefox")
	_ = s.PageView("1.2.3.4", "firefox")
	_ = s.PageView("1.2.3.4", "chrome")
	_ = s.Add("card_studied", 7)

	*now = now.AddDate(0, 0, 1)
	_ = s.PageView("1.2.3.4", "firefox") // new day, counted again

	rep, err := s.Report(3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Daily) != 3 || rep.Daily[0].Day != "2026-03-11" || rep.Daily[2].Day != "2026-03-09" {
		t.Fatalf("days = %+v", rep.Daily)
	}
	if got := rep.Daily[1].Counts; got[PageViews] != 3 || got[Visitors] != 2 || got["card_studied"] != 7 {
		t.Fatalf("day 1 = %v", got)
	}
	if got := rep.Daily[0].Counts; got[PageViews] != 1 || got[Visitors] != 1 {
		t.Fatalf("day 2 = %v", got)
	}
	if rep.Totals[Visitors] != 3 || rep.Totals[PageViews] != 4 {
		t.Fatalf("totals = %v", rep.Totals)
	}
	if len(rep.Names) != 3 || rep.Names[2] != "card_studied" {
		t.Fatalf("names = %v", rep.Names)
	}

	// A restart (new Store on the same database) keeps today's visitors.
	s2, err := NewStore(s.db)
	if err != nil {
		t.Fatal(err)
	}
	s2.now = s.now
	_ = s2.PageView("1.2.3.4", "firefox")
	_ = s2.PageView("5.6.7.8", "firefox")
	rep, _ = s2.Report(1)
	if got := rep.Daily[0].Counts; got[Visitors] != 2 || got[PageViews] != 3 {
		t.Fatalf("after restart = %v", got)
	}
	var old int
	if err := s.db.QueryRow(`SELECT (SELECT count(*) FROM visitor_hashes WHERE day <> '2026-03-11') +
		(SELECT count(*) FROM visitor_salt WHERE day <> '2026-03-11')`).Scan(&old); err != nil || old != 0 {
		t.Fatalf("previous day's salt/hashes kept: %d %v", old, err)
	}

	// Older data falls outside a one-day report.
	rep, _ = s.Report(1)
	if rep.Totals["card_studied"] != 0 {
		t.Fatalf("1-day totals = %v", rep.Totals)
	}
}
