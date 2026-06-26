// NotificationSettingsCard renders the Settings → Notifications section (#96).
// Staff-only: a non-staff operator's 403 hides the card. The enable switch and
// email recipient list are read-write; the Teams webhook is a secret — the card
// shows only whether it is set (plus a non-sensitive preview) and lets staff
// replace it. The webhook value is never displayed.
"use client";

import { useEffect, useState } from "react";
import { Card } from "./ui/Card";
import {
  useNotificationSettings,
  useSetNotificationConfig,
  useSetTeamsWebhook,
} from "../lib/api/hooks";
import { ApiError } from "../lib/api/auth";

export function NotificationSettingsCard() {
  const settings = useNotificationSettings();
  const saveConfig = useSetNotificationConfig();
  const saveWebhook = useSetTeamsWebhook();

  const [enabled, setEnabled] = useState(false);
  const [recipients, setRecipients] = useState("");
  const [graceSeconds, setGraceSeconds] = useState("180");
  const [webhook, setWebhook] = useState("");

  // Seed the editable fields from the loaded config once it arrives.
  useEffect(() => {
    if (settings.data) {
      setEnabled(settings.data.enabled);
      setRecipients(settings.data.emailRecipients.join("\n"));
      setGraceSeconds(String(settings.data.offlineGraceSeconds));
    }
  }, [settings.data]);

  // Non-staff (or surface down): hide the card so the rest of Settings renders.
  if (settings.error) {
    if (settings.error instanceof ApiError && settings.error.status === 403) return null;
    return null;
  }
  if (settings.isLoading || !settings.data) return null;

  const onSaveConfig = () => {
    const list = recipients
      .split("\n")
      .map((s) => s.trim())
      .filter((s) => s !== "");
    const grace = Math.max(0, Math.min(3600, parseInt(graceSeconds, 10) || 0));
    saveConfig.mutate({ enabled, emailRecipients: list, offlineGraceSeconds: grace });
  };

  const onSaveWebhook = () => {
    if (!webhook) return;
    saveWebhook.mutate(webhook, { onSuccess: () => setWebhook("") });
  };

  const configError = saveConfig.error instanceof Error ? saveConfig.error.message : null;
  const webhookError = saveWebhook.error instanceof Error ? saveWebhook.error.message : null;

  return (
    <Card label="Notifications">
      <p className="muted">
        Fleet alerts (device offline, service stopped, health probe red) are sent
        to these channels. Edits apply within a minute — no deploy needed.
      </p>

      <label className="row" style={{ gap: 8, alignItems: "center" }}>
        <input
          type="checkbox"
          aria-label="Enable notifications"
          checked={enabled}
          onChange={(e) => setEnabled(e.target.checked)}
        />
        <span>Enable notifications</span>
      </label>

      <div style={{ marginTop: 12 }}>
        <label htmlFor="notif-recipients">Email recipients (one per line)</label>
        <textarea
          id="notif-recipients"
          aria-label="Email recipients"
          rows={4}
          value={recipients}
          disabled={saveConfig.isPending}
          onChange={(e) => setRecipients(e.target.value)}
          style={{
            display: "block",
            width: "100%",
            fontSize: 13,
            padding: "4px 8px",
            border: "1px solid var(--line, #ccc)",
            borderRadius: 4,
            marginTop: 4,
          }}
        />
        <div style={{ marginTop: 12 }}>
          <label htmlFor="notif-offline-grace">Offline alert delay (seconds)</label>
          <input
            id="notif-offline-grace"
            type="number"
            aria-label="Offline alert delay seconds"
            min={0}
            max={3600}
            value={graceSeconds}
            disabled={saveConfig.isPending}
            onChange={(e) => setGraceSeconds(e.target.value)}
            style={{
              display: "block",
              width: 120,
              fontSize: 13,
              padding: "4px 8px",
              border: "1px solid var(--line, #ccc)",
              borderRadius: 4,
              marginTop: 4,
            }}
          />
          <p className="muted" style={{ fontSize: 12, marginTop: 4 }}>
            Wait this long before alerting on an offline device — suppresses brief
            network blips. 0 alerts immediately. Default 180 (3 min).
          </p>
        </div>

        <div className="row" style={{ gap: 8, alignItems: "center", marginTop: 8 }}>
          <button
            type="button"
            className="btn"
            onClick={onSaveConfig}
            disabled={saveConfig.isPending}
          >
            Save
          </button>
          {saveConfig.isPending && (
            <span className="muted" role="status">
              Saving…
            </span>
          )}
          {configError && (
            <span role="alert" style={{ color: "var(--red, #c00)" }}>
              {configError}
            </span>
          )}
        </div>
      </div>

      <div style={{ marginTop: 16 }}>
        <p>
          Teams webhook:{" "}
          <strong>{settings.data.teamsWebhookSet ? "Configured" : "Not set"}</strong>
          {settings.data.teamsWebhookSet && settings.data.teamsWebhookPreview && (
            <span className="muted"> ({settings.data.teamsWebhookPreview})</span>
          )}
        </p>
        <div className="row" style={{ gap: 8, alignItems: "center" }}>
          <input
            type="password"
            aria-label="Teams webhook URL"
            placeholder={settings.data.teamsWebhookSet ? "Replace webhook URL…" : "Enter webhook URL…"}
            value={webhook}
            disabled={saveWebhook.isPending}
            onChange={(e) => setWebhook(e.target.value)}
            style={{
              fontSize: 13,
              padding: "4px 8px",
              border: "1px solid var(--line, #ccc)",
              borderRadius: 4,
              minWidth: 280,
            }}
          />
          <button
            type="button"
            className="btn"
            onClick={onSaveWebhook}
            disabled={saveWebhook.isPending || webhook === ""}
          >
            Save webhook
          </button>
          {webhookError && (
            <span role="alert" style={{ color: "var(--red, #c00)" }}>
              {webhookError}
            </span>
          )}
        </div>
      </div>
    </Card>
  );
}
