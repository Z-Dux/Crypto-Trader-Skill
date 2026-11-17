package macro

import (
	"testing"
	"time"
)

func TestGetTodayNews(t *testing.T) {
	events, err := GetTodayNews(FilterOptions{Detailed: true})
	if err != nil {
		t.Fatalf("GetTodayNews err: %v", err)
	}
	t.Logf("Today's events count: %d", len(events))
	todayStr := time.Now().Format("2006-01-02")
	for _, ev := range events {
		if ev.Date != todayStr {
			t.Errorf("Expected event date %s, got %s", todayStr, ev.Date)
		}
	}
}

func TestGetUpcomingWeeklyNews(t *testing.T) {
	events, err := GetUpcomingWeeklyNews(FilterOptions{Detailed: true})
	if err != nil {
		t.Fatalf("GetUpcomingWeeklyNews err: %v", err)
	}
	t.Logf("Upcoming weekly events count: %d", len(events))

	now := time.Now()
	todayStr := now.Format("2006-01-02")
	for _, ev := range events {
		if ev.Date < todayStr {
			t.Errorf("Older event from %s not excluded", ev.Date)
		}
		if ev.Timestamp != nil && ev.Timestamp.Before(now.Add(-15*time.Minute)) {
			t.Errorf("Past event at %v not excluded", ev.Timestamp)
		}
	}

	for i := 0; i < len(events)-1; i++ {
		rankI := impactRank(events[i].Impact)
		rankJ := impactRank(events[i+1].Impact)
		if rankI > rankJ {
			t.Errorf("Events not sorted by impact importance: index %d (%s rank %d) after index %d (%s rank %d)", i, events[i].Impact, rankI, i+1, events[i+1].Impact, rankJ)
		}
	}
}

func TestGetMonthlyNewsAndMapping(t *testing.T) {
	events, err := GetMonthlyNews(FilterOptions{Detailed: true})
	if err != nil {
		t.Fatalf("GetMonthlyNews err: %v", err)
	}
	t.Logf("Monthly events count: %d", len(events))

	todayStr := time.Now().Format("2006-01-02")
	for _, ev := range events {
		if ev.Date < todayStr {
			t.Errorf("Older event from %s not excluded", ev.Date)
		}
	}

	mappings := MapImportantMonthlyDates(events)
	t.Logf("Monthly mapped dates count: %d", len(mappings))
	for _, m := range mappings {
		if m.Date < todayStr {
			t.Errorf("Older mapped date %s not excluded", m.Date)
		}
	}

	formatted := FormatMonthlyMapForLLM(mappings)
	if len(formatted) == 0 {
		t.Error("expected non-empty formatted monthly map")
	}
}
