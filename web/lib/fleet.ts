// Grouping for the fleet view: devices by client, then by site, hostname-
// sorted within a site. Devices with no site fall into "Unassigned".
import type { DeviceSummary } from "./api/devices";

export const UNASSIGNED = "Unassigned";

export interface SiteGroup {
  siteName: string;
  devices: DeviceSummary[];
}

export interface ClientGroup {
  clientName: string;
  sites: SiteGroup[];
}

// groupDevices buckets devices by client → site. Clients and sites are
// ordered alphabetically (so "Unassigned" sorts last); devices within a site
// are ordered by hostname.
export function groupDevices(devices: DeviceSummary[]): ClientGroup[] {
  const byClient = new Map<string, Map<string, DeviceSummary[]>>();
  for (const device of devices) {
    const client = device.clientName ?? UNASSIGNED;
    const site = device.siteName ?? UNASSIGNED;
    const sites = byClient.get(client) ?? new Map<string, DeviceSummary[]>();
    byClient.set(client, sites);
    const siteDevices = sites.get(site) ?? [];
    sites.set(site, siteDevices);
    siteDevices.push(device);
  }

  return [...byClient.entries()]
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([clientName, sites]) => ({
      clientName,
      sites: [...sites.entries()]
        .sort(([a], [b]) => a.localeCompare(b))
        .map(([siteName, devices]) => ({
          siteName,
          devices: [...devices].sort((x, y) => x.hostname.localeCompare(y.hostname)),
        })),
    }));
}
