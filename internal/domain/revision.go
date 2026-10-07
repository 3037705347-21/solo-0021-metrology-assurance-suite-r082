package domain

import (
	"fmt"
	"strings"
	"time"
)

// ScopeRevision captures one effective scope version of a verification case.
// Version 1 is recorded when the case opens, and every later on-site
// confirmation appends another revision carrying its reason and basis. Older
// revisions remain stored and traceable for the lifetime of the case.
type ScopeRevision struct {
	ID        string    `json:"id"`
	CaseID    string    `json:"case_id"`
	DeviceID  string    `json:"device_id"`
	Version   int       `json:"version"`
	Nominal   float64   `json:"nominal"`
	Tolerance float64   `json:"tolerance"`
	Reason    string    `json:"reason,omitempty"`
	Basis     string    `json:"basis,omitempty"`
	Actor     string    `json:"actor"`
	CreatedAt time.Time `json:"created_at"`
}

func NewScopeRevision(id, caseID, deviceID string, version int, nominal, tolerance float64, actor, reason, basis string, at time.Time) (ScopeRevision, error) {
	item := ScopeRevision{
		ID:        strings.TrimSpace(id),
		CaseID:    strings.TrimSpace(caseID),
		DeviceID:  strings.TrimSpace(deviceID),
		Version:   version,
		Nominal:   nominal,
		Tolerance: tolerance,
		Reason:    strings.TrimSpace(reason),
		Basis:     strings.TrimSpace(basis),
		Actor:     strings.TrimSpace(actor),
		CreatedAt: at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return ScopeRevision{}, err
	}
	return item, nil
}

func (r ScopeRevision) Validate() error {
	if r.ID == "" || r.CaseID == "" || r.DeviceID == "" || r.Actor == "" {
		return fmt.Errorf("%w: revision id, case id, device id, and actor are required", ErrInvalidInput)
	}
	if !isFinite(r.Nominal) || !isFinite(r.Tolerance) || r.Tolerance <= 0 {
		return fmt.Errorf("%w: nominal must be finite and tolerance must be positive", ErrInvalidInput)
	}
	if r.Version < 1 || r.CreatedAt.IsZero() {
		return fmt.Errorf("%w: revision version and time are required", ErrInvalidInput)
	}
	if r.Version > 1 && (r.Reason == "" || r.Basis == "") {
		return fmt.Errorf("%w: revision reason and basis are required", ErrInvalidInput)
	}
	return nil
}
