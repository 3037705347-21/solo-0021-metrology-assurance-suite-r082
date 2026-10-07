package domain

import (
	"fmt"
	"strings"
	"time"
)

const (
	RevisionKindOpened  = "opened"
	RevisionKindRevised = "revised"
)

// CaseRevision captures the effective scope (device, nominal value, and
// tolerance) of a verification case at a specific version. The revision that
// records case opening has kind "opened"; every later scope revision has kind
// "revised" and keeps the actor-supplied reason and basis so superseded
// versions remain traceable.
type CaseRevision struct {
	ID        string    `json:"id"`
	CaseID    string    `json:"case_id"`
	Version   int       `json:"version"`
	DeviceID  string    `json:"device_id"`
	Nominal   float64   `json:"nominal"`
	Tolerance float64   `json:"tolerance"`
	Kind      string    `json:"kind"`
	Actor     string    `json:"actor"`
	Reason    string    `json:"reason"`
	Basis     string    `json:"basis,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

func NewOpeningRevision(id, caseID string, version int, deviceID string, nominal, tolerance float64, actor string, at time.Time) (CaseRevision, error) {
	item := CaseRevision{
		ID:        strings.TrimSpace(id),
		CaseID:    strings.TrimSpace(caseID),
		Version:   version,
		DeviceID:  strings.TrimSpace(deviceID),
		Nominal:   nominal,
		Tolerance: tolerance,
		Kind:      RevisionKindOpened,
		Actor:     strings.TrimSpace(actor),
		Reason:    "verification case opened",
		CreatedAt: at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return CaseRevision{}, err
	}
	return item, nil
}

func NewCaseRevision(id, caseID string, version int, deviceID string, nominal, tolerance float64, actor, reason, basis string, at time.Time) (CaseRevision, error) {
	item := CaseRevision{
		ID:        strings.TrimSpace(id),
		CaseID:    strings.TrimSpace(caseID),
		Version:   version,
		DeviceID:  strings.TrimSpace(deviceID),
		Nominal:   nominal,
		Tolerance: tolerance,
		Kind:      RevisionKindRevised,
		Actor:     strings.TrimSpace(actor),
		Reason:    strings.TrimSpace(reason),
		Basis:     strings.TrimSpace(basis),
		CreatedAt: at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return CaseRevision{}, err
	}
	return item, nil
}

func (r CaseRevision) Validate() error {
	if r.ID == "" || r.CaseID == "" || r.Actor == "" || r.Reason == "" {
		return fmt.Errorf("%w: revision id, case id, actor, and reason are required", ErrInvalidInput)
	}
	if r.Version < 1 {
		return fmt.Errorf("%w: revision version must be positive", ErrInvalidInput)
	}
	if r.DeviceID == "" {
		return fmt.Errorf("%w: revision device id is required", ErrInvalidInput)
	}
	if !isFinite(r.Nominal) || !isFinite(r.Tolerance) || r.Tolerance <= 0 {
		return fmt.Errorf("%w: nominal must be finite and tolerance must be positive", ErrInvalidInput)
	}
	if r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: revision time is required", ErrInvalidInput)
	}
	switch r.Kind {
	case RevisionKindOpened, RevisionKindRevised:
	default:
		return fmt.Errorf("%w: unsupported revision kind %q", ErrInvalidInput, r.Kind)
	}
	if r.Kind == RevisionKindRevised && r.Basis == "" {
		return fmt.Errorf("%w: revision basis is required", ErrInvalidInput)
	}
	return nil
}
