package application

import (
	"testing"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

func TestBuildJobStatusMatcher_CommaListMatchesAnyPart(t *testing.T) {
	match, err := buildJobStatusMatcher("assigned, en-route,loaded")
	if err != nil || match == nil {
		t.Fatalf("expected a matcher, err = %v", err)
	}
	for status, want := range map[domain.JobStatus]bool{
		domain.StatusAssigned:  true,
		domain.StatusEnRoute:   true,
		domain.StatusLoaded:    true,
		domain.StatusDelivered: false,
		domain.StatusCreated:   false,
	} {
		if got := match(status); got != want {
			t.Errorf("%s: got %v, want %v", status, got, want)
		}
	}
}

func TestBuildJobStatusMatcher_CommaListRejectsUnknownPart(t *testing.T) {
	if _, err := buildJobStatusMatcher("assigned,flying"); err == nil {
		t.Fatal("an unknown status in the list should be refused")
	}
}
