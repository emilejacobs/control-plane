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
