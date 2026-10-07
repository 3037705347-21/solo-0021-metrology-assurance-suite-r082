package domain

import (
	"errors"
	"math"
	"testing"
	"time"
)

func validCase(t *testing.T, state CaseState, version int) VerificationCase {
	t.Helper()
	at := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	item, err := NewVerificationCase("case-1", "device-1", 25.0, 0.5, "Lin", at)
	if err != nil {
		t.Fatalf("NewVerificationCase returned error: %v", err)
	}
	item.State = state
	item.Version = version
	return item
}

func TestReviseCreatesNextScopeVersion(t *testing.T) {
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	item := validCase(t, CaseAwaitingMeasurement, 1)

	if err := item.Revise("device-2", 26.0, 0.2, 1, false, at); err != nil {
		t.Fatalf("Revise returned error: %v", err)
	}
	if item.Version != 2 {
		t.Errorf("version = %d, expected 2", item.Version)
	}
	if item.DeviceID != "device-2" || item.Nominal != 26.0 || item.Tolerance != 0.2 {
		t.Errorf("scope not applied: device=%q nominal=%v tolerance=%v", item.DeviceID, item.Nominal, item.Tolerance)
	}
	if item.State != CaseAwaitingMeasurement {
		t.Errorf("state = %q, expected awaiting_measurement", item.State)
	}
	if !item.UpdatedAt.Equal(at) {
		t.Errorf("updated_at = %v, expected %v", item.UpdatedAt, at)
	}
}

func TestReviseRejectedAfterMeasurements(t *testing.T) {
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	for _, state := range []CaseState{CaseReviewReady, CaseRecheckRequired} {
		item := validCase(t, state, 1)
		err := item.Revise("device-1", 26.0, 0.2, 1, true, at)
		if !errors.Is(err, ErrStateConflict) {
			t.Errorf("state %s: error = %v, expected ErrStateConflict", state, err)
		}
	}
}

func TestReviseRejectedWhenMeasurementsExist(t *testing.T) {
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	item := validCase(t, CaseAwaitingMeasurement, 1)
	if err := item.Revise("device-2", 26.0, 0.2, 1, true, at); !errors.Is(err, ErrStateConflict) {
		t.Errorf("error = %v, expected ErrStateConflict", err)
	}
	if item.Version != 1 || item.DeviceID != "device-1" {
		t.Errorf("case mutated after rejected revision: version=%d device=%q", item.Version, item.DeviceID)
	}
}

func TestReviseRejectedWhenSealed(t *testing.T) {
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	item := validCase(t, CaseSealed, 1)
	if err := item.Revise("device-1", 26.0, 0.2, 1, false, at); !errors.Is(err, ErrStateConflict) {
		t.Errorf("sealed revise error = %v, expected ErrStateConflict", err)
	}
}

func TestReviseVersionConflict(t *testing.T) {
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	item := validCase(t, CaseAwaitingMeasurement, 3)
	err := item.Revise("device-1", 26.0, 0.2, 2, false, at)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("error = %v, expected ErrVersionConflict", err)
	}
	if item.Version != 3 || item.Nominal != 25.0 {
		t.Errorf("case mutated after failed revision: version=%d nominal=%v", item.Version, item.Nominal)
	}
}

func TestReviseRejectsUnchangedScopeAndInvalidInput(t *testing.T) {
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	base := validCase(t, CaseAwaitingMeasurement, 1)

	cases := []struct {
		name      string
		deviceID  string
		nominal   float64
		tolerance float64
		expected  int
	}{
		{"unchanged", "device-1", 25.0, 0.5, 1},
		{"empty device", "  ", 26.0, 0.2, 1},
		{"non-positive tolerance", "device-2", 26.0, 0, 1},
		{"NaN nominal", "device-2", float64NaN(), 0.2, 1},
		{"zero expected version", "device-2", 26.0, 0.2, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := base
			if err := item.Revise(tc.deviceID, tc.nominal, tc.tolerance, tc.expected, false, at); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("error = %v, expected ErrInvalidInput", err)
			}
		})
	}
}

func TestStateTransitionsDoNotChangeScopeVersion(t *testing.T) {
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)

	awaiting := validCase(t, CaseAwaitingMeasurement, 2)
	if err := awaiting.RecordMeasurement(false, at); err != nil {
		t.Fatalf("RecordMeasurement returned error: %v", err)
	}
	if awaiting.Version != 2 || awaiting.State != CaseRecheckRequired {
		t.Errorf("after rejected measurement: version=%d state=%q", awaiting.Version, awaiting.State)
	}
	if err := awaiting.Reopen(at); err != nil {
		t.Fatalf("Reopen returned error: %v", err)
	}
	if awaiting.Version != 2 || awaiting.State != CaseAwaitingMeasurement {
		t.Errorf("after reopen: version=%d state=%q", awaiting.Version, awaiting.State)
	}
	if err := awaiting.RecordMeasurement(true, at); err != nil {
		t.Fatalf("RecordMeasurement returned error: %v", err)
	}
	if awaiting.Version != 2 || awaiting.State != CaseReviewReady {
		t.Errorf("after accepted measurement: version=%d state=%q", awaiting.Version, awaiting.State)
	}
	if err := awaiting.Seal(at); err != nil {
		t.Fatalf("Seal returned error: %v", err)
	}
	if awaiting.Version != 2 || awaiting.State != CaseSealed {
		t.Errorf("after seal: version=%d state=%q", awaiting.Version, awaiting.State)
	}
}

func TestNewMeasurementBindsCaseVersion(t *testing.T) {
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	item, err := NewMeasurement("measurement-1", "case-1", 2, 26.5, 26.0, 0.5, "Rui", at)
	if err != nil {
		t.Fatalf("NewMeasurement returned error: %v", err)
	}
	if item.CaseVersion != 2 {
		t.Errorf("case_version = %d, expected 2", item.CaseVersion)
	}
	if !item.Accepted || item.Deviation != 0.5 {
		t.Errorf("accepted=%v deviation=%v, expected accepted deviation 0.5", item.Accepted, item.Deviation)
	}
}

func float64NaN() float64 {
	return math.NaN()
}
