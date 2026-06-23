// Fleet-wide roll-up calls against cp-api. GET /fleet/alerts (#21) feeds the
// Overview alerts dashboard: which devices are red/yellow per probe and
// stopped per service, with the affected device ids inline for drill-down.
import { apiRequest } from "./client";
import { ApiError } from "./auth";

export interface ProbeAlert {
  probeName: string;
  red: string[]; // affected device ids
  yellow: string[];
}

export interface ServiceAlert {
  serviceName: string;
  stopped: string[]; // affected device ids
}

export interface FleetAlerts {
  probes: ProbeAlert[];
  services: ServiceAlert[];
}

interface ProbeAlertWire {
  probe_name: string;
  red?: string[];
  yellow?: string[];
}

interface ServiceAlertWire {
  service_name: string;
  stopped?: string[];
}

interface FleetAlertsWire {
  probes?: ProbeAlertWire[];
  services?: ServiceAlertWire[];
}

// OfflineCamera is one currently-offline camera in the fleet roll-up, carrying
// enough context (device, site, since-when) for the Camera alerts panel.
export interface OfflineCamera {
  cameraId: string;
  label: string;
  deviceId: string;
  hostname: string;
  siteName: string | null;
  statusChangedAt: string | null; // RFC3339 instant the camera went offline
}

// FleetCameras is the GET /fleet/cameras roll-up (#152): online/total counts
// for the Cameras gauge plus the offline-camera list (longest-outage first)
// for the Camera alerts panel.
export interface FleetCameras {
  total: number;
  online: number;
  offline: number;
  cameras: OfflineCamera[];
}

interface OfflineCameraWire {
  camera_id: string;
  label: string;
  device_id: string;
  hostname: string;
  site_name: string | null;
  status_changed_at: string | null;
}

interface FleetCamerasWire {
  total: number;
  online: number;
  offline: number;
  cameras?: OfflineCameraWire[];
}

// getFleetCameras loads the operator's site-scoped fleet camera roll-up. The
// cameras array is the offline subset, ordered longest-outage first.
export async function getFleetCameras(): Promise<FleetCameras> {
  const res = await apiRequest("/fleet/cameras");
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load fleet cameras");
  }
  const d = (await res.json()) as FleetCamerasWire;
  return {
    total: d.total,
    online: d.online,
    offline: d.offline,
    cameras: (d.cameras ?? []).map((c) => ({
      cameraId: c.camera_id,
      label: c.label,
      deviceId: c.device_id,
      hostname: c.hostname,
      siteName: c.site_name ?? null,
      statusChangedAt: c.status_changed_at ?? null,
    })),
  };
}

// getFleetAlerts loads the operator's site-scoped fleet alert roll-up. The
// response is alert-only — only probes/services with affected devices appear.
export async function getFleetAlerts(): Promise<FleetAlerts> {
  const res = await apiRequest("/fleet/alerts");
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load fleet alerts");
  }
  const d = (await res.json()) as FleetAlertsWire;
  return {
    probes: (d.probes ?? []).map((p) => ({
      probeName: p.probe_name,
      red: p.red ?? [],
      yellow: p.yellow ?? [],
    })),
    services: (d.services ?? []).map((s) => ({
      serviceName: s.service_name,
      stopped: s.stopped ?? [],
    })),
  };
}
