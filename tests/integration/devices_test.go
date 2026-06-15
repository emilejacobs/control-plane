package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"
)

// enrollForTest is a thin helper around POST /enrollments used by tests that
// need an already-enrolled device. It panics-via-t.Fatal on any failure so
// that the assertion site stays focused on the behavior under test.
func enrollForTest(t *testing.T, srv *testServer, hostname, hwUUID string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"bootstrap_key": testBootstrapKey,
		"hostname":      hostname,
		"hardware_uuid": hwUUID,
		"hardware_kind": "mac",
		"os_version":    "macOS 15.0",
		"agent_version": "0.1.0",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/enrollments", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", hwUUID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("enroll: status %d; body=%s", resp.StatusCode, raw)
	}
	var out struct {
		DeviceID string `json:"device_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out.DeviceID
}

func TestGetDeviceByIDReturnsInsertedRow(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-acme-02", "22222222-2222-3333-4444-555555555555")

	resp := doDeviceGet(t, srv.URL, deviceID, mintAccessToken(t, ctx, srv))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, raw)
	}

	var out struct {
		DeviceID     string `json:"device_id"`
		Hostname     string `json:"hostname"`
		HardwareUUID string `json:"hardware_uuid"`
		HardwareKind string `json:"hardware_kind"`
		OSVersion    string `json:"os_version"`
		AgentVersion string `json:"agent_version"`
		IoTThingARN  string `json:"iot_thing_arn"`
		EnrolledAt   string `json:"enrolled_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if out.DeviceID != deviceID {
		t.Errorf("device_id: got %q want %q", out.DeviceID, deviceID)
	}
	if out.Hostname != "mac-mini-acme-02" {
		t.Errorf("hostname: got %q want %q", out.Hostname, "mac-mini-acme-02")
	}
	if out.HardwareUUID != "22222222-2222-3333-4444-555555555555" {
		t.Errorf("hardware_uuid: got %q", out.HardwareUUID)
	}
	if out.HardwareKind != "mac" {
		t.Errorf("hardware_kind: got %q want %q", out.HardwareKind, "mac")
	}
	if out.OSVersion != "macOS 15.0" {
		t.Errorf("os_version: got %q", out.OSVersion)
	}
	if out.AgentVersion != "0.1.0" {
		t.Errorf("agent_version: got %q", out.AgentVersion)
	}
	if out.IoTThingARN == "" {
		t.Errorf("iot_thing_arn is empty")
	}
	// enrolled_at should be an RFC3339 timestamp from the last few seconds.
	enrolledAt, err := time.Parse(time.RFC3339, out.EnrolledAt)
	if err != nil {
		t.Errorf("enrolled_at not RFC3339: %v", err)
	} else if time.Since(enrolledAt) > 30*time.Second {
		t.Errorf("enrolled_at too old: %v", enrolledAt)
	}
}

func TestGetDeviceByIDSurfacesCertExpiry(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()

	srv := newTestServer(t, ctx)
	deviceID := enrollForTest(t, srv, "mac-mini-acme-09", "09090909-0909-4909-8909-090909090909")

	resp := doDeviceGet(t, srv.URL, deviceID, mintAccessToken(t, ctx, srv))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 200; body=%s", resp.StatusCode, raw)
	}

	var out struct {
		MtlsCertExpiresAt     *string `json:"mtls_cert_expires_at"`
		MtlsCertDaysRemaining *int    `json:"mtls_cert_days_remaining"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// The cert expiry minted at enrollment must round-trip through the
	// devices row and back out on the read endpoint.
	if out.MtlsCertExpiresAt == nil {
		t.Fatalf("mtls_cert_expires_at is null — not persisted at enrollment")
	}
	expiresAt, err := time.Parse(time.RFC3339, *out.MtlsCertExpiresAt)
	if err != nil {
		t.Fatalf("mtls_cert_expires_at not RFC3339: %v", err)
	}
	// The fake provisioner mints a 365-day cert.
	if until := time.Until(expiresAt); until < 360*24*time.Hour || until > 366*24*time.Hour {
		t.Errorf("mtls_cert_expires_at %v not ~365d out (%v remaining)", expiresAt, until)
	}
	if out.MtlsCertDaysRemaining == nil {
		t.Fatalf("mtls_cert_days_remaining is null")
	}
	if d := *out.MtlsCertDaysRemaining; d < 363 || d > 365 {
		t.Errorf("mtls_cert_days_remaining: got %d want ~365", d)
	}
}

func TestDeviceListIncludesSiteAndClient(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	// A device assigned to a site, and one with no site.
	clientID := insertClient(t, ctx, srv, "Acme Corp")
	siteID := insertSite(t, ctx, srv, clientID, "Acme HQ")
	insertDeviceAtSite(t, ctx, srv, "mac-sited", siteID)
	enrollForTest(t, srv, "mac-unsited", "0a0a0a0a-0a0a-4a0a-8a0a-0a0a0a0a0a0a")

	rows := doDeviceList(t, srv.URL, mintAccessToken(t, ctx, srv))
	byHost := map[string]map[string]any{}
	for _, r := range rows {
		byHost[r["hostname"].(string)] = r
	}

	sited := byHost["mac-sited"]
	if sited == nil {
		t.Fatalf("mac-sited not in the device list")
	}
	if sited["site_name"] != "Acme HQ" {
		t.Errorf("site-assigned device site_name: got %v want %q", sited["site_name"], "Acme HQ")
	}
	if sited["client_name"] != "Acme Corp" {
		t.Errorf("site-assigned device client_name: got %v want %q", sited["client_name"], "Acme Corp")
	}

	unsited := byHost["mac-unsited"]
	if unsited == nil {
		t.Fatalf("mac-unsited not in the device list")
	}
	if unsited["site_name"] != nil {
		t.Errorf("site-less device site_name: got %v want null", unsited["site_name"])
	}
	if unsited["client_name"] != nil {
		t.Errorf("site-less device client_name: got %v want null", unsited["client_name"])
	}
}

func TestGetDeviceByIDIncludesSiteAndClient(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	// A device assigned to a site, and one with no site.
	clientID := insertClient(t, ctx, srv, "Acme Corp")
	siteID := insertSite(t, ctx, srv, clientID, "Acme HQ")
	sitedID := insertDeviceAtSite(t, ctx, srv, "mac-sited", siteID)
	unsitedID := enrollForTest(t, srv, "mac-unsited", "0b0b0b0b-0b0b-4b0b-8b0b-0b0b0b0b0b0b")

	token := mintAccessToken(t, ctx, srv)

	sited := decodeDeviceGet(t, srv.URL, sitedID, token)
	if sited["site_name"] != "Acme HQ" {
		t.Errorf("site-assigned device site_name: got %v want %q", sited["site_name"], "Acme HQ")
	}
	if sited["client_name"] != "Acme Corp" {
		t.Errorf("site-assigned device client_name: got %v want %q", sited["client_name"], "Acme Corp")
	}

	unsited := decodeDeviceGet(t, srv.URL, unsitedID, token)
	if unsited["site_name"] != nil {
		t.Errorf("site-less device site_name: got %v want null", unsited["site_name"])
	}
	if unsited["client_name"] != nil {
		t.Errorf("site-less device client_name: got %v want null", unsited["client_name"])
	}
}

// Registry.List must populate Device.SiteID, not just SiteName — the rollout
// site picker (#64) builds its dropdown from each device's site_id, so a List
// that scans site_name but drops site_id leaves every device "unassigned" and
// the picker shows "No assigned sites in scope". (GetByID already scanned it;
// List silently omitted it.)
func TestRegistryListPopulatesSiteID(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	clientID := insertClient(t, ctx, srv, "Acme Corp")
	siteID := insertSite(t, ctx, srv, clientID, "Acme HQ")
	sitedID := insertDeviceAtSite(t, ctx, srv, "mac-sited", siteID)
	unsitedID := enrollForTest(t, srv, "mac-unsited", "0c0c0c0c-0c0c-4c0c-8c0c-0c0c0c0c0c0c")

	devices, err := srv.Registry.List(staffCtx(ctx))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	bySited := map[string]*string{}
	for _, d := range devices {
		bySited[d.ID] = d.SiteID
	}

	got, ok := bySited[sitedID]
	if !ok {
		t.Fatalf("sited device %s not in List", sitedID)
	}
	if got == nil || *got != siteID {
		t.Errorf("sited device SiteID: got %v want %q", got, siteID)
	}
	if unsited, ok := bySited[unsitedID]; !ok {
		t.Fatalf("unsited device %s not in List", unsitedID)
	} else if unsited != nil {
		t.Errorf("unsited device SiteID: got %v want nil", *unsited)
	}
}

// decodeDeviceGet issues an authenticated GET /devices/{id} and returns the
// decoded JSON object.
func decodeDeviceGet(t *testing.T, baseURL, deviceID, token string) map[string]any {
	t.Helper()
	resp := doDeviceGet(t, baseURL, deviceID, token)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET /devices/%s: got %d want 200; body=%s", deviceID, resp.StatusCode, raw)
	}
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestGetDeviceByIDUnknownReturns404(t *testing.T) {
	requireDocker(t)
	ctx := context.Background()
	srv := newTestServer(t, ctx)

	// A syntactically-valid UUID that won't match any row.
	resp := doDeviceGet(t, srv.URL, "00000000-0000-0000-0000-000000000000", mintAccessToken(t, ctx, srv))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 404; body=%s", resp.StatusCode, raw)
	}
}

// doDeviceGet issues an authenticated GET /devices/{id}. The caller owns
// resp.Body.
func doDeviceGet(t *testing.T, baseURL, deviceID, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, baseURL+"/devices/"+deviceID, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	return resp
}
