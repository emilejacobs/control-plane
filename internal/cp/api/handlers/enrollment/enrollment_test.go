package enrollment

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/cplog"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// fakeService is a stand-in for the registry — it records the EnrollInput it
// received and returns a preset output/error, so handler tests stay free of
// Postgres and IoT Core.
type fakeService struct {
	out registry.EnrollOutput
	err error
	got registry.EnrollInput
}

func (f *fakeService) Enroll(_ context.Context, in registry.EnrollInput) (registry.EnrollOutput, error) {
	f.got = in
	return f.out, f.err
}

// enroll drives the handler — wrapped in the cplog middleware so audit lines
// land in logs — and returns the recorder.
func enroll(h http.Handler, logs *bytes.Buffer, body map[string]any) *httptest.ResponseRecorder {
	wrapped := cplog.Middleware(cplog.New(logs, "cp-api"))(h)
	buf, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/enrollments", bytes.NewReader(buf))
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)
	return rec
}

// auditLogged reports whether buf has a JSON log line with msg == wantMsg and
// every want attribute matching.
func auditLogged(buf, wantMsg string, want map[string]any) bool {
	for _, line := range strings.Split(buf, "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) != nil || entry["msg"] != wantMsg {
			continue
		}
		match := true
		for k, v := range want {
			if entry[k] != v {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestEnrollmentAuditsSuccess(t *testing.T) {
	var logs bytes.Buffer
	h := New(&fakeService{out: registry.EnrollOutput{DeviceID: "dev-1"}}, nil)

	rec := enroll(h, &logs, map[string]any{
		"bootstrap_key": "k",
		"hostname":      "mac-mini-acme-01",
		"hardware_uuid": "11111111-2222-3333-4444-555555555555",
		"hardware_kind": "mac",
	})

	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d want 201", rec.Code)
	}
	// httptest.NewRequest stamps RemoteAddr 192.0.2.1:1234.
	if !auditLogged(logs.String(), "audit.enrollment", map[string]any{
		"outcome":       "success",
		"hostname":      "mac-mini-acme-01",
		"hardware_uuid": "11111111-2222-3333-4444-555555555555",
		"source_ip":     "192.0.2.1",
	}) {
		t.Errorf("no audit.enrollment success line:\n%s", logs.String())
	}
	// A convention-conforming hostname raises no anomaly alert.
	if strings.Contains(logs.String(), "audit.enrollment.anomaly") {
		t.Errorf("conventional hostname raised an anomaly alert:\n%s", logs.String())
	}
}

// TestEnrollmentAcceptsFieldNamingConvention covers the in-field hostname
// convention `<id>-<chain>-<store>-macmini` (e.g. `07-eegees-mesa-macmini`)
// adopted by the existing fleet — those hostnames should NOT raise the
// anomaly alert. The legacy `<kind>-<site>-<NN>` convention stays valid.
func TestEnrollmentAcceptsFieldNamingConvention(t *testing.T) {
	cases := []string{
		"07-eegees-mesa-macmini",
		"123-bigchain-store-42-macmini",
		"1-a-b-macmini",
	}
	for _, hostname := range cases {
		t.Run(hostname, func(t *testing.T) {
			var logs bytes.Buffer
			h := New(&fakeService{out: registry.EnrollOutput{DeviceID: "dev-1"}}, nil)

			rec := enroll(h, &logs, map[string]any{
				"bootstrap_key": "k",
				"hostname":      hostname,
				"hardware_uuid": "11111111-2222-3333-4444-555555555555",
				"hardware_kind": "mac",
			})

			if rec.Code != http.StatusCreated {
				t.Fatalf("status: got %d want 201", rec.Code)
			}
			if strings.Contains(logs.String(), "audit.enrollment.anomaly") {
				t.Errorf("field-convention hostname %q raised anomaly alert:\n%s", hostname, logs.String())
			}
		})
	}
}

func TestEnrollmentAlertsOnHostnameAnomaly(t *testing.T) {
	var logs bytes.Buffer
	h := New(&fakeService{out: registry.EnrollOutput{DeviceID: "dev-1"}}, nil)

	rec := enroll(h, &logs, map[string]any{
		"bootstrap_key": "k",
		"hostname":      "rogue-laptop", // not <kind>-<site>-<NN>
		"hardware_uuid": "11111111-2222-3333-4444-555555555555",
		"hardware_kind": "mac",
	})

	// The convention is a sanity check, not an allowlist (ADR-017): the
	// enrollment still completes.
	if rec.Code != http.StatusCreated {
		t.Fatalf("status: got %d want 201", rec.Code)
	}
	if !auditLogged(logs.String(), "audit.enrollment.anomaly", map[string]any{
		"alert":     "hostname_convention",
		"hostname":  "rogue-laptop",
		"source_ip": "192.0.2.1",
	}) {
		t.Errorf("no hostname-anomaly alert line:\n%s", logs.String())
	}
}

func TestEnrollmentAuditsInvalidKey(t *testing.T) {
	var logs bytes.Buffer
	h := New(&fakeService{err: registry.ErrInvalidBootstrapKey}, nil)

	rec := enroll(h, &logs, map[string]any{
		"bootstrap_key": "wrong",
		"hostname":      "mac-mini-acme-01",
		"hardware_uuid": "11111111-2222-3333-4444-555555555555",
		"hardware_kind": "mac",
	})

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", rec.Code)
	}
	if !auditLogged(logs.String(), "audit.enrollment", map[string]any{
		"outcome":   "failure",
		"reason":    "invalid_bootstrap_key",
		"source_ip": "192.0.2.1",
		"hostname":  "mac-mini-acme-01",
	}) {
		t.Errorf("no audit.enrollment failure line:\n%s", logs.String())
	}
}
