"use client";

import { useMemo } from "react";
import { useStartRollout, useAbortRollout } from "../lib/api/hooks";
import type { RolloutDevice } from "../lib/api/rollouts";
import { Card } from "./ui/Card";

// RolloutActions is the staff-only management surface for an in-progress
// rollout (#42 Slice C): promote the canary version to the rest of the fleet,
// or abort by reverting un-converged devices to their reported version. Both
// are POST /agent-rollouts under the hood (no campaign entity, ADR-035 §4).
// Renders nothing when there's no active rollout (no device has a desired
// version set).
interface Props {
  devices: RolloutDevice[];
}

export function RolloutActions({ devices }: Props) {
  const promote = useStartRollout();
  const abort = useAbortRollout();

  const { targetedCount, inFlight, promoteVersion } = useMemo(() => {
    const targeted = devices.filter((d) => d.desiredVersion != null);
    const flight = devices.filter((d) => d.state === "in_flight");
    const versions = [...new Set(targeted.map((d) => d.desiredVersion as string))];
    return {
      targetedCount: targeted.length,
      inFlight: flight,
      // Promote only makes sense when a single version is being rolled out.
      promoteVersion: versions.length === 1 ? versions[0] : null,
    };
  }, [devices]);

  if (targetedCount === 0) return null;

  const busy = promote.isPending || abort.isPending;
  const err =
    promote.error instanceof Error
      ? promote.error.message
      : abort.error instanceof Error
        ? abort.error.message
        : null;

  function onPromote() {
    if (!promoteVersion) return;
    promote.mutate({ version: promoteVersion, target: { kind: "all" } });
  }

  function onAbort() {
    abort.mutate(
      inFlight.map((d) => ({ id: d.id, reportedVersion: d.reportedVersion })),
    );
  }

  return (
    <Card label="Manage rollout">
      <div style={{ display: "grid", gap: 12, padding: "4px 2px" }}>
        <div className="row" style={{ gap: 12, flexWrap: "wrap", alignItems: "center" }}>
          <button
            className="btn"
            onClick={onPromote}
            disabled={!promoteVersion || busy}
            title={
              promoteVersion
                ? undefined
                : "More than one version is in flight — promote one rollout at a time"
            }
          >
            {promoteVersion
              ? `Promote ${promoteVersion} to entire fleet`
              : "Promote to entire fleet"}
          </button>
          <button
            className="btn"
            onClick={onAbort}
            disabled={inFlight.length === 0 || busy}
          >
            Revert {inFlight.length} un-converged to reported
          </button>
        </div>

        {promote.isSuccess && promote.data && (
          <span role="status" className="muted">
            Promoting {promoteVersion} — targeted {promote.data.targeted}, pushed to{" "}
            {promote.data.pushed} online now
          </span>
        )}
        {abort.isSuccess && abort.data && (
          <span role="status" className="muted">
            Reverted un-converged devices to their reported version
            {abort.data.skipped > 0 ? ` (${abort.data.skipped} skipped — never reported)` : ""}
          </span>
        )}
        {err && (
          <div role="alert" className="muted" style={{ color: "var(--danger, #b00)" }}>
            {err}
          </div>
        )}
      </div>
    </Card>
  );
}
