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
  // Action callbacks open the CameraDialog at the page level. Each
  // is wired by page.tsx; the panel itself does not own the modal
  // state.
  onAddCamera?: () => void;
  onEditCamera?: (camera: Camera) => void;
  onDeleteCamera?: (camera: Camera) => void;
  // onScanNetwork triggers the "Scan network" install-flow shortcut
  // (issue #3, ADR-030 § 2). The parent owns the resulting modal +
  // post-scan correlation_id polling; the panel only renders the
  // button.
  onScanNetwork?: () => void;
}

export function CamerasPanel({
  cameras,
  lastAppliedAt,
  onAddCamera,
  onEditCamera,
  onDeleteCamera,
  onScanNetwork,
}: Props) {
  const pending = lastAppliedAt === null && cameras.length > 0;

  return (
    <div>
      <div
        style={{
          display: "flex",
          justifyContent: "space-between",
          alignItems: "center",
          marginBottom: 8,
        }}
      >
        <div>
          {pending && (
            <>
              <Pill tone="amber">Pending</Pill>
              <span
                className="muted"
                style={{ marginLeft: 8, fontSize: 12.5 }}
              >
                Waiting for the agent to ACK the latest change.
              </span>
            </>
          )}
        </div>
        <div style={{ display: "flex", gap: 8 }}>
          {onScanNetwork && (
            <button
              type="button"
              onClick={onScanNetwork}
              style={{
                padding: "4px 10px",
                fontSize: 12.5,
                background: "transparent",
                border: "1px solid var(--line, #ccc)",
                borderRadius: 4,
                cursor: "pointer",
              }}
            >
              Scan network
            </button>
          )}
          {onAddCamera && (
            <button
              type="button"
              onClick={onAddCamera}
              style={{
                padding: "4px 10px",
                fontSize: 12.5,
                background: "transparent",
                border: "1px solid var(--line, #ccc)",
                borderRadius: 4,
                cursor: "pointer",
              }}
            >
              + Add camera
            </button>
          )}
        </div>
      </div>
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
              {(onEditCamera || onDeleteCamera) && (
                <th style={{ padding: "6px 8px", width: 120 }}></th>
              )}
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
                {(onEditCamera || onDeleteCamera) && (
                  <td style={{ padding: "6px 8px", textAlign: "right" }}>
                    {onEditCamera && (
                      <button
                        type="button"
                        onClick={() => onEditCamera(c)}
                        style={{
                          fontSize: 12,
                          padding: "2px 8px",
                          background: "transparent",
                          border: "1px solid var(--line, #ccc)",
                          borderRadius: 4,
                          marginRight: 4,
                          cursor: "pointer",
                        }}
                      >
                        Edit
                      </button>
                    )}
                    {onDeleteCamera && (
                      <button
                        type="button"
                        onClick={() => onDeleteCamera(c)}
                        style={{
                          fontSize: 12,
                          padding: "2px 8px",
                          background: "transparent",
                          border: "1px solid var(--line, #ccc)",
                          borderRadius: 4,
                          cursor: "pointer",
                          color: "var(--red, #c00)",
                        }}
                      >
                        Delete
                      </button>
                    )}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
