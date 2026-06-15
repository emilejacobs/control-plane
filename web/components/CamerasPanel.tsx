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
import { CameraSnapshot } from "./CameraSnapshot";

interface Props {
  cameras: Camera[];
  lastAppliedAt: string | null;
  // deviceId enables the per-camera snapshot thumbnail + Refresh button (#8).
  // Omitted (e.g. in isolated tests) → the snapshot column is hidden.
  deviceId?: string;
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
  // scanInFlight drives the button's immediate loading affordance (#12): the
  // label flips to "Scanning…" and the button disables while a scan runs, so
  // the click isn't a silent 20s no-op.
  scanInFlight?: boolean;
  // scanError surfaces a failed scan POST inline next to the button instead of
  // failing silently (#12).
  scanError?: string | null;
  // onVerifyAngle deep-links the operator to the on-device Edge UI's
  // /preview/<camera_id> page (issue #4, ADR-030 § 1, ADR-032). The
  // parent typically wires this to window.open(previewURL(c)). The
  // previewURL hook lets the panel render the target URL as the
  // button's title attribute, so the operator sees the destination on
  // hover and can copy-paste if the tab fails to open over the
  // tailnet.
  onVerifyAngle?: (camera: Camera) => void;
  previewURL?: (camera: Camera) => string;
  // lanURL renders the LAN-IP fallback affordance (issue #14, the
  // ADR-030 § 1 fallback hint deferred per ADR-032). When provided,
  // each camera row gets a secondary "Copy LAN URL" button whose
  // click handler writes the URL to the clipboard (the panel does
  // not own the clipboard call directly; it just renders the
  // button + title and the parent supplies the URL builder).
  // Undefined means "no LAN IP known" — the affordance is hidden
  // entirely. Devices that pre-date the issue-#14 rollout never
  // pass a lanURL.
  lanURL?: (camera: Camera) => string;
}

export function CamerasPanel({
  cameras,
  lastAppliedAt,
  deviceId,
  onAddCamera,
  onEditCamera,
  onDeleteCamera,
  onScanNetwork,
  scanInFlight,
  scanError,
  onVerifyAngle,
  previewURL,
  lanURL,
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
              disabled={scanInFlight}
              aria-busy={scanInFlight}
              style={{
                padding: "4px 10px",
                fontSize: 12.5,
                background: "transparent",
                border: "1px solid var(--line, #ccc)",
                borderRadius: 4,
                cursor: scanInFlight ? "not-allowed" : "pointer",
                opacity: scanInFlight ? 0.6 : 1,
              }}
            >
              {scanInFlight ? "Scanning…" : "Scan network"}
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
      {scanError && (
        <p
          role="alert"
          style={{
            margin: "0 0 10px 0",
            fontSize: 12.5,
            color: "var(--red, #c00)",
          }}
        >
          {scanError}
        </p>
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
              {deviceId && <th style={{ padding: "6px 8px", width: 120 }}>Snapshot</th>}
              {(onEditCamera || onDeleteCamera || onVerifyAngle || lanURL) && (
                <th style={{ padding: "6px 8px", width: 200 }}></th>
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
                {deviceId && (
                  <td style={{ padding: "6px 8px" }}>
                    <CameraSnapshot deviceId={deviceId} cameraId={c.cameraId} />
                  </td>
                )}
                {(onEditCamera || onDeleteCamera || onVerifyAngle || lanURL) && (
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
                    {onVerifyAngle && (
                      <button
                        type="button"
                        onClick={() => onVerifyAngle(c)}
                        title={previewURL ? previewURL(c) : undefined}
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
                        Verify angle
                      </button>
                    )}
                    {lanURL && (
                      <button
                        type="button"
                        onClick={() => {
                          // The clipboard call is best-effort —
                          // jsdom in tests + restrictive browser
                          // permissions in prod both throw here;
                          // swallow so the button click still
                          // round-trips for the operator's "I saw
                          // it click" feedback expectation.
                          const url = lanURL(c);
                          if (
                            typeof navigator !== "undefined" &&
                            navigator.clipboard
                          ) {
                            navigator.clipboard.writeText(url).catch(() => {});
                          }
                        }}
                        title={lanURL(c)}
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
                        Copy LAN URL
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
