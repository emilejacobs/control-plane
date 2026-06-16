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
    teams_webhook_set?: boolean;
    teams_webhook_preview?: string;
  };
  return {
    enabled: d.enabled ?? false,
    emailRecipients: d.email_recipients ?? [],
    teamsWebhookSet: d.teams_webhook_set ?? false,
    teamsWebhookPreview: d.teams_webhook_preview ?? "",
  };
}

// setNotificationConfig writes the non-secret config (enable switch + recipient
// list) in one PUT.
export async function setNotificationConfig(
  enabled: boolean,
  emailRecipients: string[],
): Promise<void> {
  const res = await apiRequest("/settings/notifications", {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ enabled, email_recipients: emailRecipients }),
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
