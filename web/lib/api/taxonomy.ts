// Taxonomy sync API calls against cp-api: status read + on-demand
// trigger. Both are staff-only; a non-staff caller gets a 403 that
// the Settings page renders as a hidden section (ADR-033 § 8).
import { apiRequest } from "./client";
import { ApiError } from "./auth";

export interface TaxonomyStatus {
  clientsTotal: number;
  clientsActive: number;
  sitesTotal: number;
  sitesActive: number;
  // null when no sync has ever completed; the Settings card renders "Never".
  lastSyncedAt: string | null;
}

interface TaxonomyStatusWire {
  clients_total: number;
  clients_active: number;
  sites_total: number;
  sites_active: number;
  last_synced_at: string | null;
}

// getTaxonomyStatus returns the counts + most recent sync timestamp
// the Clients & Sites Settings card renders. Throws ApiError(403) for
// non-staff so the card can hide itself.
export async function getTaxonomyStatus(): Promise<TaxonomyStatus> {
  const res = await apiRequest("/taxonomy/status");
  if (!res.ok) {
    throw new ApiError(res.status, "taxonomy status failed");
  }
  const body = (await res.json()) as TaxonomyStatusWire;
  return {
    clientsTotal: body.clients_total,
    clientsActive: body.clients_active,
    sitesTotal: body.sites_total,
    sitesActive: body.sites_active,
    lastSyncedAt: body.last_synced_at,
  };
}

export interface TaxonomySyncTrigger {
  taskArn: string;
}

// triggerTaxonomySync invokes POST /taxonomy/sync — the staff-only
// "Force sync now" button. Returns the new ECS task ARN; the
// Settings card displays "Sync started — refresh in ~1 minute".
export async function triggerTaxonomySync(): Promise<TaxonomySyncTrigger> {
  const res = await apiRequest("/taxonomy/sync", { method: "POST" });
  if (!res.ok) {
    throw new ApiError(res.status, "taxonomy sync trigger failed");
  }
  const body = (await res.json()) as { task_arn: string };
  return { taskArn: body.task_arn };
}
