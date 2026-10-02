package application

import (
	"sync"
	"time"
)

const (
	minutesPerDay = 24 * 60
	hoursPerDay   = 24
)

// HourCount is accepted messages in one UTC clock hour.
type HourCount struct {
	HourStart time.Time `json:"hourStart"`
	Count     int       `json:"count"`
}

type MessageRateReport struct {
	CountsSince string      `json:"countsSince"`
	StartedAt   time.Time   `json:"startedAt"`
	LastHour    int         `json:"lastHour"`
	Hours       []HourCount `json:"hours"`
}

type minuteSlot struct {
	minute int64
	count  int
}

// MessageRate counts accepted location messages per minute for the last 24
// hours. It lives in memory only, so it restarts from zero with the service.
type MessageRate struct {
	mu    sync.Mutex
	since time.Time
	slots [minutesPerDay]minuteSlot
}

func NewMessageRate(now time.Time) *MessageRate {
	return &MessageRate{since: now.UTC()}
}

func (r *MessageRate) Record(now time.Time) {
	if r == nil {
		return
	}
	minute := now.Unix() / 60
	r.mu.Lock()
	defer r.mu.Unlock()
	slot := &r.slots[minute%minutesPerDay]
	if slot.minute != minute {
		*slot = minuteSlot{minute: minute}
	}
	slot.count++
}

func (r *MessageRate) Report(now time.Time) MessageRateReport {
	report := MessageRateReport{CountsSince: CountsSinceRestart, Hours: []HourCount{}}
	if r == nil {
		return report
	}
	report.StartedAt = r.since
	nowMinute := now.Unix() / 60
	currentHour := now.UTC().Truncate(time.Hour)
	hours := make([]HourCount, hoursPerDay)
	for i := range hours {
		hours[i].HourStart = currentHour.Add(time.Duration(i-hoursPerDay+1) * time.Hour)
	}
	firstHourMinute := hours[0].HourStart.Unix() / 60

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, slot := range r.slots {
		if slot.count == 0 || slot.minute < firstHourMinute || slot.minute > nowMinute {
			continue
		}
		hours[(slot.minute-firstHourMinute)/60].Count += slot.count
		if slot.minute > nowMinute-60 {
			report.LastHour += slot.count
		}
	}
	report.Hours = hours
	return report
}
