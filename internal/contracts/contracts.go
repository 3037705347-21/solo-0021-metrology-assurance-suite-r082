package contracts

import "example.com/solo-0021-metrology-assurance-suite/internal/domain"

type RegisterDeviceRequest struct {
	AssetTag string `json:"asset_tag"`
	Model    string `json:"model"`
	Room     string `json:"room"`
	Steward  string `json:"steward"`
}

type OpenCaseRequest struct {
	DeviceID  string  `json:"device_id"`
	Nominal   float64 `json:"nominal"`
	Tolerance float64 `json:"tolerance"`
	Steward   string  `json:"steward"`
}

type RecordMeasurementRequest struct {
	Observed   float64 `json:"observed"`
	Technician string  `json:"technician"`
}

type ReopenCaseRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

// ReviseCaseScopeRequest opens a new effective scope version for the same
// case. ExpectedVersion is the version the steward reviewed; a stale value is
// a deterministic conflict so concurrent revisions cannot silently overwrite
// each other. DeviceID is optional: when blank the case stays on its device;
// when present the case is rebound to that device and keeps the device's
// at-most-one-unfinished-case rule.
type ReviseCaseScopeRequest struct {
	ExpectedVersion int     `json:"expected_version"`
	DeviceID        string  `json:"device_id,omitempty"`
	Nominal         float64 `json:"nominal"`
	Tolerance       float64 `json:"tolerance"`
	Actor           string  `json:"actor"`
	Reason          string  `json:"reason"`
	Basis           string  `json:"basis"`
}

type SealCaseRequest struct {
	Reviewer string `json:"reviewer"`
	Summary  string `json:"summary"`
}

type DeviceResponse struct {
	Device domain.Device `json:"device"`
}

type CaseResponse struct {
	Case         domain.VerificationCase `json:"case"`
	Revisions    []domain.ScopeRevision  `json:"revisions"`
	Measurements []domain.Measurement    `json:"measurements"`
	Seal         *domain.EvidenceSeal    `json:"seal,omitempty"`
	Events       []domain.AuditEvent     `json:"events"`
}

type EventsResponse struct {
	Events []domain.AuditEvent `json:"events"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
