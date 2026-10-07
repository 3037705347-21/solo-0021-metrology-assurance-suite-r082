package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
)

func at() time.Time { return time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC) }

func seedCase(t *testing.T, repo *Repository, caseID, deviceID string, version int, state domain.CaseState) domain.VerificationCase {
	t.Helper()
	item, err := domain.NewVerificationCase(caseID, deviceID, 25.0, 0.5, "Lin", at())
	if err != nil {
		t.Fatalf("NewVerificationCase: %v", err)
	}
	item.State = state
	item.Version = version
	err = repo.Mutate(context.Background(), func(m *Mutation) error {
		if err := m.PutCase(item); err != nil {
			return err
		}
		return m.AppendRevision(domain.CaseRevision{
			ID:        "rev-1",
			CaseID:    caseID,
			Version:   1,
			DeviceID:  deviceID,
			Nominal:   25.0,
			Tolerance: 0.5,
			Kind:      domain.RevisionKindOpened,
			Actor:     "Lin",
			Reason:    "opened",
			CreatedAt: at(),
		})
	})
	if err != nil {
		t.Fatalf("seed mutate: %v", err)
	}
	return item
}

func TestRevisionsAreRetainedPerVersion(t *testing.T) {
	repo := New()
	seedCase(t, repo, "case-1", "device-1", 1, domain.CaseAwaitingMeasurement)

	err := repo.Mutate(context.Background(), func(m *Mutation) error {
		return m.AppendRevision(domain.CaseRevision{
			ID:        "rev-2",
			CaseID:    "case-1",
			Version:   2,
			DeviceID:  "device-2",
			Nominal:   26.0,
			Tolerance: 0.2,
			Kind:      domain.RevisionKindRevised,
			Actor:     "Lin",
			Reason:    "field correction",
			Basis:     "certificate",
			CreatedAt: at(),
		})
	})
	if err != nil {
		t.Fatalf("AppendRevision: %v", err)
	}

	var revisions []domain.CaseRevision
	if err := repo.Read(context.Background(), func(snapshot Snapshot) error {
		revisions = snapshot.Revisions("case-1")
		return nil
	}); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(revisions) != 2 || revisions[0].Version != 1 || revisions[1].Version != 2 {
		t.Fatalf("revisions = %+v", revisions)
	}
	if revisions[0].DeviceID != "device-1" || revisions[1].DeviceID != "device-2" {
		t.Errorf("superseded scope was not retained: %+v", revisions)
	}
}

func TestAppendRevisionRejectsDuplicateVersion(t *testing.T) {
	repo := New()
	seedCase(t, repo, "case-1", "device-1", 1, domain.CaseAwaitingMeasurement)

	err := repo.Mutate(context.Background(), func(m *Mutation) error {
		rev := domain.CaseRevision{
			ID: "rev-dup", CaseID: "case-1", Version: 1, DeviceID: "device-1",
			Nominal: 25, Tolerance: 0.5, Kind: domain.RevisionKindRevised,
			Actor: "Lin", Reason: "r", Basis: "b", CreatedAt: at(),
		}
		return m.AppendRevision(rev)
	})
	if !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("error = %v, expected ErrStateConflict", err)
	}
}

func TestActiveCaseForDeviceExcept(t *testing.T) {
	repo := New()
	seedCase(t, repo, "case-1", "device-1", 1, domain.CaseAwaitingMeasurement)
	seedCase(t, repo, "case-2", "device-2", 1, domain.CaseAwaitingMeasurement)

	err := repo.Mutate(context.Background(), func(m *Mutation) error {
		if active, exists := m.ActiveCaseForDeviceExcept("device-1", "case-1"); exists {
			t.Errorf("self should be excluded, found %q", active.ID)
		}
		if active, exists := m.ActiveCaseForDeviceExcept("device-2", "case-1"); !exists || active.ID != "case-2" {
			t.Errorf("expected other device's active case-2, got %q (exists=%v)", active.ID, exists)
		}
		if _, exists := m.ActiveCaseForDeviceExcept("device-1", "case-2"); !exists {
			t.Errorf("device-1's active case should still block")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Mutate: %v", err)
	}
}
