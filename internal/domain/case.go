package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

type CaseState string

const (
	CaseAwaitingMeasurement CaseState = "awaiting_measurement"
	CaseReviewReady         CaseState = "review_ready"
	CaseRecheckRequired     CaseState = "recheck_required"
	CaseSealed              CaseState = "sealed"
)

type VerificationCase struct {
	ID        string     `json:"id"`
	DeviceID  string     `json:"device_id"`
	Nominal   float64    `json:"nominal"`
	Tolerance float64    `json:"tolerance"`
	Steward   string     `json:"steward"`
	State     CaseState  `json:"state"`
	Version   int        `json:"version"`
	OpenedAt  time.Time  `json:"opened_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	SealedAt  *time.Time `json:"sealed_at,omitempty"`
}

func NewVerificationCase(id, deviceID string, nominal, tolerance float64, steward string, at time.Time) (VerificationCase, error) {
	item := VerificationCase{
		ID:        strings.TrimSpace(id),
		DeviceID:  strings.TrimSpace(deviceID),
		Nominal:   nominal,
		Tolerance: tolerance,
		Steward:   strings.TrimSpace(steward),
		State:     CaseAwaitingMeasurement,
		Version:   1,
		OpenedAt:  at.UTC(),
		UpdatedAt: at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return VerificationCase{}, err
	}
	return item, nil
}

func (c VerificationCase) Validate() error {
	if c.ID == "" || c.DeviceID == "" || c.Steward == "" {
		return fmt.Errorf("%w: case id, device id, and steward are required", ErrInvalidInput)
	}
	if !isFinite(c.Nominal) || !isFinite(c.Tolerance) || c.Tolerance <= 0 {
		return fmt.Errorf("%w: nominal must be finite and tolerance must be positive", ErrInvalidInput)
	}
	if c.Version < 1 || c.OpenedAt.IsZero() || c.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: case version and timestamps are required", ErrInvalidInput)
	}
	switch c.State {
	case CaseAwaitingMeasurement, CaseReviewReady, CaseRecheckRequired, CaseSealed:
		return nil
	default:
		return fmt.Errorf("%w: unsupported case state %q", ErrInvalidInput, c.State)
	}
}

func (c VerificationCase) IsUnfinished() bool {
	return c.State != CaseSealed
}

// RecordMeasurement moves the case through its measurement lifecycle. The
// scope Version is intentionally left untouched: it identifies the effective
// nominal/tolerance/device revision, not every state transition.
func (c *VerificationCase) RecordMeasurement(accepted bool, at time.Time) error {
	if c.State != CaseAwaitingMeasurement {
		return fmt.Errorf("%w: case is not awaiting measurement", ErrStateConflict)
	}
	if accepted {
		c.State = CaseReviewReady
	} else {
		c.State = CaseRecheckRequired
	}
	c.UpdatedAt = at.UTC()
	return nil
}

func (c *VerificationCase) Reopen(at time.Time) error {
	if c.State != CaseRecheckRequired {
		return fmt.Errorf("%w: only a recheck_required case can be reopened", ErrStateConflict)
	}
	c.State = CaseAwaitingMeasurement
	c.UpdatedAt = at.UTC()
	return nil
}

// ReviseScope starts a new effective scope version after on-site confirmation.
// It is only valid while the case is awaiting its first measurement on the
// current scope: an existing measurement or a sealed case must be rejected by
// the caller, which owns the cross-entity checks. Tolerance is validated here
// so a malformed revision can never become the current version.
func (c *VerificationCase) ReviseScope(deviceID string, nominal, tolerance float64, at time.Time) error {
	if c.State != CaseAwaitingMeasurement {
		return fmt.Errorf("%w: only an awaiting_measurement case can revise its scope", ErrStateConflict)
	}
	if strings.TrimSpace(deviceID) == "" {
		return fmt.Errorf("%w: revision device id is required", ErrInvalidInput)
	}
	if !isFinite(nominal) || !isFinite(tolerance) || tolerance <= 0 {
		return fmt.Errorf("%w: nominal must be finite and tolerance must be positive", ErrInvalidInput)
	}
	c.DeviceID = strings.TrimSpace(deviceID)
	c.Nominal = nominal
	c.Tolerance = tolerance
	c.Version++
	c.UpdatedAt = at.UTC()
	return nil
}

func (c *VerificationCase) Seal(at time.Time) error {
	if c.State != CaseReviewReady {
		return fmt.Errorf("%w: case is not ready for review", ErrStateConflict)
	}
	stamp := at.UTC()
	c.State = CaseSealed
	c.UpdatedAt = stamp
	c.SealedAt = &stamp
	return nil
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
