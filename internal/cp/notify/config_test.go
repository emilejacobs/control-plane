package notify_test

import (
	"context"
	"testing"

	"github.com/emilejacobs/control-plane/internal/cp/notify"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

type fakeSettingStore struct {
	values map[string]string
}

func (f *fakeSettingStore) GetCPSetting(_ context.Context, key string) (string, bool, error) {
	v, ok := f.values[key]
	return v, ok, nil
}

// Load maps the three cp_settings keys onto the NotificationConfig: enabled
// flag, JSON recipient list, and webhook URL.
func TestSettingsConfigSourceLoad(t *testing.T) {
	store := &fakeSettingStore{values: map[string]string{
		registry.SettingNotificationsEnabled:    "true",
		registry.SettingNotificationsRecipients: `["a@x.com","b@y.com"]`,
		registry.SettingTeamsWebhookURL:         "https://hook.example/z",
	}}
	cfg, err := notify.NewSettingsConfigSource(store).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.Enabled {
		t.Error("enabled = false, want true")
	}
	if len(cfg.Recipients) != 2 || cfg.Recipients[0] != "a@x.com" || cfg.Recipients[1] != "b@y.com" {
		t.Errorf("recipients = %v", cfg.Recipients)
	}
	if cfg.TeamsWebhookURL != "https://hook.example/z" {
		t.Errorf("webhook = %q", cfg.TeamsWebhookURL)
	}
}

// Unset keys default safely: disabled, no recipients, no webhook.
func TestSettingsConfigSourceDefaultsWhenUnset(t *testing.T) {
	store := &fakeSettingStore{values: map[string]string{}}
	cfg, err := notify.NewSettingsConfigSource(store).Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Enabled {
		t.Error("enabled should default false when unset")
	}
	if len(cfg.Recipients) != 0 {
		t.Errorf("recipients = %v, want empty", cfg.Recipients)
	}
	if cfg.TeamsWebhookURL != "" {
		t.Errorf("webhook = %q, want empty", cfg.TeamsWebhookURL)
	}
}
