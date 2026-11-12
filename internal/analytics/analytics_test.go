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

	// Older data falls outside a one-day report.
	rep, _ = s.Report(1)
	if rep.Totals["card_studied"] != 0 {
		t.Fatalf("1-day totals = %v", rep.Totals)
	}
}
