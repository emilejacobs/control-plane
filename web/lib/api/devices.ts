// Device read calls against cp-api. The full fleet view is Issue 17; this
// slice ships the summary list the Devices shell renders.
import { apiRequest } from "./client";
import { ApiError } from "./auth";

export interface DeviceSummary {
  deviceId: string;
  hostname: string;
  isOnline: boolean;
  // null for a device with no site assigned — grouped under "Unassigned".
  siteName: string | null;
  clientName: string | null;
  // Phase 2 Chain A: per-device fields the overview tiles aggregate
  // over (Cert expiring ≤ 30d count, Agent version drift count, Cert
  // expiring soonest rollup). Both null for rows that predate
  // migration 006; agent_version is "" for pre-Phase-1 devices.
  agentVersion: string;
  certExpiresAt: string | null;
  certDaysRemaining: number | null;
}

interface DeviceSummaryWire {
  device_id: string;
  hostname: string;
  is_online: boolean;
  site_name: string | null;
  client_name: string | null;
  agent_version: string;
  mtls_cert_expires_at: string | null;
  mtls_cert_days_remaining: number | null;
}

// getDevices fetches the operator's site-scoped fleet from GET /devices.
export async function getDevices(): Promise<DeviceSummary[]> {
  const res = await apiRequest("/devices");
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load devices");
  }
  const body = (await res.json()) as { devices: DeviceSummaryWire[] };
  return body.devices.map((d) => ({
    deviceId: d.device_id,
    hostname: d.hostname,
    isOnline: d.is_online,
    siteName: d.site_name,
    clientName: d.client_name,
    agentVersion: d.agent_version ?? "",
    certExpiresAt: d.mtls_cert_expires_at ?? null,
    certDaysRemaining: d.mtls_cert_days_remaining ?? null,
  }));
}

// Device is the full per-device record GET /devices/{id} returns — the
// per-device view (Issue 18) renders it. siteName / clientName are null for
// a device with no site assigned.
export interface Device {
  deviceId: string;
  hostname: string;
  hardwareUuid: string;
  hardwareKind: string;
  osVersion: string;
  agentVersion: string;
  iotThingArn: string;
  isOnline: boolean;
  // lastSeenAt is the absolute instant of the last heartbeat, anchored from
  // the server's relative last_seen_ago_seconds at fetch time so the
  // per-device view can recompute a ticking ago-string between polls. Null
  // for a device that has never reported.
  lastSeenAt: Date | null;
  // mTLS cert expiry — the ISO timestamp and the whole days remaining,
  // computed server-side (#09). Both null only for rows predating the
  // migration that began persisting the cert notAfter.
  certExpiresAt: string | null;
  certDaysRemaining: number | null;
  enrolledAt: string;
  siteName: string | null;
  clientName: string | null;
  // Phase 2: per-service state from the agent's last service-status
  // report. Empty array for a device that has never reported (the
  // dashboard distinguishes "no report yet" from a missing field).
  services: DeviceService[];
  // Phase 2 slice 2: per-device allow-list + cadence override, plus
  // the last-applied tracking the cmd-result handler stamps. The
  // EditServicesModal reads + writes this block; the panel chrome
  // shows "(default)" vs "(overridden)" derived from allowListOverride.
  serviceConfig: ServiceConfig;
}

// DeviceService is one row from the device_services table — what the
// per-device Services panel renders.
export interface DeviceService {
  name: string;
  state: "running" | "stopped" | "unknown";
  stateSince: string;   // RFC3339
  lastReported: string; // RFC3339
}

// ServiceConfig is the per-device override block. null fields mean
// "no override" (agent uses its bundled defaults); a non-null
// allowListOverride of `[]` means "track nothing" — distinct from null.
// lastAppliedAt is null until the agent has ACKed the most recent
// (or any) config.update.
export interface ServiceConfig {
  allowListOverride: string[] | null;
  intervalOverride: string | null;
  lastAppliedAt: string | null; // RFC3339
  lastAppliedCorrelationId: string | null;
}

interface DeviceWire {
  device_id: string;
  hostname: string;
  hardware_uuid: string;
  hardware_kind: string;
  os_version: string;
  agent_version: string;
  iot_thing_arn: string;
  is_online: boolean;
  last_seen_ago_seconds: number | null;
  mtls_cert_expires_at: string | null;
  mtls_cert_days_remaining: number | null;
  enrolled_at: string;
  site_name: string | null;
  client_name: string | null;
  services: DeviceServiceWire[];
  service_config: ServiceConfigWire;
}

interface DeviceServiceWire {
  name: string;
  state: string;
  state_since: string;
  last_reported: string;
}

interface ServiceConfigWire {
  allow_list_override: string[] | null;
  interval_override: string | null;
  last_applied_at: string | null;
  last_applied_correlation_id: string | null;
}

// getDevice fetches one device's full record from GET /devices/{id}.
export async function getDevice(id: string): Promise<Device> {
  const res = await apiRequest(`/devices/${id}`);
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load device");
  }
  const d = (await res.json()) as DeviceWire;
  return {
    deviceId: d.device_id,
    hostname: d.hostname,
    hardwareUuid: d.hardware_uuid,
    hardwareKind: d.hardware_kind,
    osVersion: d.os_version,
    agentVersion: d.agent_version,
    iotThingArn: d.iot_thing_arn,
    isOnline: d.is_online,
    lastSeenAt:
      d.last_seen_ago_seconds == null
        ? null
        : new Date(Date.now() - d.last_seen_ago_seconds * 1000),
    certExpiresAt: d.mtls_cert_expires_at,
    certDaysRemaining: d.mtls_cert_days_remaining,
    enrolledAt: d.enrolled_at,
    siteName: d.site_name,
    clientName: d.client_name,
    services: (d.services ?? []).map((s) => ({
      name: s.name,
      state: s.state as DeviceService["state"],
      stateSince: s.state_since,
      lastReported: s.last_reported,
    })),
    serviceConfig: {
      allowListOverride: d.service_config?.allow_list_override ?? null,
      intervalOverride: d.service_config?.interval_override ?? null,
      lastAppliedAt: d.service_config?.last_applied_at ?? null,
      lastAppliedCorrelationId:
        d.service_config?.last_applied_correlation_id ?? null,
    },
  };
}

// putServiceConfig pushes a per-device allow-list + cadence override
// down to the agent via PUT /devices/{id}/service-config (Phase 2
// slice 2). Both fields are optional — pass null to clear, an array
// (possibly empty for "track nothing") or string to set. CP persists,
// publishes config.update on the cmd channel, returns 202 + the
// correlation_id the caller polls /devices/{id} against until
// serviceConfig.lastAppliedCorrelationId matches.
export interface ServiceConfigUpdate {
  serviceAllowList?: string[] | null;
  serviceStatusInterval?: string | null;
}

export async function putServiceConfig(
  id: string,
  update: ServiceConfigUpdate,
): Promise<{ correlationId: string }> {
  const body: Record<string, unknown> = {};
  if (update.serviceAllowList !== undefined) {
    body.service_allow_list = update.serviceAllowList;
  }
  if (update.serviceStatusInterval !== undefined) {
    body.service_status_interval = update.serviceStatusInterval;
  }
  const res = await apiRequest(`/devices/${id}/service-config`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (res.status === 400) {
    const detail = (await res.json().catch(() => ({}))) as {
      code?: string;
      message?: string;
    };
    const code = detail.code ? ` (${detail.code})` : "";
    throw new ApiError(400, `${detail.message ?? "invalid service config"}${code}`);
  }
  if (!res.ok) {
    throw new ApiError(res.status, "failed to update service config");
  }
  const payload = (await res.json()) as { correlation_id: string };
  return { correlationId: payload.correlation_id };
}
