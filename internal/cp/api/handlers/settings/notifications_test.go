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

type fakeStore struct{ m map[string]string }

func (f *fakeStore) GetCPSetting(_ context.Context, k string) (string, bool, error) {
	v, ok := f.m[k]
	return v, ok, nil
}
func (f *fakeStore) SetCPSetting(_ context.Context, k, v string) error { f.m[k] = v; return nil }

type fakeAudit struct{}

func (fakeAudit) Write(context.Context, audit.Entry) error { return nil }

type notifResp struct {
	Enabled             bool `json:"enabled"`
	OfflineGraceSeconds int  `json:"offline_grace_seconds"`
}

// GET reports the configured offline-grace window, defaulting to 180s when the
// setting is unset.
func TestNotificationsGetOfflineGrace(t *testing.T) {
	cases := []struct {
		stored string
		set    bool
		want   int
	}{
		{"", false, 180}, // unset → default
		{"120", true, 120},
		{"0", true, 0},
	}
	for _, tc := range cases {
		m := map[string]string{}
		if tc.set {
			m[registry.SettingOfflineGraceSeconds] = tc.stored
		}
		rec := httptest.NewRecorder()
		settings.NewNotificationsGet(&fakeStore{m: m}).ServeHTTP(
			rec, httptest.NewRequest(http.MethodGet, "/settings/notifications", nil))
		var out notifResp
		_ = json.NewDecoder(rec.Body).Decode(&out)
		if out.OfflineGraceSeconds != tc.want {
			t.Errorf("stored=%q set=%v: offline_grace_seconds = %d, want %d", tc.stored, tc.set, out.OfflineGraceSeconds, tc.want)
		}
	}
}

// PUT persists a valid offline-grace value and echoes it.
func TestNotificationsPutSetsGrace(t *testing.T) {
	store := &fakeStore{m: map[string]string{}}
	rec := httptest.NewRecorder()
	settings.NewNotificationsPut(store, fakeAudit{}).ServeHTTP(rec, httptest.NewRequest(
		http.MethodPut, "/settings/notifications",
		strings.NewReader(`{"enabled":true,"email_recipients":[],"offline_grace_seconds":240}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d; body=%s", rec.Code, rec.Body.String())
	}
	if got := store.m[registry.SettingOfflineGraceSeconds]; got != "240" {
		t.Errorf("stored grace = %q, want 240", got)
	}
	var out notifResp
	_ = json.NewDecoder(rec.Body).Decode(&out)
	if out.OfflineGraceSeconds != 240 {
		t.Errorf("echoed grace = %d, want 240", out.OfflineGraceSeconds)
	}
}

// An omitted offline_grace_seconds leaves the stored value untouched (so older
// clients that only send enabled/recipients don't reset it).
func TestNotificationsPutOmittedGraceUnchanged(t *testing.T) {
	store := &fakeStore{m: map[string]string{registry.SettingOfflineGraceSeconds: "90"}}
	rec := httptest.NewRecorder()
	settings.NewNotificationsPut(store, fakeAudit{}).ServeHTTP(rec, httptest.NewRequest(
		http.MethodPut, "/settings/notifications",
		strings.NewReader(`{"enabled":true,"email_recipients":[]}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if got := store.m[registry.SettingOfflineGraceSeconds]; got != "90" {
		t.Errorf("omitted grace should be unchanged; got %q want 90", got)
	}
}

// Out-of-range grace values are rejected.
func TestNotificationsPutRejectsBadGrace(t *testing.T) {
	for _, body := range []string{
		`{"enabled":true,"email_recipients":[],"offline_grace_seconds":-1}`,
		`{"enabled":true,"email_recipients":[],"offline_grace_seconds":99999}`,
	} {
		store := &fakeStore{m: map[string]string{}}
		rec := httptest.NewRecorder()
		settings.NewNotificationsPut(store, fakeAudit{}).ServeHTTP(rec, httptest.NewRequest(
			http.MethodPut, "/settings/notifications", strings.NewReader(body)))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("body %s: status %d, want 400", body, rec.Code)
		}
		if _, ok := store.m[registry.SettingOfflineGraceSeconds]; ok {
			t.Errorf("body %s: should not persist an invalid grace", body)
		}
	}
}
