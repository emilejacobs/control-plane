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

// SnapshotCadence is the per-device scheduled-snapshot frequency (#9).
export type SnapshotCadence = "off" | "daily" | "weekly";

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
  // siteId is the local UUID — used by the EditDeploymentModal to
  // pre-select the current site in the picker without fragile
  // name-matching. Null when unassigned.
  siteId: string | null;
  siteName: string | null;
  clientName: string | null;
  // Fleet-tracking identifier set during install. Null until
  // install-module 11 starts shipping it; rendered "Unassigned" on
  // the per-device Deployment card.
  assetNumber: string | null;
  // Issue #14 / migration 018: the three network fields the agent
  // publishes on every heartbeat. All null until the first
  // post-rollout heartbeat lands (and tailscaleName / tailscaleIp
  // stay null forever on non-tailnet devices).
  //
  // edgePreviewURL prefers tailscaleName over hostname so the
  // Verify-angle deep-link still resolves when device.hostname
  // diverged from the tailnet's MagicDNS view (the bench-Mac
  // drift case 2026-05-26). CamerasPanel exposes a secondary
  // "Copy LAN URL" affordance when lanIp is set — the LAN-IP
  // fallback hint deferred from issue #4 per ADR-032.
  lanIp: string | null;
  tailscaleIp: string | null;
  tailscaleName: string | null;
  // Phase 2 captures (#9): per-device scheduled-snapshot cadence; the
  // device page renders it as a picker.
  snapshotCadence: SnapshotCadence;
  // #84: whether a per-device Plate Recognizer license is configured. The
  // license itself is a secret and is never sent to the dashboard — the
  // device page shows set/not-set and lets staff (re)enter it.
  alprLicenseSet: boolean;
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

// Camera is the per-device camera inventory row (Phase 2 Edge UI
// rework, issue #2). cameraId is server-assigned (camN per device);
// isLpr indicates which row's RTSP URL feeds Plate Recognizer.
export interface Camera {
  cameraId: string;
  label: string;
  rtspUrl: string;
  isLpr: boolean;
}

// CamerasResponse is the GET /devices/{id}/cameras body. The
// lastAppliedAt + lastAppliedCorrelationId mirror the cmd-result ACK
// stamps; both null until the agent has ACKed at least one
// cameras.update. The Cameras panel renders a "pending" badge when
// either is null (operator just edited; ACK hasn't landed yet).
export interface CamerasResponse {
  cameras: Camera[];
  lastAppliedAt: string | null;
  lastAppliedCorrelationId: string | null;
}

interface CameraWire {
  camera_id: string;
  label: string;
  rtsp_url: string;
  is_lpr: boolean;
}

interface CamerasResponseWire {
  cameras: CameraWire[];
  last_applied_at: string | null;
  last_applied_correlation_id: string | null;
}

// CameraInput is the request body for POST + PUT — camera_id is
// server-assigned on POST, path-param on PUT, so it's never in the body.
export interface CameraInput {
  label: string;
  rtspUrl: string;
  isLpr: boolean;
}

export async function postCamera(deviceId: string, input: CameraInput): Promise<Camera> {
  const res = await apiRequest(`/devices/${deviceId}/cameras`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
    body: JSON.stringify({ label: input.label, rtsp_url: input.rtspUrl, is_lpr: input.isLpr }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, await res.text());
  }
  const d = (await res.json()) as CameraWire;
  return { cameraId: d.camera_id, label: d.label, rtspUrl: d.rtsp_url, isLpr: d.is_lpr };
}

export async function putCamera(
  deviceId: string,
  cameraId: string,
  input: CameraInput,
): Promise<Camera> {
  const res = await apiRequest(`/devices/${deviceId}/cameras/${cameraId}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
    body: JSON.stringify({ label: input.label, rtsp_url: input.rtspUrl, is_lpr: input.isLpr }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, await res.text());
  }
  const d = (await res.json()) as CameraWire;
  return { cameraId: d.camera_id, label: d.label, rtspUrl: d.rtsp_url, isLpr: d.is_lpr };
}

export async function deleteCamera(deviceId: string, cameraId: string): Promise<void> {
  const res = await apiRequest(`/devices/${deviceId}/cameras/${cameraId}`, {
    method: "DELETE",
    headers: { "Idempotency-Key": crypto.randomUUID() },
  });
  if (!res.ok && res.status !== 404) {
    throw new ApiError(res.status, await res.text());
  }
}

// edgePreviewURL builds the deep-link an operator clicks "Verify
// angle" to open in a new tab (issue #4, ADR-030 § 1, ADR-032). v1
// targets the device at port 5051 over plain HTTP — tailnet
// membership is the perimeter. The Edge UI binary's Next.js SPA at
// /preview/<camera_id> renders an <img> pointing at its own
// /preview/<id>/stream MJPEG endpoint.
//
// Host preference (issue #14): tailscaleName when present, else
// hostname. The bench-Mac drift case (2026-05-26) showed that
// `os.Hostname()`-derived hostname can diverge from the
// Tailscale-resolvable name; tailscaleName from the heartbeat is
// authoritative for MagicDNS resolution.
//
// edgeLanURL is the secondary affordance — the LAN-IP fallback
// hint described in ADR-030 § 1, deferred per ADR-032 until
// telemetry shipped lan_ip (issue #14). When lanIp is null, the
// LAN URL cannot be built; CamerasPanel hides the affordance.
export function edgePreviewURL(
  device: Pick<Device, "hostname"> & { tailscaleName?: string | null },
  cameraId: string,
): string {
  const host =
    device.tailscaleName && device.tailscaleName.length > 0
      ? device.tailscaleName
      : device.hostname;
  return `http://${host}:5051/preview/${cameraId}`;
}

// edgeLanURL returns the LAN-IP fallback URL when device.lanIp is
// non-empty, or null when the field hasn't landed yet (older
// agents / pre-rollout). Callers MUST handle the null case — the
// caller is responsible for hiding the affordance entirely rather
// than rendering a broken link.
export function edgeLanURL(
  device: { lanIp: string | null },
  cameraId: string,
): string | null {
  if (!device.lanIp) {
    return null;
  }
  return `http://${device.lanIp}:5051/preview/${cameraId}`;
}

export async function getCameras(deviceId: string): Promise<CamerasResponse> {
  const res = await apiRequest(`/devices/${deviceId}/cameras`);
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load cameras");
  }
  const d = (await res.json()) as CamerasResponseWire;
  return {
    cameras: (d.cameras ?? []).map((c) => ({
      cameraId: c.camera_id,
      label: c.label,
      rtspUrl: c.rtsp_url,
      isLpr: c.is_lpr,
    })),
    lastAppliedAt: d.last_applied_at,
    lastAppliedCorrelationId: d.last_applied_correlation_id,
  };
}

// DeviceService is one row from the device_services table — what the
// per-device Services panel renders.
export interface DeviceService {
  name: string;
  state: "running" | "stopped" | "unknown";
  stateSince: string;   // RFC3339
  lastReported: string; // RFC3339
}

// HealthProbe is one row from GET /devices/{id}/health-probes — what the
// per-device Health panel renders (Phase 2, issue #19). status is the
// agent-decided colour; state the OS-agnostic signal token; details the
// structured per-probe payload (whisper variant/size, config sha, etc.).
export interface HealthProbe {
  name: string;
  status: "green" | "yellow" | "red";
  state: string;
  details: Record<string, unknown>;
  lastObservedAt: string; // RFC3339
}

interface HealthProbeWire {
  name: string;
  status: "green" | "yellow" | "red";
  state: string;
  details: Record<string, unknown>;
  last_observed_at: string;
}

interface HealthProbesResponseWire {
  probes: HealthProbeWire[];
}

export async function getHealthProbes(deviceId: string): Promise<HealthProbe[]> {
  const res = await apiRequest(`/devices/${deviceId}/health-probes`);
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load health probes");
  }
  const d = (await res.json()) as HealthProbesResponseWire;
  return (d.probes ?? []).map((p) => ({
    name: p.name,
    status: p.status,
    state: p.state,
    details: p.details ?? {},
    lastObservedAt: p.last_observed_at,
  }));
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
  site_id: string | null;
  site_name: string | null;
  client_name: string | null;
  asset_number: string | null;
  lan_ip: string | null;
  tailscale_ip: string | null;
  tailscale_name: string | null;
  snapshot_cadence: SnapshotCadence;
  alpr_license_set: boolean;
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
    siteId: d.site_id ?? null,
    siteName: d.site_name,
    clientName: d.client_name,
    assetNumber: d.asset_number ?? null,
    lanIp: d.lan_ip ?? null,
    tailscaleIp: d.tailscale_ip ?? null,
    tailscaleName: d.tailscale_name ?? null,
    snapshotCadence: d.snapshot_cadence ?? "weekly",
    alprLicenseSet: d.alpr_license_set ?? false,
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

// === Log tail (Phase 2 slice 3) ===

// LogTail is the row state the dashboard polls for. Status walks
// pending → done | error as the agent's cmd-result lands; content +
// truncation are populated on the done path, errorCode + errorMessage
// on the error path.
export interface LogTail {
  correlationId: string;
  logName: string;
  linesRequested: number;
  status: "pending" | "done" | "error";
  content: string | null;
  truncated: boolean;
  truncatedFrom: number | null;
  errorCode: string | null;
  errorMessage: string | null;
  requestedAt: string;
  returnedAt: string | null;
}

interface LogTailWire {
  correlation_id: string;
  log_name: string;
  lines_requested: number;
  status: string;
  content: string | null;
  truncated: boolean;
  truncated_from: number | null;
  error_code: string | null;
  error_message: string | null;
  requested_at: string;
  returned_at: string | null;
}

export interface LogTailRequest {
  logName: string;
  lines: number;
}

// postLogTail initiates an operator-driven log tail. Returns the
// server-minted correlation_id; the dashboard polls getLogTail until
// status flips out of "pending".
export async function postLogTail(
  deviceId: string,
  request: LogTailRequest,
): Promise<{ correlationId: string }> {
  const res = await apiRequest(`/devices/${deviceId}/logs/tail`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ log_name: request.logName, lines: request.lines }),
  });
  if (res.status === 400) {
    const detail = (await res.json().catch(() => ({}))) as {
      code?: string;
      message?: string;
    };
    const code = detail.code ? ` (${detail.code})` : "";
    throw new ApiError(400, `${detail.message ?? "invalid log tail request"}${code}`);
  }
  if (!res.ok) {
    throw new ApiError(res.status, "failed to start log tail");
  }
  const payload = (await res.json()) as { correlation_id: string };
  return { correlationId: payload.correlation_id };
}

// getLogTail fetches the current row state for a pending or completed
// log tail. Dashboard polls this every ~2s until status ≠ "pending".
export async function getLogTail(
  deviceId: string,
  correlationId: string,
): Promise<LogTail> {
  const res = await apiRequest(
    `/devices/${deviceId}/logs/tail/${correlationId}`,
  );
  if (!res.ok) {
    throw new ApiError(res.status, "failed to fetch log tail");
  }
  const d = (await res.json()) as LogTailWire;
  return {
    correlationId: d.correlation_id,
    logName: d.log_name,
    linesRequested: d.lines_requested,
    status: d.status as LogTail["status"],
    content: d.content,
    truncated: d.truncated,
    truncatedFrom: d.truncated_from,
    errorCode: d.error_code,
    errorMessage: d.error_message,
    requestedAt: d.requested_at,
    returnedAt: d.returned_at,
  };
}

// === Network scan (Phase 2 Edge UI rework, issue #3) ===

// NetworkScanHost is one candidate the agent's LAN scan returned. The
// dashboard's "Add as camera" button populates the Add Camera dialog
// with the host's IP pre-filled.
export interface NetworkScanHost {
  ip: string;
  mac: string;
  vendor: string;
  openPorts: number[];
}

// NetworkScan is the row state the dashboard polls for. Status walks
// pending → done | error as the agent's cmd-result lands; hosts is
// populated on the done path, errorCode + errorMessage on the error path.
export interface NetworkScan {
  correlationId: string;
  cidr: string | null;
  status: "pending" | "done" | "error";
  hosts: NetworkScanHost[] | null;
  errorCode: string | null;
  errorMessage: string | null;
  requestedAt: string;
  returnedAt: string | null;
}

interface NetworkScanWire {
  correlation_id: string;
  cidr: string | null;
  status: string;
  result: { hosts: { ip: string; mac: string; vendor: string; open_ports: number[] }[] } | null;
  error_code: string | null;
  error_message: string | null;
  requested_at: string;
  returned_at: string | null;
}

export interface NetworkScanRequest {
  // cidr is optional — pass undefined / empty for auto-detect mode.
  cidr?: string;
}

// postNetworkScan initiates an operator-triggered LAN scan. Returns the
// server-minted correlation_id; the dashboard polls getNetworkScan until
// status flips out of "pending".
export async function postNetworkScan(
  deviceId: string,
  request: NetworkScanRequest = {},
): Promise<{ correlationId: string }> {
  const body: Record<string, unknown> = {};
  if (request.cidr) {
    body.cidr = request.cidr;
  }
  const res = await apiRequest(`/devices/${deviceId}/network-scan`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify(body),
  });
  if (res.status === 400) {
    const detail = (await res.json().catch(() => ({}))) as {
      code?: string;
      message?: string;
    };
    const code = detail.code ? ` (${detail.code})` : "";
    throw new ApiError(400, `${detail.message ?? "invalid network scan request"}${code}`);
  }
  if (!res.ok) {
    throw new ApiError(res.status, "failed to start network scan");
  }
  const payload = (await res.json()) as { correlation_id: string };
  return { correlationId: payload.correlation_id };
}

// getNetworkScan fetches the current row state for a pending or
// completed scan. Dashboard polls this every ~2s until status ≠
// "pending".
export async function getNetworkScan(
  deviceId: string,
  correlationId: string,
): Promise<NetworkScan> {
  const res = await apiRequest(
    `/devices/${deviceId}/network-scan/${correlationId}`,
  );
  if (!res.ok) {
    throw new ApiError(res.status, "failed to fetch network scan");
  }
  const d = (await res.json()) as NetworkScanWire;
  return {
    correlationId: d.correlation_id,
    cidr: d.cidr,
    status: d.status as NetworkScan["status"],
    hosts:
      d.result == null
        ? null
        : d.result.hosts.map((h) => ({
            ip: h.ip,
            mac: h.mac,
            vendor: h.vendor,
            openPorts: h.open_ports,
          })),
    errorCode: d.error_code,
    errorMessage: d.error_message,
    requestedAt: d.requested_at,
    returnedAt: d.returned_at,
  };
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

// setSnapshotCadence updates a device's scheduled-snapshot cadence via
// PUT /devices/{id}/snapshot-config (#9). CP persists it; the agent picks it up
// via config.update (a later slice).
export async function setSnapshotCadence(
  id: string,
  cadence: SnapshotCadence,
): Promise<void> {
  const res = await apiRequest(`/devices/${id}/snapshot-config`, {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ cadence }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "failed to update snapshot cadence");
  }
}

// setALPRLicense stores a device's Plate Recognizer license (#84). The license
// is a secret — write-only; the device read exposes only alprLicenseSet. CP
// pushes it to the device at Commission.
export async function setALPRLicense(id: string, license: string): Promise<void> {
  const res = await apiRequest(`/devices/${id}/alpr-license`, {
    method: "PUT",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ license }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "failed to update ALPR license");
  }
}
