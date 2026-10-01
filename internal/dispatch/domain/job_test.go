package domain

import (
	"testing"
	"time"
)

func validJobFixture(t *testing.T) Job {
	t.Helper()
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	job, err := NewJob(Job{
		ID:        "job-1",
		TenantID:  "tenant-a",
		Pickup:    JobStop{Address: "Apapa Port"},
		Dropoff:   JobStop{Address: "Lekki Free Zone"},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("fixture: unexpected error building job: %v", err)
	}
	return job
}

func TestJob_UpdateDetails_HappyPath(t *testing.T) {
	job := validJobFixture(t)
	now := job.UpdatedAt.Add(time.Hour)
	deadline := now.Add(48 * time.Hour)

	update := JobDetailsUpdate{
		Pickup:           JobStop{Address: " New Pickup ", ContactName: " John "},
		Dropoff:          JobStop{Address: " New Dropoff "},
		Stops:            []JobStop{{Address: " Stop 1 "}},
		CargoType:        " Electronics ",
		CargoDescription: " Fragile ",
		AmountNgn:        50000,
		Deadline:         &deadline,
		LoadWeightKg:     120.5,
		LoadVolumeM3:     3.2,
	}

	updated, err := job.UpdateDetails(update, now, "timeline-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated.Pickup.Address != "New Pickup" || updated.Pickup.ContactName != "John" {
		t.Errorf("expected trimmed pickup, got %#v", updated.Pickup)
	}
	if updated.Dropoff.Address != "New Dropoff" {
		t.Errorf("expected trimmed dropoff, got %#v", updated.Dropoff)
	}
	if len(updated.Stops) != 1 || updated.Stops[0].Address != "Stop 1" {
		t.Errorf("expected 1 trimmed stop, got %#v", updated.Stops)
	}
	if updated.CargoType != "Electronics" || updated.CargoDescription != "Fragile" {
		t.Errorf("expected trimmed cargo fields, got %q/%q", updated.CargoType, updated.CargoDescription)
	}
	if updated.AmountNgn != 50000 {
		t.Errorf("expected amount 50000, got %v", updated.AmountNgn)
	}
	if updated.Deadline == nil || !updated.Deadline.Equal(deadline.UTC()) {
		t.Errorf("expected deadline %v, got %v", deadline.UTC(), updated.Deadline)
	}
	if updated.LoadWeightKg != 120.5 || updated.LoadVolumeM3 != 3.2 {
		t.Errorf("expected load weight/volume 120.5/3.2, got %v/%v", updated.LoadWeightKg, updated.LoadVolumeM3)
	}
	if !updated.UpdatedAt.Equal(now.UTC()) {
		t.Errorf("expected updated_at %v, got %v", now.UTC(), updated.UpdatedAt)
	}
	if len(updated.Timeline) != 1 {
		t.Fatalf("expected 1 timeline entry, got %d", len(updated.Timeline))
	}
	entry := updated.Timeline[0]
	if entry.Type != "job_details_updated" || entry.ID != "timeline-1" {
		t.Errorf("expected job_details_updated/timeline-1 entry, got %#v", entry)
	}
}

func TestJob_UpdateDetails_Validation(t *testing.T) {
	base := validJobFixture(t)
	now := base.UpdatedAt.Add(time.Hour)

	validUpdate := func() JobDetailsUpdate {
		return JobDetailsUpdate{
			Pickup:       JobStop{Address: "Apapa Port"},
			Dropoff:      JobStop{Address: "Lekki Free Zone"},
			AmountNgn:    1000,
			LoadWeightKg: 10,
			LoadVolumeM3: 1,
		}
	}

	cases := []struct {
		name   string
		mutate func(JobDetailsUpdate) JobDetailsUpdate
	}{
		{"empty pickup address", func(u JobDetailsUpdate) JobDetailsUpdate {
			u.Pickup.Address = "   "
			return u
		}},
		{"empty dropoff address", func(u JobDetailsUpdate) JobDetailsUpdate {
			u.Dropoff.Address = ""
			return u
		}},
		{"negative amount_ngn", func(u JobDetailsUpdate) JobDetailsUpdate {
			u.AmountNgn = -1
			return u
		}},
		{"negative load_weight_kg", func(u JobDetailsUpdate) JobDetailsUpdate {
			u.LoadWeightKg = -1
			return u
		}},
		{"negative load_volume_m3", func(u JobDetailsUpdate) JobDetailsUpdate {
			u.LoadVolumeM3 = -1
			return u
		}},
	}

	for _, c := range cases {
		update := c.mutate(validUpdate())
		if _, err := base.UpdateDetails(update, now, "timeline-x"); err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestJob_UpdateDetails_RejectsTerminalStatuses(t *testing.T) {
	base := validJobFixture(t)
	now := base.UpdatedAt.Add(time.Hour)
	update := JobDetailsUpdate{
		Pickup:  JobStop{Address: "Apapa Port"},
		Dropoff: JobStop{Address: "Lekki Free Zone"},
	}

	for _, status := range []JobStatus{StatusCancelled, StatusDelivered} {
		job := base
		job.Status = status
		if _, err := job.UpdateDetails(update, now, "timeline-x"); err == nil {
			t.Errorf("status %s: expected error, got nil", status)
		}
	}
}

func TestNormalizeStops_EmptyInputs(t *testing.T) {
	if got := normalizeStops(nil); got != nil {
		t.Errorf("nil input: expected nil, got %#v", got)
	}
	if got := normalizeStops([]JobStop{}); got != nil {
		t.Errorf("empty slice: expected nil, got %#v", got)
	}
}

func TestNormalizeStops_FiltersAndTrims(t *testing.T) {
	lat, lng := 6.5, 3.4
	input := []JobStop{
		{Address: "   ", Latitude: nil, Longitude: nil},
		{Address: "  Warehouse A  ", ContactName: " Jane ", ContactPhone: " 0801 ", Notes: " fragile "},
		{Address: "", Latitude: &lat, Longitude: &lng},
	}

	got := normalizeStops(input)
	if len(got) != 2 {
		t.Fatalf("expected 2 stops after filtering, got %d: %#v", len(got), got)
	}

	withAddress := got[0]
	if withAddress.Address != "Warehouse A" || withAddress.ContactName != "Jane" ||
		withAddress.ContactPhone != "0801" || withAddress.Notes != "fragile" {
		t.Errorf("expected trimmed fields, got %#v", withAddress)
	}

	coordsOnly := got[1]
	if coordsOnly.Address != "" || coordsOnly.Latitude == nil || *coordsOnly.Latitude != lat ||
		coordsOnly.Longitude == nil || *coordsOnly.Longitude != lng {
		t.Errorf("expected lat/lng-only stop kept, got %#v", coordsOnly)
	}
}

func TestJob_Assign_VehicleAloneKeepsJobWaitingForDriver(t *testing.T) {
	job := validJobFixture(t)

	assigned, err := job.Assign("", "", "vehicle-1", job.UpdatedAt, "tl-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assigned.AssignedVehicleID != "vehicle-1" || assigned.AssignedDriverID != "" {
		t.Fatalf("expected vehicle only, got driver=%q vehicle=%q", assigned.AssignedDriverID, assigned.AssignedVehicleID)
	}
	if assigned.Status != StatusCreated {
		t.Fatalf("expected status %q, got %q", StatusCreated, assigned.Status)
	}
}

func TestJob_Assign_DriverAloneAssignsAndKeepsVehicle(t *testing.T) {
	job := validJobFixture(t)
	withVehicle, err := job.Assign("", "", "vehicle-1", job.UpdatedAt, "tl-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assigned, err := withVehicle.Assign("driver-1", "+2348000000000", "", job.UpdatedAt, "tl-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if assigned.AssignedDriverID != "driver-1" || assigned.AssignedVehicleID != "vehicle-1" {
		t.Fatalf("expected both kept, got driver=%q vehicle=%q", assigned.AssignedDriverID, assigned.AssignedVehicleID)
	}
	if assigned.Status != StatusAssigned {
		t.Fatalf("expected status %q, got %q", StatusAssigned, assigned.Status)
	}
}

func TestJob_Assign_VehicleAloneKeepsDriverAndPhone(t *testing.T) {
	job := validJobFixture(t)
	withDriver, err := job.Assign("driver-1", "+2348000000000", "", job.UpdatedAt, "tl-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	reassigned, err := withDriver.Assign("", "", "vehicle-2", job.UpdatedAt, "tl-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reassigned.AssignedDriverID != "driver-1" || reassigned.AssignedDriverPhone != "+2348000000000" {
		t.Fatalf("expected driver kept, got driver=%q phone=%q", reassigned.AssignedDriverID, reassigned.AssignedDriverPhone)
	}
	if reassigned.AssignedVehicleID != "vehicle-2" {
		t.Fatalf("expected vehicle-2, got %q", reassigned.AssignedVehicleID)
	}
}

func TestJob_Assign_RequiresDriverOrVehicle(t *testing.T) {
	job := validJobFixture(t)

	if _, err := job.Assign(" ", "", " ", job.UpdatedAt, "tl-1"); err == nil {
		t.Fatal("expected an error when neither driver nor vehicle is sent")
	}
}

func TestJob_PatchDetails_KeepsFieldsNotSent(t *testing.T) {
	job := validJobFixture(t)
	deadline := job.UpdatedAt.Add(48 * time.Hour)
	job.Deadline = &deadline
	weight := 8500.0

	patched, err := job.PatchDetails(JobDetailsPatch{LoadWeightKg: &weight}, job.UpdatedAt, "tl-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if patched.LoadWeightKg != 8500 {
		t.Fatalf("expected weight 8500, got %v", patched.LoadWeightKg)
	}
	if patched.Pickup.Address != "Apapa Port" || patched.Dropoff.Address != "Lekki Free Zone" {
		t.Fatalf("expected stops kept, got %q/%q", patched.Pickup.Address, patched.Dropoff.Address)
	}
	if patched.Deadline == nil || !patched.Deadline.Equal(deadline) {
		t.Fatalf("expected deadline kept, got %v", patched.Deadline)
	}
}

func TestJob_PatchDetails_ClearsDeadline(t *testing.T) {
	job := validJobFixture(t)
	deadline := job.UpdatedAt.Add(48 * time.Hour)
	job.Deadline = &deadline

	patched, err := job.PatchDetails(JobDetailsPatch{ClearDeadline: true}, job.UpdatedAt, "tl-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if patched.Deadline != nil {
		t.Fatalf("expected deadline cleared, got %v", patched.Deadline)
	}
}

func TestJob_PatchDetails_StillRejectsBlankPickup(t *testing.T) {
	job := validJobFixture(t)

	if _, err := job.PatchDetails(JobDetailsPatch{Pickup: &JobStop{Address: " "}}, job.UpdatedAt, "tl-1"); err == nil {
		t.Fatal("expected an error when pickup is sent blank")
	}
}
