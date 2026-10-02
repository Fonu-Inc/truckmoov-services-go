package domain

import "time"

const (
	VoiceDirectionInbound  = "inbound"
	VoiceDirectionOutbound = "outbound"

	VoiceStatusQueued     = "queued"
	VoiceStatusRinging    = "ringing"
	VoiceStatusInProgress = "in_progress"
	VoiceStatusCompleted  = "completed"
	VoiceStatusMissed     = "missed"
	VoiceStatusFailed     = "failed"
)

var (
	voiceLanguages   = map[string]bool{"en": true, "ha": true, "yo": true, "ig": true, "pcm": true}
	voiceStatuses    = map[string]bool{VoiceStatusQueued: true, VoiceStatusRinging: true, VoiceStatusInProgress: true, VoiceStatusCompleted: true, VoiceStatusMissed: true, VoiceStatusFailed: true}
	promptCategories = map[string]bool{"greeting": true, "menu": true, "confirmation": true, "error": true, "goodbye": true}
)

func IsVoiceLanguage(value string) bool  { return voiceLanguages[value] }
func IsVoiceStatus(value string) bool    { return voiceStatuses[value] }
func IsPromptCategory(value string) bool { return promptCategories[value] }

// IVRPrompt is one spoken line of the phone menu, written per language.
type IVRPrompt struct {
	ID           string            `json:"id"`
	Name         string            `json:"name"`
	Category     string            `json:"category"`
	Translations map[string]string `json:"translations"`
	KeyPress     string            `json:"key_press"`
	IsActive     bool              `json:"is_active"`
	CreatedBy    string            `json:"created_by"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// VoiceCall is one call the platform placed or took.
type VoiceCall struct {
	ID              string     `json:"id"`
	ProviderCallID  string     `json:"provider_call_id"`
	Direction       string     `json:"direction"`
	FromNumber      string     `json:"from_number"`
	ToNumber        string     `json:"to_number"`
	ContactName     string     `json:"contact_name"`
	Status          string     `json:"status"`
	Language        string     `json:"language"`
	StartedAt       time.Time  `json:"started_at"`
	AnsweredAt      *time.Time `json:"answered_at"`
	EndedAt         *time.Time `json:"ended_at"`
	DurationSeconds int        `json:"duration_seconds"`
	RecordingURL    string     `json:"recording_url"`
	Notes           string     `json:"notes"`
	CreatedBy       string     `json:"created_by"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type VoiceCallFilter struct {
	Direction    string
	Status       string
	Query        string
	HasRecording bool
	Page         int
	Limit        int
}

// VoiceDayStat is one day of the call dashboard.
type VoiceDayStat struct {
	Day       string `json:"day"`
	Total     int    `json:"total"`
	Completed int    `json:"completed"`
	Missed    int    `json:"missed"`
	Failed    int    `json:"failed"`
}

type VoiceStats struct {
	Total           int            `json:"total"`
	Completed       int            `json:"completed"`
	Missed          int            `json:"missed"`
	Failed          int            `json:"failed"`
	AnswerRate      int            `json:"answer_rate"`
	AverageDuration int            `json:"average_duration_seconds"`
	ByLanguage      map[string]int `json:"by_language"`
	Days            []VoiceDayStat `json:"days"`
}

// SummarizeVoiceCalls builds the dashboard from calls already filtered to the
// window. Answer rate is completed over calls that reached an outcome.
func SummarizeVoiceCalls(calls []VoiceCall, days []string) VoiceStats {
	stats := VoiceStats{ByLanguage: map[string]int{}, Days: make([]VoiceDayStat, 0, len(days))}
	byDay := make(map[string]*VoiceDayStat, len(days))
	for _, day := range days {
		stats.Days = append(stats.Days, VoiceDayStat{Day: day})
		byDay[day] = &stats.Days[len(stats.Days)-1]
	}
	talk, talked := 0, 0
	for _, call := range calls {
		stats.Total++
		stats.ByLanguage[call.Language]++
		day := byDay[call.StartedAt.UTC().Format("2006-01-02")]
		if day != nil {
			day.Total++
		}
		switch call.Status {
		case VoiceStatusCompleted:
			stats.Completed++
			talk, talked = talk+call.DurationSeconds, talked+1
			if day != nil {
				day.Completed++
			}
		case VoiceStatusMissed:
			stats.Missed++
			if day != nil {
				day.Missed++
			}
		case VoiceStatusFailed:
			stats.Failed++
			if day != nil {
				day.Failed++
			}
		}
	}
	if outcomes := stats.Completed + stats.Missed + stats.Failed; outcomes > 0 {
		stats.AnswerRate = stats.Completed * 100 / outcomes
	}
	if talked > 0 {
		stats.AverageDuration = talk / talked
	}
	return stats
}
