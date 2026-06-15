// Agent fleet-update rollout read calls against cp-api (issue #42, ADR-035 §4).
// Rollout state is DERIVED from desired_agent_version vs the reported
// agent_version — there is no campaign entity. This slice ships the read view
// (GET /fleet/agent-rollout); the start/abort/promote write surface lands in a
// later slice over POST /agent-rollouts.
import { apiRequest } from "./client";
import { ApiError } from "./auth";

// RolloutState mirrors the backend's per-device derivation:
//   done        reported_version == desired_version
//   in_flight   targeted (desired set) but not yet converged
//   rolled_back tried the desired version; the wrapper reverted it
//   untargeted  no desired_version set
export type RolloutState = "done" | "in_flight" | "rolled_back" | "untargeted";

export interface RolloutDevice {
  id: string;
  hostname: string;
  siteName: string | null;
  clientName: string | null;
  reportedVersion: string;
  // null when the device is untargeted (no desired_agent_version set).
  desiredVersion: string | null;
  isOnline: boolean;
  state: RolloutState;
}

export interface RolloutCounts {
  done: number;
  inFlight: number;
  rolledBack: number;
  untargeted: number;
}

export interface AgentRollout {
  counts: RolloutCounts;
  devices: RolloutDevice[];
}

interface RolloutWire {
  counts: { done: number; in_flight: number; rolled_back: number; untargeted: number };
  devices: Array<{
    id: string;
    hostname: string;
    site_name: string | null;
    client_name: string | null;
    reported_version: string;
    desired_version: string | null;
    is_online: boolean;
    state: RolloutState;
  }>;
}

// getAgentVersions lists the published catalog versions (GET /fleet/agent-versions,
// #42) that back the target picker, newest-first. Auth-only (not staff-gated).
export async function getAgentVersions(): Promise<string[]> {
  const res = await apiRequest("/fleet/agent-versions");
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load agent versions");
  }
  const body = (await res.json()) as { versions: string[] };
  return body.versions ?? [];
}

// RolloutTarget is the exactly-one-of device selector the POST accepts: the
// whole fleet, a single site, or an explicit device subset (canary).
export type RolloutTarget =
  | { kind: "all" }
  | { kind: "site"; siteId: string }
  | { kind: "devices"; deviceIds: string[] };

export interface StartRolloutInput {
  version: string;
  target: RolloutTarget;
}

export interface StartRolloutResult {
  correlationId: string;
  targeted: number;
  pushed: number;
}

// RolloutError carries cp-api's structured rollout error code
// (agent_rollout.unknown_version / .no_targets / .bad_payload) alongside the
// human-readable message, so the start panel can tailor its inline notice.
export class RolloutError extends ApiError {
  constructor(
    status: number,
    message: string,
    public readonly code: string | null,
  ) {
    super(status, message);
    this.name = "RolloutError";
  }
}

// startRollout stamps the desired version on the target set (POST
// /agent-rollouts, staff-only). Abort / promote-canary are the same call with a
// different selector — there is no campaign entity (ADR-035 §4).
export async function startRollout(input: StartRolloutInput): Promise<StartRolloutResult> {
  const body: Record<string, unknown> = { version: input.version };
  switch (input.target.kind) {
    case "all":
      body.all = true;
      break;
    case "site":
      body.site_id = input.target.siteId;
      break;
    case "devices":
      body.device_ids = input.target.deviceIds;
      break;
  }

  const res = await apiRequest("/agent-rollouts", {
    method: "POST",
    body: JSON.stringify(body),
  });
  if (!res.ok) {
    let message = "failed to start rollout";
    let code: string | null = null;
    try {
      const err = (await res.json()) as { code?: string; message?: string };
      if (err.message) message = err.message;
      if (err.code) code = err.code;
    } catch {
      // non-JSON error body (e.g. a 502 from http.Error) — keep the default.
    }
    throw new RolloutError(res.status, message, code);
  }
  const r = (await res.json()) as {
    correlation_id: string;
    targeted: number;
    pushed: number;
  };
  return { correlationId: r.correlation_id, targeted: r.targeted, pushed: r.pushed };
}

// abortRollout reverts un-converged devices by resetting each one's desired
// version back to the version it currently reports (ADR-035 §4 abort = "reset
// desired to current"). Because POST /agent-rollouts sets one version per call,
// devices are grouped by reported version and one call is made per group.
// Devices with no reported version yet are skipped (there's nothing to reset to,
// and version "" would be rejected). Returns the group + skipped counts.
export async function abortRollout(
  devices: { id: string; reportedVersion: string }[],
): Promise<{ groups: number; skipped: number }> {
  const byVersion = new Map<string, string[]>();
  let skipped = 0;
  for (const d of devices) {
    if (!d.reportedVersion) {
      skipped++;
      continue;
    }
    const ids = byVersion.get(d.reportedVersion) ?? [];
    ids.push(d.id);
    byVersion.set(d.reportedVersion, ids);
  }
  for (const [version, deviceIds] of byVersion) {
    await startRollout({ version, target: { kind: "devices", deviceIds } });
  }
  return { groups: byVersion.size, skipped };
}

// getAgentRollout fetches the operator's site-scoped rollout view from
// GET /fleet/agent-rollout: roll-up counts plus per-device desired-vs-reported
// state. Site scope is applied server-side (staff see the fleet).
export async function getAgentRollout(): Promise<AgentRollout> {
  const res = await apiRequest("/fleet/agent-rollout");
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load agent rollout");
  }
  const body = (await res.json()) as RolloutWire;
  return {
    counts: {
      done: body.counts.done,
      inFlight: body.counts.in_flight,
      rolledBack: body.counts.rolled_back,
      untargeted: body.counts.untargeted,
    },
    devices: body.devices.map((d) => ({
      id: d.id,
      hostname: d.hostname,
      siteName: d.site_name,
      clientName: d.client_name,
      reportedVersion: d.reported_version,
      desiredVersion: d.desired_version,
      isOnline: d.is_online,
      state: d.state,
    })),
  };
}
