package application

import (
	"testing"
	"time"
)

func TestMessageRate_LastHourAndHours(t *testing.T) {
	now := time.Date(2026, 2, 2, 11, 30, 0, 0, time.UTC)
	rate := NewMessageRate(now.Add(-48 * time.Hour))
	rate.Record(now)
	rate.Record(now.Add(-10 * time.Minute))
	rate.Record(now.Add(-90 * time.Minute))
	rate.Record(now.Add(-30 * time.Hour))

	report := rate.Report(now)
	if report.LastHour != 2 {
		t.Fatalf("last hour = %d, want 2", report.LastHour)
	}
	if len(report.Hours) != 24 || report.Hours[23].Count != 2 || report.Hours[22].Count != 1 {
		t.Fatalf("hours wrong: %+v", report.Hours)
	}
	if report.CountsSince != CountsSinceRestart {
		t.Fatalf("countsSince = %q", report.CountsSince)
	}
}

func TestMessageRate_NilIsSafe(t *testing.T) {
	var rate *MessageRate
	rate.Record(time.Now())
	if got := rate.Report(time.Now()); got.LastHour != 0 || len(got.Hours) != 0 {
		t.Fatalf("unexpected report %+v", got)
	}
}
