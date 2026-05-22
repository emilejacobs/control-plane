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
}

interface DeviceSummaryWire {
  device_id: string;
  hostname: string;
  is_online: boolean;
  site_name: string | null;
  client_name: string | null;
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
  enrolledAt: string;
  siteName: string | null;
  clientName: string | null;
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
  enrolled_at: string;
  site_name: string | null;
  client_name: string | null;
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
    enrolledAt: d.enrolled_at,
    siteName: d.site_name,
    clientName: d.client_name,
  };
}
