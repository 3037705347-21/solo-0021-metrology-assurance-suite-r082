package domain

import (
	"errors"
	"testing"
	"time"
)

func TestOpeningAndRevisedRevisions(t *testing.T) {
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	opening, err := NewOpeningRevision("revision-1", "case-1", 1, "device-1", 25.0, 0.5, "Lin", at)
	if err != nil {
		t.Fatalf("NewOpeningRevision returned error: %v", err)
	}
	if opening.Kind != RevisionKindOpened || opening.Reason == "" {
		t.Errorf("opening revision kind=%q reason=%q", opening.Kind, opening.Reason)
	}

	revised, err := NewCaseRevision("revision-2", "case-1", 2, "device-2", 26.0, 0.2, "Lin", "field correction", "certificate C-9", at)
	if err != nil {
		t.Fatalf("NewCaseRevision returned error: %v", err)
	}
	if revised.Kind != RevisionKindRevised || revised.Basis != "certificate C-9" {
		t.Errorf("revised revision kind=%q basis=%q", revised.Kind, revised.Basis)
	}
}

func TestRevisedRevisionRequiresBasis(t *testing.T) {
	at := time.Date(2026, 10, 7, 10, 0, 0, 0, time.UTC)
	if _, err := NewCaseRevision("revision-2", "case-1", 2, "device-2", 26.0, 0.2, "Lin", "reason", "  ", at); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("error = %v, expected ErrInvalidInput", err)
	}
}
