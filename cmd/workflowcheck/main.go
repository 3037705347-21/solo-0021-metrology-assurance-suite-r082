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
		"seal-verification":        checkSealVerification,
		"revise-case-scope":        checkReviseCaseScope,
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

func checkReviseCaseScope() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1501")
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
	initialVersion, err := nestedInt(opened.Body, "case", "version")
	if err != nil {
		return err
	}
	if initialVersion != 1 {
		return fmt.Errorf("opened case version = %d, expected 1", initialVersion)
	}
	revisions, err := nestedSlice(opened.Body, "revisions")
	if err != nil {
		return err
	}
	if len(revisions) != 1 {
		return fmt.Errorf("opened case revisions = %d, expected 1", len(revisions))
	}

	revised, err := h.request(http.MethodPost, "/cases/"+caseID+"/revisions", map[string]any{
		"expected_version": 1,
		"nominal":          26.0,
		"tolerance":        0.25,
		"actor":            "Qiao",
		"reason":           "on-site confirmation found the nameplate nominal differs from the intake form",
		"basis":            "nameplate photograph NP-77 and work order WO-42",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(revised, http.StatusCreated); err != nil {
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
	if tolerance != 0.25 {
		return fmt.Errorf("revised tolerance = %v, expected 0.25", tolerance)
	}
	state, err := nestedString(revised.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("revised case state = %q, expected awaiting_measurement", state)
	}

	// Old version remains traceable alongside the new effective version.
	revisions, err = nestedSlice(revised.Body, "revisions")
	if err != nil {
		return err
	}
	if len(revisions) != 2 {
		return fmt.Errorf("revisions after revision = %d, expected 2", len(revisions))
	}
	first := revisions[0].(map[string]any)
	second := revisions[1].(map[string]any)
	if int(first["version"].(float64)) != 1 || first["nominal"].(float64) != 25.0 {
		return fmt.Errorf("old revision was not preserved: %v", first)
	}
	if int(second["version"].(float64)) != 2 || second["nominal"].(float64) != 26.0 {
		return fmt.Errorf("new revision was not recorded: %v", second)
	}
	if second["reason"] == "" || second["basis"] == "" {
		return errors.New("revision reason and basis were not stored")
	}
	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_scope_revised") {
		return errors.New("scope revision audit event is missing")
	}

	// A stale expected version must conflict rather than overwrite the winner.
	stale, err := h.request(http.MethodPost, "/cases/"+caseID+"/revisions", map[string]any{
		"expected_version": 1,
		"nominal":          99.0,
		"tolerance":        1.0,
		"actor":            "Qiao",
		"reason":           "duplicate concurrent request",
		"basis":            "stale client form",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(stale, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(stale, "state_conflict"); err != nil {
		return err
	}

	// Revalidating the current version succeeds and keeps the history chain.
	secondRevision, err := h.request(http.MethodPost, "/cases/"+caseID+"/revisions", map[string]any{
		"expected_version": 2,
		"nominal":          26.1,
		"tolerance":        0.3,
		"actor":            "Qiao",
		"reason":           "calibration certificate corrected the scale factor",
		"basis":            "calibration cert CC-9",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(secondRevision, http.StatusCreated); err != nil {
		return err
	}
	version3, err := nestedInt(secondRevision.Body, "case", "version")
	if err != nil {
		return err
	}
	if version3 != 3 {
		return fmt.Errorf("second revision version = %d, expected 3", version3)
	}
	revisions, err = nestedSlice(secondRevision.Body, "revisions")
	if err != nil {
		return err
	}
	if len(revisions) != 3 {
		return fmt.Errorf("revisions after second revision = %d, expected 3", len(revisions))
	}

	// Concurrent revisions on the same base version: exactly one succeeds.
	concurrentDeviceID, err := h.createDevice("MET-1502")
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
			response, err := h.request(http.MethodPost, "/cases/"+concurrentCaseID+"/revisions", map[string]any{
				"expected_version": 1,
				"nominal":          30.0 + float64(index),
				"tolerance":        0.4,
				"actor":            "Qiao",
				"reason":           fmt.Sprintf("field confirmation attempt %d", index+1),
				"basis":            "on-site inspection",
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
			return fmt.Errorf("unexpected concurrent revision status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if successes != 1 || conflicts != 1 {
		return fmt.Errorf("concurrent revision outcomes: %d success, %d conflict", successes, conflicts)
	}
	readAfterRace, err := h.request(http.MethodGet, "/cases/"+concurrentCaseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(readAfterRace, http.StatusOK); err != nil {
		return err
	}
	racedVersion, err := nestedInt(readAfterRace.Body, "case", "version")
	if err != nil {
		return err
	}
	if racedVersion != 2 {
		return fmt.Errorf("case version after concurrent revision = %d, expected 2", racedVersion)
	}

	// Once a measurement is recorded, a revision must be refused deterministically.
	measured, err := h.recordMeasurement(caseID, 26.12, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(measured, http.StatusCreated); err != nil {
		return err
	}
	measuredNominal, err := nestedFloat(measured.Body, "measurements", 0, "nominal")
	if err != nil {
		return err
	}
	if measuredNominal != 26.1 {
		return fmt.Errorf("measurement stamped nominal = %v, expected current scope 26.1", measuredNominal)
	}
	measuredVersion, err := nestedInt(measured.Body, "measurements", 0, "case_version")
	if err != nil {
		return err
	}
	if measuredVersion != 3 {
		return fmt.Errorf("measurement stamped version = %d, expected 3", measuredVersion)
	}
	afterMeasurement, err := h.request(http.MethodPost, "/cases/"+caseID+"/revisions", map[string]any{
		"expected_version": 3,
		"nominal":          40.0,
		"tolerance":        1.0,
		"actor":            "Qiao",
		"reason":           "late correction",
		"basis":            "new paperwork",
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

	// Sealing uses only an accepted measurement on the current scope version,
	// and sealed cases reject every later revision.
	sealed, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Verifier Lin",
		"summary":  "sealed on the current revised scope",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}
	sealVersion, err := nestedInt(sealed.Body, "seal", "case_version")
	if err != nil {
		return err
	}
	if sealVersion != 3 {
		return fmt.Errorf("seal version = %d, expected 3", sealVersion)
	}
	afterSeal, err := h.request(http.MethodPost, "/cases/"+caseID+"/revisions", map[string]any{
		"expected_version": 3,
		"nominal":          40.0,
		"tolerance":        1.0,
		"actor":            "Qiao",
		"reason":           "attempt after seal",
		"basis":            "ignored paperwork",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(afterSeal, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(afterSeal, "state_conflict"); err != nil {
		return err
	}

	// Deterministic input and ownership failures on a fresh untouched case.
	freshDeviceID, err := h.createDevice("MET-1503")
	if err != nil {
		return err
	}
	freshOpened, err := h.openCase(freshDeviceID)
	if err != nil {
		return err
	}
	freshCaseID, err := nestedString(freshOpened.Body, "case", "id")
	if err != nil {
		return err
	}
	missingBasis, err := h.request(http.MethodPost, "/cases/"+freshCaseID+"/revisions", map[string]any{
		"expected_version": 1,
		"nominal":          26.0,
		"tolerance":        0.25,
		"actor":            "Qiao",
		"reason":           "no basis provided",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(missingBasis, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(missingBasis, "invalid_input"); err != nil {
		return err
	}
	otherActor, err := h.request(http.MethodPost, "/cases/"+freshCaseID+"/revisions", map[string]any{
		"expected_version": 1,
		"nominal":          26.0,
		"tolerance":        0.25,
		"actor":            "Someone Else",
		"reason":           "not the owner",
		"basis":            "none",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(otherActor, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(otherActor, "state_conflict"); err != nil {
		return err
	}
	unknownDevice, err := h.request(http.MethodPost, "/cases/"+freshCaseID+"/revisions", map[string]any{
		"expected_version": 1,
		"device_id":        "device-9999",
		"nominal":          26.0,
		"tolerance":        0.25,
		"actor":            "Qiao",
		"reason":           "move to a missing device",
		"basis":            "none",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(unknownDevice, http.StatusNotFound); err != nil {
		return err
	}
	if err := expectErrorCode(unknownDevice, "not_found"); err != nil {
		return err
	}

	// Rebinding onto another device with its own unfinished case is a
	// deterministic conflict; a free active device is accepted and stays the
	// current binding for subsequent measurement and seal.
	busyDeviceID, err := h.createDevice("MET-1504")
	if err != nil {
		return err
	}
	if _, err := h.openCase(busyDeviceID); err != nil {
		return err
	}
	busyTarget, err := h.request(http.MethodPost, "/cases/"+freshCaseID+"/revisions", map[string]any{
		"expected_version": 1,
		"device_id":        busyDeviceID,
		"nominal":          26.0,
		"tolerance":        0.25,
		"actor":            "Qiao",
		"reason":           "reassign to busy device",
		"basis":            "shop floor request",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(busyTarget, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(busyTarget, "active_case_exists"); err != nil {
		return err
	}
	freeDeviceID, err := h.createDevice("MET-1505")
	if err != nil {
		return err
	}
	freeTarget, err := h.request(http.MethodPost, "/cases/"+freshCaseID+"/revisions", map[string]any{
		"expected_version": 1,
		"device_id":        freeDeviceID,
		"nominal":          26.0,
		"tolerance":        0.25,
		"actor":            "Qiao",
		"reason":           "reassign after confirming the correct asset",
		"basis":            "asset tag verification",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(freeTarget, http.StatusCreated); err != nil {
		return err
	}
	boundDevice, err := nestedString(freeTarget.Body, "case", "device_id")
	if err != nil {
		return err
	}
	if boundDevice != freeDeviceID {
		return fmt.Errorf("case device = %q, expected %q", boundDevice, freeDeviceID)
	}
	reboundMeasurement, err := h.recordMeasurement(freshCaseID, 26.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(reboundMeasurement, http.StatusCreated); err != nil {
		return err
	}
	reboundSeal, err := h.request(http.MethodPost, "/cases/"+freshCaseID+"/seal", map[string]any{
		"reviewer": "Verifier Lin",
		"summary":  "sealed against the rebound device",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reboundSeal, http.StatusCreated); err != nil {
		return err
	}
	sealedDevice, err := nestedString(reboundSeal.Body, "seal", "device_id")
	if err != nil {
		return err
	}
	if sealedDevice != freeDeviceID {
		return fmt.Errorf("seal device = %q, expected %q", sealedDevice, freeDeviceID)
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

func nestedFloat(root map[string]any, path ...any) (float64, error) {
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
	number, err := nestedFloat(root, path...)
	if err != nil {
		return 0, err
	}
	return int(number), nil
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
