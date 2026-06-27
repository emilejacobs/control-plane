package settings_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/api/handlers/settings"
	"github.com/emilejacobs/control-plane/internal/cp/audit"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type hpStore struct{ m map[string]string }

func (f *hpStore) GetCPSetting(_ context.Context, k string) (string, bool, error) {
	v, ok := f.m[k]
	return v, ok, nil
}
func (f *hpStore) SetCPSetting(_ context.Context, k, v string) error { f.m[k] = v; return nil }

type noopAudit struct{}

func (noopAudit) Write(context.Context, audit.Entry) error { return nil }

type hpResp struct {
	EphemeralWarnPct float64 `json:"ephemeral_warn_pct"`
	EphemeralCritPct float64 `json:"ephemeral_crit_pct"`
	CloseWaitWarn    int     `json:"close_wait_warn"`
	CloseWaitCrit    int     `json:"close_wait_crit"`
}

// GET on an unconfigured store returns the calibrated defaults.
func TestHostPressureGet_Defaults(t *testing.T) {
	rec := httptest.NewRecorder()
	settings.NewHostPressureGet(&hpStore{m: map[string]string{}}).ServeHTTP(
		rec, httptest.NewRequest(http.MethodGet, "/settings/host-pressure", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var got hpResp
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.EphemeralWarnPct != 40 || got.EphemeralCritPct != 60 || got.CloseWaitWarn != 100 || got.CloseWaitCrit != 400 {
		t.Errorf("defaults = %+v, want 40/60/100/400", got)
	}
}

// PUT persists valid thresholds and echoes them back.
func TestHostPressurePut_Persists(t *testing.T) {
	store := &hpStore{m: map[string]string{}}
	body := `{"ephemeral_warn_pct":45,"ephemeral_crit_pct":65,"close_wait_warn":150,"close_wait_crit":500}`
	rec := httptest.NewRecorder()
	settings.NewHostPressurePut(store, noopAudit{}).ServeHTTP(
		rec, httptest.NewRequest(http.MethodPut, "/settings/host-pressure", strings.NewReader(body)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if store.m[registry.SettingHostPressureEphemeralCritPct] != "65" {
		t.Errorf("crit not persisted: %q", store.m[registry.SettingHostPressureEphemeralCritPct])
	}
}

// PUT rejects warn >= crit (a non-sensical band that would never warn before
// it criticals).
func TestHostPressurePut_RejectsWarnAboveCrit(t *testing.T) {
	rec := httptest.NewRecorder()
	body := `{"ephemeral_warn_pct":70,"ephemeral_crit_pct":60,"close_wait_warn":100,"close_wait_crit":400}`
	settings.NewHostPressurePut(&hpStore{m: map[string]string{}}, noopAudit{}).ServeHTTP(
		rec, httptest.NewRequest(http.MethodPut, "/settings/host-pressure", strings.NewReader(body)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}
