package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/bootstrap"
)

type apiResponse struct {
	Status int
	Body   map[string]any
	Raw    string
}

type harness struct {
	baseURL string
	client  *http.Client
	server  *httptest.Server
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to verify")
	flag.Parse()

	checks := map[string]func() error{
		"register-device":          checkRegisterDevice,
		"open-verification-case":   checkOpenVerificationCase,
		"record-measurement":       checkRecordMeasurement,
		"reopen-verification-case": checkReopenVerificationCase,
		"revise-case-scope":        checkReviseCaseScope,
		"seal-verification":        checkSealVerification,
	}
	run, ok := checks[*workflow]
	if !ok {
		fmt.Fprintf(flag.CommandLine.Output(), "unknown workflow %q\n", *workflow)
		flag.Usage()
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Printf("workflow %s: failed: %v\n", *workflow, err)
		os.Exit(1)
	}
	fmt.Printf("workflow %s: ok\n", *workflow)
}

func checkRegisterDevice() error {
	h := newHarness()
	defer h.close()

	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": " met-1001 ",
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return err
	}
	id, err := nestedString(response.Body, "device", "id")
	if err != nil {
		return err
	}
	tag, err := nestedString(response.Body, "device", "asset_tag")
	if err != nil {
		return err
	}
	if tag != "MET-1001" {
		return fmt.Errorf("asset tag was not normalized: %q", tag)
	}

	read, err := h.request(http.MethodGet, "/devices/"+id, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	readID, err := nestedString(read.Body, "device", "id")
	if err != nil {
		return err
	}
	if readID != id {
		return fmt.Errorf("read returned device %q, expected %q", readID, id)
	}

	duplicate, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": "MET-1001",
		"model":     "Other Meter",
		"room":      "Room B",
		"steward":   "Mira",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(duplicate, "duplicate_asset_tag")
}

func checkOpenVerificationCase() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1101")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	state, err := nestedString(opened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("opened case state = %q", state)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}

	second, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(second, "active_case_exists")
}

func checkRecordMeasurement() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1201")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	accepted, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(accepted, http.StatusCreated); err != nil {
		return err
	}
	state, err := nestedString(accepted.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "review_ready" {
		return fmt.Errorf("accepted measurement state = %q", state)
	}
	acceptedValue, err := nestedBool(accepted.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if !acceptedValue {
		return errors.New("in-tolerance measurement was not accepted")
	}

	second, err := h.recordMeasurement(caseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	rejectDeviceID, err := h.createDevice("MET-1202")
	if err != nil {
		return err
	}
	rejectCase, err := h.openCase(rejectDeviceID)
	if err != nil {
		return err
	}
	rejectCaseID, err := nestedString(rejectCase.Body, "case", "id")
	if err != nil {
		return err
	}
	rejected, err := h.recordMeasurement(rejectCaseID, 26.5, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(rejected, http.StatusCreated); err != nil {
		return err
	}
	rejectedState, err := nestedString(rejected.Body, "case", "state")
	if err != nil {
		return err
	}
	if rejectedState != "recheck_required" {
		return fmt.Errorf("out-of-tolerance state = %q", rejectedState)
	}
	rejectedValue, err := nestedBool(rejected.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if rejectedValue {
		return errors.New("out-of-tolerance measurement was accepted")
	}
	return nil
}

func checkReopenVerificationCase() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.rejectedCase("MET-1301")
	if err != nil {
		return err
	}
	reopened, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "measurement setup was replaced",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reopened, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(reopened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("reopened case state = %q", state)
	}
	measurements, err := nestedSlice(reopened.Body, "measurements")
	if err != nil {
		return err
	}
	if len(measurements) != 1 {
		return fmt.Errorf("reopen changed measurement history: %d entries", len(measurements))
	}

	second, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "duplicate request",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_reopened") {
		return errors.New("reopen audit event is missing")
	}
	return nil
}

func checkReviseCaseScope() error {
	h := newHarness()
	defer h.close()

	// Fresh case: the steward corrects nominal value and tolerance before any
	// measurement has been recorded.
	deviceID, err := h.createDevice("MET-1351")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	version, err := nestedInt(opened.Body, "case", "version")
	if err != nil {
		return err
	}
	if version != 1 {
		return fmt.Errorf("opened case version = %d, expected 1", version)
	}

	revised, err := h.reviseScope(caseID, map[string]any{
		"device_id":        deviceID,
		"nominal":          26.0,
		"tolerance":        0.2,
		"actor":            "Lin",
		"reason":           "nameplate nominal confirmed at the bench",
		"basis":            "calibration certificate CAL-2026-031",
		"expected_version": 1,
	})
	if err != nil {
		return err
	}
	if err := expectStatus(revised, http.StatusOK); err != nil {
		return err
	}
	newVersion, err := nestedInt(revised.Body, "case", "version")
	if err != nil {
		return err
	}
	if newVersion != 2 {
		return fmt.Errorf("revised case version = %d, expected 2", newVersion)
	}
	nominal, err := nestedFloat(revised.Body, "case", "nominal")
	if err != nil {
		return err
	}
	if nominal != 26.0 {
		return fmt.Errorf("revised nominal = %v, expected 26", nominal)
	}
	tolerance, err := nestedFloat(revised.Body, "case", "tolerance")
	if err != nil {
		return err
	}
	if tolerance != 0.2 {
		return fmt.Errorf("revised tolerance = %v, expected 0.2", tolerance)
	}
	state, err := nestedString(revised.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("revised case state = %q", state)
	}

	// Both versions remain traceable in the same case history.
	revisions, err := nestedSlice(revised.Body, "revisions")
	if err != nil {
		return err
	}
	if len(revisions) != 2 {
		return fmt.Errorf("revision history = %d entries, expected 2", len(revisions))
	}
	first := revisions[0].(map[string]any)
	if first["kind"] != "opened" || int(first["version"].(float64)) != 1 {
		return fmt.Errorf("first revision entry is not the v1 opening record: %v", first)
	}
	if first["nominal"].(float64) != 25.0 || first["tolerance"].(float64) != 0.5 {
		return fmt.Errorf("v1 scope was not preserved: %v", first)
	}
	second := revisions[1].(map[string]any)
	if second["kind"] != "revised" || int(second["version"].(float64)) != 2 {
		return fmt.Errorf("second revision entry is not the v2 revision record: %v", second)
	}
	if second["reason"] != "nameplate nominal confirmed at the bench" ||
		second["basis"] != "calibration certificate CAL-2026-031" {
		return fmt.Errorf("v2 reason and basis were not recorded: %v", second)
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_revised") {
		return errors.New("revision audit event is missing")
	}

	// Revising against a stale version (the case is already at v2) must be a
	// conflict with a definite version result.
	stale, err := h.reviseScope(caseID, map[string]any{
		"device_id":        deviceID,
		"nominal":          27.0,
		"tolerance":        0.3,
		"actor":            "Lin",
		"reason":           "late correction",
		"basis":            "stale read of the case",
		"expected_version": 1,
	})
	if err != nil {
		return err
	}
	if err := expectStatus(stale, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(stale, "version_conflict"); err != nil {
		return err
	}

	// Measurements after revision use the current version and scope: 25.2 was
	// inside the old range but is out of tolerance against nominal 26 +/- 0.2.
	recorded, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(recorded, http.StatusCreated); err != nil {
		return err
	}
	measurementVersion, err := nestedInt(recorded.Body, "measurements", 0, "case_version")
	if err != nil {
		return err
	}
	if measurementVersion != 2 {
		return fmt.Errorf("measurement bound to version %d, expected 2", measurementVersion)
	}
	measurementNominal, err := nestedFloat(recorded.Body, "measurements", 0, "nominal")
	if err != nil {
		return err
	}
	if measurementNominal != 26.0 {
		return fmt.Errorf("measurement nominal = %v, expected 26", measurementNominal)
	}
	recordedState, err := nestedString(recorded.Body, "case", "state")
	if err != nil {
		return err
	}
	if recordedState != "recheck_required" {
		return fmt.Errorf("post-revision measurement state = %q, expected recheck_required", recordedState)
	}

	// A case that already has measurements cannot be revised, even when the
	// caller presents the current version.
	afterMeasurement, err := h.reviseScope(caseID, map[string]any{
		"device_id":        deviceID,
		"nominal":          27.0,
		"tolerance":        0.3,
		"actor":            "Lin",
		"reason":           "attempt after measurement",
		"basis":            "should be rejected",
		"expected_version": 2,
	})
	if err != nil {
		return err
	}
	if err := expectStatus(afterMeasurement, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(afterMeasurement, "state_conflict"); err != nil {
		return err
	}

	// Concurrent revisions against the same version produce exactly one
	// success and one version conflict.
	concurrentDeviceID, err := h.createDevice("MET-1352")
	if err != nil {
		return err
	}
	concurrentOpened, err := h.openCase(concurrentDeviceID)
	if err != nil {
		return err
	}
	concurrentCaseID, err := nestedString(concurrentOpened.Body, "case", "id")
	if err != nil {
		return err
	}
	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response, err := h.reviseScope(concurrentCaseID, map[string]any{
				"device_id":        concurrentDeviceID,
				"nominal":          26.0 + float64(index),
				"tolerance":        0.2,
				"actor":            "Lin",
				"reason":           fmt.Sprintf("concurrent correction %d", index+1),
				"basis":            "field confirmation form F-09",
				"expected_version": 1,
			})
			results <- outcome{response: response, err: err}
		}(index)
	}
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusOK:
			successes++
		case http.StatusConflict:
			conflicts++
			if err := expectErrorCode(item.response, "version_conflict"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unexpected concurrent revision status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if successes != 1 || conflicts != 1 {
		return fmt.Errorf("concurrent revision outcomes: %d success, %d conflict", successes, conflicts)
	}

	// Revising a sealed case is rejected.
	sealedCaseID, err := h.readyCase("MET-1353")
	if err != nil {
		return err
	}
	sealed, err := h.request(http.MethodPost, "/cases/"+sealedCaseID+"/seal", map[string]any{
		"reviewer": "Verifier",
		"summary":  "accepted measurement sealed",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}
	sealedTargetID, err := h.createDevice("MET-1354")
	if err != nil {
		return err
	}
	reviseSealed, err := h.reviseScope(sealedCaseID, map[string]any{
		"device_id":        sealedTargetID,
		"nominal":          30.0,
		"tolerance":        1.0,
		"actor":            "Lin",
		"reason":           "attempt after seal",
		"basis":            "should be rejected",
		"expected_version": 2,
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reviseSealed, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(reviseSealed, "state_conflict"); err != nil {
		return err
	}

	// Moving the case to another active device keeps the same case id and
	// history, and frees the original device for a new case.
	movedDeviceID, err := h.createDevice("MET-1355")
	if err != nil {
		return err
	}
	movedOpened, err := h.openCase(movedDeviceID)
	if err != nil {
		return err
	}
	movedCaseID, err := nestedString(movedOpened.Body, "case", "id")
	if err != nil {
		return err
	}
	targetDeviceID, err := h.createDevice("MET-1356")
	if err != nil {
		return err
	}
	moved, err := h.reviseScope(movedCaseID, map[string]any{
		"device_id":        targetDeviceID,
		"nominal":          25.0,
		"tolerance":        0.5,
		"actor":            "Lin",
		"reason":           "case reassigned to the confirmed asset",
		"basis":            "asset tag correction memo",
		"expected_version": 1,
	})
	if err != nil {
		return err
	}
	if err := expectStatus(moved, http.StatusOK); err != nil {
		return err
	}
	movedTo, err := nestedString(moved.Body, "case", "device_id")
	if err != nil {
		return err
	}
	if movedTo != targetDeviceID {
		return fmt.Errorf("case device = %q, expected %q", movedTo, targetDeviceID)
	}
	reopenedOnOriginal, err := h.openCase(movedDeviceID)
	if err != nil {
		return err
	}
	if err := expectStatus(reopenedOnOriginal, http.StatusCreated); err != nil {
		return err
	}

	// A target device that already has an unfinished case is a definite
	// conflict.
	busyDeviceID, err := h.createDevice("MET-1357")
	if err != nil {
		return err
	}
	busyOpened, err := h.openCase(busyDeviceID)
	if err != nil {
		return err
	}
	busyCaseID, err := nestedString(busyOpened.Body, "case", "id")
	if err != nil {
		return err
	}
	busyTargetID, err := h.createDevice("MET-1358")
	if err != nil {
		return err
	}
	if _, err := h.openCase(busyTargetID); err != nil {
		return err
	}
	busy, err := h.reviseScope(busyCaseID, map[string]any{
		"device_id":        busyTargetID,
		"nominal":          25.0,
		"tolerance":        0.5,
		"actor":            "Lin",
		"reason":           "move onto a busy device",
		"basis":            "should be rejected",
		"expected_version": 1,
	})
	if err != nil {
		return err
	}
	if err := expectStatus(busy, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(busy, "active_case_exists"); err != nil {
		return err
	}

	// Missing reason, basis, or expected version is a 400.
	missingFields, err := h.reviseScope(busyCaseID, map[string]any{
		"device_id":        busyDeviceID,
		"nominal":          25.0,
		"tolerance":        0.5,
		"actor":            "Lin",
		"expected_version": 1,
	})
	if err != nil {
		return err
	}
	return expectStatus(missingFields, http.StatusBadRequest)
}

func checkSealVerification() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.readyCase("MET-1401")
	if err != nil {
		return err
	}
	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
				"reviewer": fmt.Sprintf("Reviewer-%d", index+1),
				"summary":  "accepted measurement sealed for review",
			})
			results <- outcome{response: response, err: err}
		}(index)
	}
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			return fmt.Errorf("unexpected concurrent seal status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if successes != 1 || conflicts != 1 {
		return fmt.Errorf("concurrent seal outcomes: %d success, %d conflict", successes, conflicts)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(read.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "sealed" {
		return fmt.Errorf("sealed case state = %q", state)
	}
	sealID, err := nestedString(read.Body, "seal", "id")
	if err != nil {
		return err
	}
	if sealID == "" {
		return errors.New("evidence seal is missing")
	}
	return nil
}

func newHarness() *harness {
	server := httptest.NewServer(bootstrap.NewHandler())
	return &harness{
		baseURL: server.URL,
		client:  server.Client(),
		server:  server,
	}
}

func (h *harness) close() {
	h.server.Close()
}

func (h *harness) request(method, path string, body any) (apiResponse, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return apiResponse{}, err
		}
		reader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, reader)
	if err != nil {
		return apiResponse{}, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		return apiResponse{}, err
	}
	defer response.Body.Close()
	rawBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return apiResponse{}, err
	}
	result := apiResponse{Status: response.StatusCode, Raw: string(rawBytes)}
	if len(bytes.TrimSpace(rawBytes)) > 0 {
		if err := json.Unmarshal(rawBytes, &result.Body); err != nil {
			return apiResponse{}, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return result, nil
}

func (h *harness) createDevice(tag string) (string, error) {
	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": tag,
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return nestedString(response.Body, "device", "id")
}

func (h *harness) openCase(deviceID string) (apiResponse, error) {
	response, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return apiResponse{}, err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return apiResponse{}, err
	}
	return response, nil
}

func (h *harness) recordMeasurement(caseID string, observed float64, technician string) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/measurements", map[string]any{
		"observed":   observed,
		"technician": technician,
	})
}

func (h *harness) reviseScope(caseID string, body map[string]any) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/revisions", body)
}

func (h *harness) rejectedCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 26.5, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) readyCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func expectStatus(response apiResponse, expected int) error {
	if response.Status != expected {
		return fmt.Errorf("status = %d, expected %d: %s", response.Status, expected, response.Raw)
	}
	return nil
}

func expectErrorCode(response apiResponse, expected string) error {
	code, err := nestedString(response.Body, "error", "code")
	if err != nil {
		return err
	}
	if code != expected {
		return fmt.Errorf("error code = %q, expected %q", code, expected)
	}
	return nil
}

func nestedString(root map[string]any, path ...string) (string, error) {
	value, err := nestedValue(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s is not a string", strings.Join(path, "."))
	}
	return text, nil
}

func nestedBool(root map[string]any, path ...any) (bool, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return false, err
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%v is not a boolean", path)
	}
	return flag, nil
}

func nestedNumber(root map[string]any, path ...any) (float64, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return 0, err
	}
	number, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("%v is not a number", path)
	}
	return number, nil
}

func nestedInt(root map[string]any, path ...any) (int, error) {
	number, err := nestedNumber(root, path...)
	if err != nil {
		return 0, err
	}
	return int(number), nil
}

func nestedFloat(root map[string]any, path ...any) (float64, error) {
	return nestedNumber(root, path...)
}

func nestedSlice(root map[string]any, key string) ([]any, error) {
	value, ok := root[key]
	if !ok {
		return nil, fmt.Errorf("missing %s", key)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", key)
	}
	return items, nil
}

func hasEvent(root map[string]any, kind string) bool {
	items, err := nestedSlice(root, "events")
	if err != nil {
		return false
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if event["kind"] == kind {
			return true
		}
	}
	return false
}

func nestedValue(root map[string]any, path ...string) (any, error) {
	parts := make([]any, 0, len(path))
	for _, part := range path {
		parts = append(parts, part)
	}
	return nestedAny(root, parts...)
}

func nestedAny(root map[string]any, path ...any) (any, error) {
	var current any = root
	for _, part := range path {
		switch typed := part.(type) {
		case string:
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not an object", typed)
			}
			value, ok := object[typed]
			if !ok {
				return nil, fmt.Errorf("missing %s", typed)
			}
			current = value
		case int:
			items, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("index %d is not inside an array", typed)
			}
			if typed < 0 || typed >= len(items) {
				return nil, fmt.Errorf("index %d is out of range", typed)
			}
			current = items[typed]
		default:
			return nil, fmt.Errorf("unsupported path element %T", part)
		}
	}
	return current, nil
}
