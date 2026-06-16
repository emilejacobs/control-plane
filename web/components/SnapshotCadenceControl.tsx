"use client";

import { useSetSnapshotCadence } from "../lib/api/hooks";
import type { SnapshotCadence } from "../lib/api/devices";

// SnapshotCadenceControl is the per-device scheduled-snapshot picker (#9): off /
// daily / weekly. Changing it PUTs to /devices/{id}/snapshot-config; CP persists
// it and the agent picks it up via config.update (a later slice).
interface Props {
  deviceId: string;
  cadence: SnapshotCadence;
}

const OPTIONS: { value: SnapshotCadence; label: string }[] = [
  { value: "off", label: "Off" },
  { value: "daily", label: "Daily" },
  { value: "weekly", label: "Weekly" },
];

export function SnapshotCadenceControl({ deviceId, cadence }: Props) {
  const set = useSetSnapshotCadence(deviceId);
  const errorMessage = set.error instanceof Error ? set.error.message : null;

  return (
    <div className="row" style={{ gap: 8, alignItems: "center", fontSize: 12.5 }}>
      <span className="muted">Scheduled snapshots</span>
      <select
        aria-label="Scheduled snapshot cadence"
        value={cadence}
        disabled={set.isPending}
        onChange={(e) => set.mutate(e.target.value as SnapshotCadence)}
        style={{
          fontSize: 12.5,
          padding: "2px 6px",
          border: "1px solid var(--line, #ccc)",
          borderRadius: 4,
        }}
      >
        {OPTIONS.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
      {set.isPending && (
        <span className="muted" role="status">
          Saving…
        </span>
      )}
      {errorMessage && (
        <span role="alert" style={{ color: "var(--red, #c00)" }}>
          {errorMessage}
        </span>
      )}
    </div>
  );
}
