// CP-singleton settings calls against cp-api (#84). The account-wide Plate
// Recognizer token is write-only over the API: setPRToken stores it;
// getPRTokenStatus reports only whether it is set — never the value.
import { apiRequest } from "./client";
import { ApiError } from "./auth";

export interface PRTokenStatus {
  isSet: boolean;
}

export async function getPRTokenStatus(): Promise<PRTokenStatus> {
  const res = await apiRequest("/settings/pr-token");
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load PR token status");
  }
  const d = (await res.json()) as { is_set: boolean };
  return { isSet: d.is_set ?? false };
}

export async function setPRToken(token: string): Promise<void> {
  const res = await apiRequest("/settings/pr-token", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ token }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "failed to update PR token");
  }
}

// Notification settings (PRD .scratch/fleet-notifications, #96). enabled and
// email recipients are read-write; the Teams webhook is a write-only secret —
// the read exposes only whether it is set plus a non-sensitive preview, never
// the signed URL itself.
export interface NotificationSettings {
  enabled: boolean;
  emailRecipients: string[];
  // offlineGraceSeconds is the debounce window: a device must be offline this
  // long before an OFFLINE alert fires, suppressing brief network blips. 0 =
  // alert immediately. Defaults to 180.
  offlineGraceSeconds: number;
  teamsWebhookSet: boolean;
  teamsWebhookPreview: string;
}

export async function getNotificationSettings(): Promise<NotificationSettings> {
  const res = await apiRequest("/settings/notifications");
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load notification settings");
  }
  const d = (await res.json()) as {
    enabled?: boolean;
    email_recipients?: string[];
    offline_grace_seconds?: number;
    teams_webhook_set?: boolean;
    teams_webhook_preview?: string;
  };
  return {
    enabled: d.enabled ?? false,
    emailRecipients: d.email_recipients ?? [],
    offlineGraceSeconds: d.offline_grace_seconds ?? 180,
    teamsWebhookSet: d.teams_webhook_set ?? false,
    teamsWebhookPreview: d.teams_webhook_preview ?? "",
  };
}

// setNotificationConfig writes the non-secret config (enable switch + recipient
// list + the offline-alert debounce window) in one PUT.
export async function setNotificationConfig(
  enabled: boolean,
  emailRecipients: string[],
  offlineGraceSeconds: number,
): Promise<void> {
  const res = await apiRequest("/settings/notifications", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({
      enabled,
      email_recipients: emailRecipients,
      offline_grace_seconds: offlineGraceSeconds,
    }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "failed to update notification settings");
  }
}

// setTeamsWebhook stores the write-only Teams webhook URL on its own endpoint,
// mirroring the PR-token secret path.
export async function setTeamsWebhook(webhookUrl: string): Promise<void> {
  const res = await apiRequest("/settings/notifications/teams-webhook", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ webhook_url: webhookUrl }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "failed to update Teams webhook");
  }
}

// Host-network-pressure probe scoring thresholds (store13/mesa port-exhaustion
// incident). All four are read-write; the API fills unset fields with the
// calibrated defaults. Edits apply fleet-wide on the next probe (~5 min).
export interface HostPressureThresholds {
  ephemeralWarnPct: number;
  ephemeralCritPct: number;
  closeWaitWarn: number;
  closeWaitCrit: number;
}

export async function getHostPressureThresholds(): Promise<HostPressureThresholds> {
  const res = await apiRequest("/settings/host-pressure");
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load host-pressure thresholds");
  }
  const d = (await res.json()) as {
    ephemeral_warn_pct?: number;
    ephemeral_crit_pct?: number;
    close_wait_warn?: number;
    close_wait_crit?: number;
  };
  return {
    ephemeralWarnPct: d.ephemeral_warn_pct ?? 40,
    ephemeralCritPct: d.ephemeral_crit_pct ?? 60,
    closeWaitWarn: d.close_wait_warn ?? 100,
    closeWaitCrit: d.close_wait_crit ?? 400,
  };
}

export async function setHostPressureThresholds(t: HostPressureThresholds): Promise<void> {
  const res = await apiRequest("/settings/host-pressure", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({
      ephemeral_warn_pct: t.ephemeralWarnPct,
      ephemeral_crit_pct: t.ephemeralCritPct,
      close_wait_warn: t.closeWaitWarn,
      close_wait_crit: t.closeWaitCrit,
    }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "failed to update host-pressure thresholds");
  }
}
