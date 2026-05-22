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
