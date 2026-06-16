package notify

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/emilejacobs/control-plane/internal/cp/ingest"
	"github.com/emilejacobs/control-plane/internal/cp/registry"
)

// SettingStore is the CP-settings read surface the config source needs.
// *registry.Registry satisfies it.
type SettingStore interface {
	GetCPSetting(ctx context.Context, key string) (string, bool, error)
}

// SettingsConfigSource loads the notification config from the cp_settings store
// (#96 keys). Implements ingest.ConfigSource, read by the reconciler each tick
// so an operator's Settings edit applies without a redeploy.
type SettingsConfigSource struct {
	store SettingStore
}

func NewSettingsConfigSource(store SettingStore) *SettingsConfigSource {
	return &SettingsConfigSource{store: store}
}

func (s *SettingsConfigSource) Load(ctx context.Context) (ingest.NotificationConfig, error) {
	enabled, _, err := s.store.GetCPSetting(ctx, registry.SettingNotificationsEnabled)
	if err != nil {
		return ingest.NotificationConfig{}, fmt.Errorf("load notifications.enabled: %w", err)
	}
	recipientsRaw, _, err := s.store.GetCPSetting(ctx, registry.SettingNotificationsRecipients)
	if err != nil {
		return ingest.NotificationConfig{}, fmt.Errorf("load notifications.email_recipients: %w", err)
	}
	webhook, _, err := s.store.GetCPSetting(ctx, registry.SettingTeamsWebhookURL)
	if err != nil {
		return ingest.NotificationConfig{}, fmt.Errorf("load notifications.teams_webhook_url: %w", err)
	}

	return ingest.NotificationConfig{
		Enabled: enabled == "true",
		NotifyConfig: ingest.NotifyConfig{
			Recipients:      parseRecipients(recipientsRaw),
			TeamsWebhookURL: webhook,
		},
	}, nil
}

// parseRecipients decodes the stored JSON array, tolerating unset/empty/invalid
// by returning nil (no recipients → email channel skipped).
func parseRecipients(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}
