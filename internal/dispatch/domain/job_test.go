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

func mustAssign(t *testing.T, job Job, driverID, vehicleID string) Job {
	t.Helper()
	assigned, err := job.Assign(driverID, "", vehicleID, job.UpdatedAt, "tl-"+driverID+vehicleID)
	if err != nil {
		t.Fatalf("assign driver=%q vehicle=%q: %v", driverID, vehicleID, err)
	}
	return assigned
}

func lastTimelineEntry(t *testing.T, job Job) JobTimelineEntry {
	t.Helper()
	if len(job.Timeline) == 0 {
		t.Fatal("expected a timeline entry")
	}
	return job.Timeline[len(job.Timeline)-1]
}

func TestJob_Assign_TimelineNamesWhatChanged(t *testing.T) {
	withVehicle := mustAssign(t, validJobFixture(t), "", "vehicle-1")
	if entry := lastTimelineEntry(t, withVehicle); entry.Type != "vehicle_assigned" || entry.Summary != "Vehicle assigned" {
		t.Fatalf("vehicle alone: expected vehicle_assigned/Vehicle assigned, got %s/%s", entry.Type, entry.Summary)
	}

	withDriver := mustAssign(t, withVehicle, "driver-1", "")
	if entry := lastTimelineEntry(t, withDriver); entry.Type != "job_assigned" || entry.Metadata["previous_driver_id"] != "" {
		t.Fatalf("first driver after a vehicle: expected job_assigned, got %s %v", entry.Type, entry.Metadata)
	}

	swapped := mustAssign(t, withDriver, "", "vehicle-2")
	entry := lastTimelineEntry(t, swapped)
	if entry.Type != "vehicle_assigned" || entry.Summary != "Vehicle changed" || entry.Metadata["previous_vehicle_id"] != "vehicle-1" {
		t.Fatalf("vehicle swap: expected vehicle_assigned/Vehicle changed from vehicle-1, got %s/%s %v", entry.Type, entry.Summary, entry.Metadata)
	}

	reassigned := mustAssign(t, swapped, "driver-2", "")
	if entry := lastTimelineEntry(t, reassigned); entry.Type != "job_reassigned" || entry.Metadata["previous_driver_id"] != "driver-1" {
		t.Fatalf("driver change: expected job_reassigned from driver-1, got %s %v", entry.Type, entry.Metadata)
	}
}

func TestJob_Assign_VehicleSwapKeepsAnInProgressStatus(t *testing.T) {
	job := mustAssign(t, validJobFixture(t), "driver-1", "vehicle-1")
	job.Status = StatusEnRoute

	swapped := mustAssign(t, job, "", "vehicle-2")
	if swapped.Status != StatusEnRoute {
		t.Fatalf("expected %q kept, got %q", StatusEnRoute, swapped.Status)
	}
}

func TestJob_Assign_SameDriverAgainLogsNothing(t *testing.T) {
	job := mustAssign(t, validJobFixture(t), "driver-1", "vehicle-1")

	again := mustAssign(t, job, "driver-1", "vehicle-1")
	if len(again.Timeline) != len(job.Timeline) {
		t.Fatalf("expected no new timeline entry, got %d -> %d", len(job.Timeline), len(again.Timeline))
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

func TestJob_UpdateStatus_StampsDeliveredAtOnce(t *testing.T) {
	job := validJobFixture(t)
	now := job.UpdatedAt.Add(time.Hour)
	for index, status := range []JobStatus{StatusAssigned, StatusEnRoute, StatusArrived, StatusLoaded} {
		next, err := job.UpdateStatus(status, now, "t")
		if err != nil {
			t.Fatalf("status %s: %v", status, err)
		}
		if next.DeliveredAt != nil {
			t.Fatalf("step %d: delivered_at set before delivery", index)
		}
		job = next
	}

	deliveredAt := now.Add(2 * time.Hour)
	delivered, err := job.UpdateStatus(StatusDelivered, deliveredAt, "t")
	if err != nil {
		t.Fatalf("deliver: %v", err)
	}
	if delivered.DeliveredAt == nil || !delivered.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("expected delivered_at %v, got %v", deliveredAt, delivered.DeliveredAt)
	}

	again, err := delivered.UpdateStatus(StatusDelivered, deliveredAt.Add(time.Hour), "t")
	if err != nil {
		t.Fatalf("repeat deliver: %v", err)
	}
	if !again.DeliveredAt.Equal(deliveredAt) {
		t.Fatalf("expected delivered_at to stay %v, got %v", deliveredAt, again.DeliveredAt)
	}
}

func TestJob_ConfirmDriverStatusRequest_StampsDeliveredAt(t *testing.T) {
	job := validJobFixture(t)
	now := job.UpdatedAt.Add(time.Hour)
	job, _ = job.Assign("driver-1", "", "", now, "a")
	job.Status = StatusLoaded

	reported, request, err := job.ReportDriverStatusRequest(StatusDelivered, "driver-1", "", "delivered", "", "msg-1", "whatsapp", now, "req-1", "t1")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	confirmed, _, err := reported.ConfirmDriverStatusRequest(request.ID, "dispatcher", now.Add(time.Minute), "t2", "t3")
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	if confirmed.DeliveredAt == nil || !confirmed.DeliveredAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("expected delivered_at at confirmation, got %v", confirmed.DeliveredAt)
	}
}

func TestJob_DriverInstructionOutcomesAreRecorded(t *testing.T) {
	job := validJobFixture(t)
	now := job.UpdatedAt.Add(time.Hour)
	job, _ = job.Assign("driver-1", "+2348012345678", "", now, "a")

	notSent := job.MarkDriverInstructionNotSent("whatsapp", "no WhatsApp number", now, "n1")
	last := notSent.Timeline[len(notSent.Timeline)-1]
	if last.Type != "driver_instruction_not_sent" || last.Metadata["reason"] != "no WhatsApp number" {
		t.Fatalf("unexpected entry %+v", last)
	}

	failed := job.MarkDriverInstructionFailed("whatsapp", "template rejected", now, "n2")
	if got := failed.Timeline[len(failed.Timeline)-1].Type; got != "driver_instruction_failed" {
		t.Fatalf("unexpected entry type %s", got)
	}

	sent := job.MarkDriverInstructionSent("whatsapp", now, "n3")
	if _, hasReason := sent.Timeline[len(sent.Timeline)-1].Metadata["reason"]; hasReason {
		t.Fatal("a sent entry carries no reason")
	}
}

func TestJob_CargoDetailsAreKeptAndValidated(t *testing.T) {
	now := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	job, err := NewJob(Job{
		ID: "job-1", TenantID: "tenant-a",
		Pickup:  JobStop{Address: "Apapa", ContactName: " Ada ", CompanyName: "Acme", TimeFrom: "08:00"},
		Dropoff: JobStop{Address: "Ikeja"},
		JobCargoDetails: JobCargoDetails{
			Category: " Electronics ", PackagesCount: 4, SpecialHandling: []string{" Fragile ", ""},
			Dimensions: &JobDimensions{LengthCm: 10},
		},
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if job.Category != "Electronics" || len(job.SpecialHandling) != 1 || job.Dimensions.LengthCm != 10 {
		t.Fatalf("cargo details not kept: %+v", job.JobCargoDetails)
	}
	if job.Pickup.ContactName != "Ada" || job.Pickup.CompanyName != "Acme" || job.Pickup.TimeFrom != "08:00" {
		t.Fatalf("stop details not kept: %+v", job.Pickup)
	}

	if _, err := NewJob(Job{
		ID: "job-2", TenantID: "tenant-a", Pickup: JobStop{Address: "A"}, Dropoff: JobStop{Address: "B"},
		JobCargoDetails: JobCargoDetails{PackagesCount: -1}, CreatedAt: now, UpdatedAt: now,
	}); err == nil {
		t.Fatal("expected negative packages to be refused")
	}
}

func TestJob_AddAttachmentKeepsMetadata(t *testing.T) {
	job := validJobFixture(t)
	now := job.UpdatedAt.Add(time.Hour)
	updated, err := job.AddAttachment(JobAttachment{ID: "att-1", Name: "bol.pdf", SizeBytes: 10}, now, "t1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(updated.Attachments) != 1 || !updated.Attachments[0].UploadedAt.Equal(now) || len(job.Attachments) != 0 {
		t.Fatalf("unexpected attachments %+v", updated.Attachments)
	}
}
