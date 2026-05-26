// CamerasPanel renders the per-device cameras inventory for the
// per-device view (Phase 2 Edge UI rework, issue #2). Reads cameras +
// lastAppliedAt from the GET /devices/{id}/cameras endpoint (fetched
// alongside the rest of the page on the standard 10-second poll).
//
// Visual contract: label | LPR pill (when set) | RTSP URL (mono). A
// "pending" pill renders at the panel header when lastAppliedAt is
// null — the operator just edited and the agent's ACK hasn't landed
// yet. Empty array shows a placeholder rather than an empty table so
// "no cameras configured" is distinguishable from a fetch failure.
import type { Camera } from "../lib/api/devices";
import { Pill } from "./ui/Pill";

interface Props {
  cameras: Camera[];
  lastAppliedAt: string | null;
}

export function CamerasPanel({ cameras, lastAppliedAt }: Props) {
  const pending = lastAppliedAt === null && cameras.length > 0;

  return (
    <div>
      {pending && (
        <div style={{ marginBottom: 8 }}>
          <Pill tone="amber">Pending</Pill>
          <span
            className="muted"
            style={{ marginLeft: 8, fontSize: 12.5 }}
          >
            Waiting for the agent to ACK the latest change.
          </span>
        </div>
      )}
      {cameras.length === 0 ? (
        <p className="muted" style={{ fontSize: 13, margin: 0 }}>
          No cameras configured yet. Use "Scan network" or "Add
          camera" to populate this device's inventory.
        </p>
      ) : (
        <table
          className="cameras"
          style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}
        >
          <thead>
            <tr style={{ textAlign: "left", color: "var(--ink-2)" }}>
              <th style={{ padding: "6px 8px" }}>Label</th>
              <th style={{ padding: "6px 8px", width: 60 }}>LPR</th>
              <th style={{ padding: "6px 8px" }}>RTSP URL</th>
            </tr>
          </thead>
          <tbody>
            {cameras.map((c) => (
              <tr
                key={c.cameraId}
                style={{ borderTop: "1px solid var(--line)" }}
              >
                <td style={{ padding: "6px 8px" }}>{c.label}</td>
                <td style={{ padding: "6px 8px" }}>
                  {c.isLpr && <Pill tone="green">LPR</Pill>}
                </td>
                <td
                  className="mono"
                  style={{
                    padding: "6px 8px",
                    fontSize: 11.5,
                    color: "var(--ink-2)",
                    wordBreak: "break-all",
                  }}
                >
                  {c.rtspUrl}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
