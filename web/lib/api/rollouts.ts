// Agent fleet-update rollout read calls against cp-api (issue #42, ADR-035 §4).
// Rollout state is DERIVED from desired_agent_version vs the reported
// agent_version — there is no campaign entity. This slice ships the read view
// (GET /fleet/agent-rollout); the start/abort/promote write surface lands in a
// later slice over POST /agent-rollouts.
import { apiRequest } from "./client";
import { ApiError } from "./auth";

// RolloutState mirrors the backend's per-device derivation:
//   done       reported_version == desired_version
//   in_flight  targeted (desired set) but not yet converged
//   untargeted no desired_version set
export type RolloutState = "done" | "in_flight" | "untargeted";

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
  untargeted: number;
}

export interface AgentRollout {
  counts: RolloutCounts;
  devices: RolloutDevice[];
}

interface RolloutWire {
  counts: { done: number; in_flight: number; untargeted: number };
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
