package domain

import (
	"fmt"
	"strings"
	"time"
)

type EvidenceSeal struct {
	ID            string    `json:"id"`
	CaseID        string    `json:"case_id"`
	CaseVersion   int       `json:"case_version"`
	DeviceID      string    `json:"device_id"`
	MeasurementID string    `json:"measurement_id"`
	Reviewer      string    `json:"reviewer"`
	Summary       string    `json:"summary"`
	SealedAt      time.Time `json:"sealed_at"`
}

func NewEvidenceSeal(id, caseID string, caseVersion int, deviceID, measurementID, reviewer, summary string, at time.Time) (EvidenceSeal, error) {
	item := EvidenceSeal{
		ID:            strings.TrimSpace(id),
		CaseID:        strings.TrimSpace(caseID),
		CaseVersion:   caseVersion,
		DeviceID:      strings.TrimSpace(deviceID),
		MeasurementID: strings.TrimSpace(measurementID),
		Reviewer:      strings.TrimSpace(reviewer),
		Summary:       strings.TrimSpace(summary),
		SealedAt:      at.UTC(),
	}
	if err := item.Validate(); err != nil {
		return EvidenceSeal{}, err
	}
	return item, nil
}

func (s EvidenceSeal) Validate() error {
	if s.ID == "" || s.CaseID == "" || s.DeviceID == "" || s.MeasurementID == "" || s.Reviewer == "" || s.Summary == "" {
		return fmt.Errorf("%w: seal id, case, device, measurement, reviewer, and summary are required", ErrInvalidInput)
	}
	if s.CaseVersion < 1 {
		return fmt.Errorf("%w: seal case version is required", ErrInvalidInput)
	}
	if s.SealedAt.IsZero() {
		return fmt.Errorf("%w: seal time is required", ErrInvalidInput)
	}
	return nil
}
