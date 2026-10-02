package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/truckmoov/services-go/internal/dispatch/domain"
)

type fakeTenantLister struct {
	tenants []string
	err     error
}

func (f fakeTenantLister) ListTenantsWithActiveJobs(context.Context) ([]string, error) {
	return f.tenants, f.err
}

type recordedAlert struct{ tenantID, jobID string }

type fakeAlertPublisher struct{ alerts []recordedAlert }

func (f *fakeAlertPublisher) PublishAlert(_ context.Context, tenantID, _, _, jobID, _ string, _ map[string]any) error {
	f.alerts = append(f.alerts, recordedAlert{tenantID: tenantID, jobID: jobID})
	return nil
}

func lateJob(t *testing.T, tenantID, id string, status domain.JobStatus, dropoffEnd time.Time) domain.Job {
	t.Helper()
	created := dropoffEnd.Add(-48 * time.Hour)
	job, err := domain.NewJob(domain.Job{
		ID: id, TenantID: tenantID, Status: status,
		Pickup: domain.JobStop{Address: "A"}, Dropoff: domain.JobStop{Address: "B"},
		TimeWindow: domain.JobTimeWindow{DropoffEnd: &dropoffEnd},
		CreatedAt:  created, UpdatedAt: created,
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return job
}

func TestEtaMonitor_AlertsLateJobsOfEveryTenantOnce(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	repository := newLifecycleRepository()
	_ = repository.Save(lateJob(t, "tenant-a", "job-a", domain.StatusEnRoute, now.Add(-time.Hour)))
	_ = repository.Save(lateJob(t, "tenant-b", "job-b", domain.StatusAssigned, now.Add(-time.Minute)))
	_ = repository.Save(lateJob(t, "tenant-b", "job-ontime", domain.StatusEnRoute, now.Add(time.Hour)))
	_ = repository.Save(lateJob(t, "tenant-b", "job-delivered", domain.StatusDelivered, now.Add(-time.Hour)))
	publisher := &fakeAlertPublisher{}
	monitor := NewEtaMonitorUseCase(repository, fakeTenantLister{tenants: []string{"tenant-a", "tenant-b"}}, publisher, lifecycleClock{now: now})

	monitor.Check(context.Background())
	monitor.Check(context.Background())

	if len(publisher.alerts) != 2 {
		t.Fatalf("expected one alert per late job, got %+v", publisher.alerts)
	}
	got := map[string]string{}
	for _, alert := range publisher.alerts {
		got[alert.jobID] = alert.tenantID
	}
	if got["job-a"] != "tenant-a" || got["job-b"] != "tenant-b" {
		t.Fatalf("alerts attributed to the wrong tenants: %+v", got)
	}
}

func TestEtaMonitor_SameJobIDInTwoTenantsAlertsBoth(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	repository := newLifecycleRepository()
	_ = repository.Save(lateJob(t, "tenant-a", "job-1", domain.StatusEnRoute, now.Add(-time.Hour)))
	_ = repository.Save(lateJob(t, "tenant-b", "job-1", domain.StatusEnRoute, now.Add(-time.Hour)))
	publisher := &fakeAlertPublisher{}
	monitor := NewEtaMonitorUseCase(repository, fakeTenantLister{tenants: []string{"tenant-a", "tenant-b"}}, publisher, lifecycleClock{now: now})

	monitor.Check(context.Background())

	if len(publisher.alerts) != 2 {
		t.Fatalf("expected an alert for each tenant, got %+v", publisher.alerts)
	}
}

func TestEtaMonitor_DoesNothingWhenTenantsCannotBeListed(t *testing.T) {
	publisher := &fakeAlertPublisher{}
	monitor := NewEtaMonitorUseCase(newLifecycleRepository(), fakeTenantLister{err: errors.New("db down")}, publisher, lifecycleClock{now: time.Now()})

	monitor.Check(context.Background())

	if len(publisher.alerts) != 0 {
		t.Fatalf("expected no alerts, got %+v", publisher.alerts)
	}
}

type flakyAlertPublisher struct {
	fakeAlertPublisher
	failures int
}

func (f *flakyAlertPublisher) PublishAlert(ctx context.Context, tenantID, alertType, severity, jobID, vehicleID string, details map[string]any) error {
	if f.failures > 0 {
		f.failures--
		return errors.New("bus down")
	}
	return f.fakeAlertPublisher.PublishAlert(ctx, tenantID, alertType, severity, jobID, vehicleID, details)
}

func TestEtaMonitor_AlertsALoadedJobThatIsLate(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	repository := newLifecycleRepository()
	_ = repository.Save(lateJob(t, "tenant-a", "job-loaded", domain.StatusLoaded, now.Add(-time.Hour)))
	publisher := &fakeAlertPublisher{}
	monitor := NewEtaMonitorUseCase(repository, fakeTenantLister{tenants: []string{"tenant-a"}}, publisher, lifecycleClock{now: now})

	monitor.Check(context.Background())

	if len(publisher.alerts) != 1 || publisher.alerts[0].jobID != "job-loaded" {
		t.Fatalf("expected one alert for the loaded job, got %+v", publisher.alerts)
	}
}

func TestEtaMonitor_RetriesAnAlertThePublisherRefused(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	repository := newLifecycleRepository()
	_ = repository.Save(lateJob(t, "tenant-a", "job-a", domain.StatusEnRoute, now.Add(-time.Hour)))
	publisher := &flakyAlertPublisher{failures: 1}
	monitor := NewEtaMonitorUseCase(repository, fakeTenantLister{tenants: []string{"tenant-a"}}, publisher, lifecycleClock{now: now})

	monitor.Check(context.Background())
	monitor.Check(context.Background())
	monitor.Check(context.Background())

	if len(publisher.alerts) != 1 {
		t.Fatalf("expected the refused alert to go out once on the next sweep, got %+v", publisher.alerts)
	}
}
