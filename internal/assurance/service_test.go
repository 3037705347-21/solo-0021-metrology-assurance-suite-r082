package assurance

import (
	"context"
	"errors"
	"sync"
	"testing"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

func newTestService() *Service {
	return NewService(repository.New())
}

func registerDevice(t *testing.T, s *Service, tag string) string {
	t.Helper()
	device, err := s.RegisterDevice(context.Background(), contracts.RegisterDeviceRequest{
		AssetTag: tag, Model: "Bench Meter", Room: "Room A", Steward: "Lin",
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	return device.ID
}

func openCase(t *testing.T, s *Service, deviceID string) contracts.CaseResponse {
	t.Helper()
	view, err := s.OpenCase(context.Background(), contracts.OpenCaseRequest{
		DeviceID: deviceID, Nominal: 25.0, Tolerance: 0.5, Steward: "Qiao",
	})
	if err != nil {
		t.Fatalf("OpenCase: %v", err)
	}
	return view
}

func TestReviseCaseProducesTraceableNewVersion(t *testing.T) {
	s := newTestService()
	deviceID := registerDevice(t, s, "MET-2001")
	opened := openCase(t, s, deviceID)
	caseID := opened.Case.ID

	view, err := s.ReviseCase(context.Background(), caseID, contracts.ReviseCaseRequest{
		DeviceID: deviceID, Nominal: 26.0, Tolerance: 0.2,
		Actor: "Lin", Reason: "nameplate confirmed", Basis: "certificate C-1", ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("ReviseCase: %v", err)
	}
	if view.Case.Version != 2 || view.Case.Nominal != 26.0 || view.Case.Tolerance != 0.2 {
		t.Errorf("current scope = version %d nominal %v tolerance %v", view.Case.Version, view.Case.Nominal, view.Case.Tolerance)
	}
	if view.Case.State != domain.CaseAwaitingMeasurement {
		t.Errorf("state = %q", view.Case.State)
	}
	if len(view.Revisions) != 2 {
		t.Fatalf("revisions = %d, expected 2", len(view.Revisions))
	}
	if view.Revisions[0].Kind != domain.RevisionKindOpened || view.Revisions[1].Kind != domain.RevisionKindRevised {
		t.Errorf("revision kinds = %q, %q", view.Revisions[0].Kind, view.Revisions[1].Kind)
	}
	if view.Revisions[0].Nominal != 25.0 || view.Revisions[1].Nominal != 26.0 {
		t.Errorf("old scope not retained: %+v", view.Revisions)
	}
	if !hasEventKind(view.Events, "case_revised") {
		t.Errorf("case_revised audit event missing: %+v", view.Events)
	}
}

func TestReviseCaseRejectsMissingReasonOrBasis(t *testing.T) {
	s := newTestService()
	caseID := openCase(t, s, registerDevice(t, s, "MET-2002")).Case.ID

	if _, err := s.ReviseCase(context.Background(), caseID, contracts.ReviseCaseRequest{
		DeviceID: "x", Nominal: 26.0, Tolerance: 0.2, Actor: "Lin", ExpectedVersion: 1,
	}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("missing reason/basis error = %v, expected ErrInvalidInput", err)
	}
	if _, err := s.ReviseCase(context.Background(), caseID, contracts.ReviseCaseRequest{
		Nominal: 26.0, Tolerance: 0.2, Actor: "Lin", Reason: "r", Basis: "b", ExpectedVersion: 1,
	}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("missing device error = %v, expected ErrInvalidInput", err)
	}
	if _, err := s.ReviseCase(context.Background(), caseID, contracts.ReviseCaseRequest{
		DeviceID: "x", Nominal: 26.0, Tolerance: 0.2, Actor: "Lin", Reason: "r", Basis: "b",
	}); !errors.Is(err, domain.ErrInvalidInput) {
		t.Errorf("missing expected version error = %v, expected ErrInvalidInput", err)
	}
}

func TestReviseCaseRejectedAfterMeasurement(t *testing.T) {
	s := newTestService()
	deviceID := registerDevice(t, s, "MET-2003")
	caseID := openCase(t, s, deviceID).Case.ID

	if _, err := s.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
		Observed: 26.5, Technician: "Rui",
	}); err != nil {
		t.Fatalf("RecordMeasurement: %v", err)
	}
	_, err := s.ReviseCase(context.Background(), caseID, contracts.ReviseCaseRequest{
		DeviceID: deviceID, Nominal: 26.0, Tolerance: 0.2,
		Actor: "Lin", Reason: "too late", Basis: "b", ExpectedVersion: 1,
	})
	if !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("error = %v, expected ErrStateConflict", err)
	}
}

func TestReviseCaseRejectedWhenSealed(t *testing.T) {
	s := newTestService()
	deviceID := registerDevice(t, s, "MET-2004")
	caseID := openCase(t, s, deviceID).Case.ID
	if _, err := s.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
		Observed: 25.2, Technician: "Rui",
	}); err != nil {
		t.Fatalf("RecordMeasurement: %v", err)
	}
	if _, err := s.SealCase(context.Background(), caseID, contracts.SealCaseRequest{
		Reviewer: "Verifier", Summary: "done",
	}); err != nil {
		t.Fatalf("SealCase: %v", err)
	}
	_, err := s.ReviseCase(context.Background(), caseID, contracts.ReviseCaseRequest{
		DeviceID: deviceID, Nominal: 26.0, Tolerance: 0.2,
		Actor: "Lin", Reason: "late", Basis: "b", ExpectedVersion: 1,
	})
	if !errors.Is(err, domain.ErrStateConflict) {
		t.Fatalf("error = %v, expected ErrStateConflict", err)
	}
}

func TestConcurrentRevisionsHaveDefiniteOutcome(t *testing.T) {
	s := newTestService()
	deviceID := registerDevice(t, s, "MET-2005")
	caseID := openCase(t, s, deviceID).Case.ID

	var wait sync.WaitGroup
	type outcome struct {
		view contracts.CaseResponse
		err  error
	}
	results := make(chan outcome, 2)
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			view, err := s.ReviseCase(context.Background(), caseID, contracts.ReviseCaseRequest{
				DeviceID: deviceID, Nominal: 26.0 + float64(index), Tolerance: 0.2,
				Actor: "Lin", Reason: "concurrent", Basis: "form F-9", ExpectedVersion: 1,
			})
			results <- outcome{view: view, err: err}
		}(index)
	}
	wait.Wait()
	close(results)

	successes, conflicts := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
		case errors.Is(result.err, domain.ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("outcomes = %d success, %d conflict", successes, conflicts)
	}

	view, err := s.Case(context.Background(), caseID)
	if err != nil {
		t.Fatalf("Case: %v", err)
	}
	if view.Case.Version != 2 || len(view.Revisions) != 2 {
		t.Fatalf("after race: version=%d revisions=%d", view.Case.Version, len(view.Revisions))
	}
}

func TestReviseMovesCaseToAnotherDevice(t *testing.T) {
	s := newTestService()
	fromID := registerDevice(t, s, "MET-2006")
	toID := registerDevice(t, s, "MET-2007")
	caseID := openCase(t, s, fromID).Case.ID

	view, err := s.ReviseCase(context.Background(), caseID, contracts.ReviseCaseRequest{
		DeviceID: toID, Nominal: 25.0, Tolerance: 0.5,
		Actor: "Lin", Reason: "wrong asset tagged", Basis: "correction memo", ExpectedVersion: 1,
	})
	if err != nil {
		t.Fatalf("ReviseCase: %v", err)
	}
	if view.Case.DeviceID != toID {
		t.Fatalf("device = %q, expected %q", view.Case.DeviceID, toID)
	}
	// The original device is free for a new unfinished case.
	if _, err := s.OpenCase(context.Background(), contracts.OpenCaseRequest{
		DeviceID: fromID, Nominal: 1.0, Tolerance: 0.1, Steward: "Qiao",
	}); err != nil {
		t.Fatalf("original device should accept a new case: %v", err)
	}
	// The target device still rejects a second unfinished case.
	_, err = s.OpenCase(context.Background(), contracts.OpenCaseRequest{
		DeviceID: toID, Nominal: 1.0, Tolerance: 0.1, Steward: "Qiao",
	})
	if !errors.Is(err, domain.ErrActiveCaseExists) {
		t.Fatalf("error = %v, expected ErrActiveCaseExists", err)
	}
}

func TestReviseOntoBusyDeviceIsConflict(t *testing.T) {
	s := newTestService()
	caseID := openCase(t, s, registerDevice(t, s, "MET-2008")).Case.ID
	busyID := registerDevice(t, s, "MET-2009")
	openCase(t, s, busyID)

	_, err := s.ReviseCase(context.Background(), caseID, contracts.ReviseCaseRequest{
		DeviceID: busyID, Nominal: 25.0, Tolerance: 0.5,
		Actor: "Lin", Reason: "move", Basis: "b", ExpectedVersion: 1,
	})
	if !errors.Is(err, domain.ErrActiveCaseExists) {
		t.Fatalf("error = %v, expected ErrActiveCaseExists", err)
	}
}

func TestSealUsesCurrentVersionMeasurement(t *testing.T) {
	s := newTestService()
	deviceID := registerDevice(t, s, "MET-2010")
	caseID := openCase(t, s, deviceID).Case.ID

	// Revise before any measurement: the new range rejects 25.2, which the
	// old range (25 +/- 0.5) would have accepted.
	if _, err := s.ReviseCase(context.Background(), caseID, contracts.ReviseCaseRequest{
		DeviceID: deviceID, Nominal: 26.0, Tolerance: 0.2,
		Actor: "Lin", Reason: "field confirmed", Basis: "certificate", ExpectedVersion: 1,
	}); err != nil {
		t.Fatalf("ReviseCase: %v", err)
	}
	rejected, err := s.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
		Observed: 25.2, Technician: "Rui",
	})
	if err != nil {
		t.Fatalf("RecordMeasurement: %v", err)
	}
	if rejected.Case.State != domain.CaseRecheckRequired || rejected.Measurements[0].CaseVersion != 2 {
		t.Fatalf("state=%q measurement version=%d", rejected.Case.State, rejected.Measurements[0].CaseVersion)
	}
	if _, err := s.ReopenCase(context.Background(), caseID, contracts.ReopenCaseRequest{
		Actor: "Lin", Reason: "setup fixed",
	}); err != nil {
		t.Fatalf("ReopenCase: %v", err)
	}
	accepted, err := s.RecordMeasurement(context.Background(), caseID, contracts.RecordMeasurementRequest{
		Observed: 26.1, Technician: "Rui",
	})
	if err != nil {
		t.Fatalf("RecordMeasurement: %v", err)
	}
	if accepted.Case.State != domain.CaseReviewReady {
		t.Fatalf("state = %q", accepted.Case.State)
	}

	sealed, err := s.SealCase(context.Background(), caseID, contracts.SealCaseRequest{
		Reviewer: "Verifier", Summary: "sealed on current version",
	})
	if err != nil {
		t.Fatalf("SealCase: %v", err)
	}
	if sealed.Seal == nil || sealed.Seal.MeasurementID != accepted.Measurements[1].ID {
		t.Fatalf("seal selected %v, expected current-version measurement %q", sealed.Seal, accepted.Measurements[1].ID)
	}
}

func hasEventKind(events []domain.AuditEvent, kind string) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}
