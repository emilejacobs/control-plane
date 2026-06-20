// PRConfigPanel is the per-device Plate Recognizer config editor (issue #5).
// Reads GET /devices/{id}/pr-config and PUTs edits (region, the camera_id, and
// the webhook targets). The LPR camera RTSP URL is read-only here — it's
// resolved server-side from the cameras inventory. Saving pushes
// pr.config.update to the device; lastAppliedAt drives the "pending" hint until
// the agent ACKs.
"use client";

import { useEffect, useState } from "react";
import { useDevicePRConfig, usePutPRConfig } from "../lib/api/hooks";
import type { PRWebhook } from "../lib/api/devices";
import { PR_REGIONS } from "../lib/prRegions";
import { Pill } from "./ui/Pill";

const cellInput: React.CSSProperties = {
  fontSize: 12.5,
  padding: "3px 6px",
  border: "1px solid var(--line, #ccc)",
  borderRadius: 4,
};

export function PRConfigPanel({ deviceId }: { deviceId: string }) {
  const query = useDevicePRConfig(deviceId);
  const put = usePutPRConfig(deviceId);

  const [cameraId, setCameraId] = useState("");
  const [region, setRegion] = useState("");
  const [webhooks, setWebhooks] = useState<PRWebhook[]>([]);

  // Seed the form from the fetched config once (and on refetch after save).
  useEffect(() => {
    if (query.data) {
      setCameraId(query.data.cameraId);
      setRegion(query.data.region);
      setWebhooks(query.data.webhooks);
    }
  }, [query.data]);

  if (query.isLoading) {
    return <p className="muted" style={{ fontSize: 13, margin: 0 }}>Loading…</p>;
  }
  if (query.error) {
    return (
      <p role="alert" style={{ fontSize: 13, margin: 0, color: "var(--red, #c00)" }}>
        Failed to load Plate Recognizer config.
      </p>
    );
  }

  const data = query.data!;
  const pending = data.lastAppliedAt === null && data.cameraId !== "";
  const saveError = put.error instanceof Error ? put.error.message : null;

  const setWebhook = (i: number, patch: Partial<PRWebhook>) =>
    setWebhooks((ws) => ws.map((w, j) => (j === i ? { ...w, ...patch } : w)));
  const addWebhook = () =>
    setWebhooks((ws) => [...ws, { name: "", url: "", enabled: true, image: false, caching: false }]);
  const removeWebhook = (i: number) => setWebhooks((ws) => ws.filter((_, j) => j !== i));

  const onSave = () => put.mutate({ cameraId, region, webhooks });

  return (
    <div style={{ fontSize: 13 }}>
      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 10 }}>
        <div>
          {pending && (
            <>
              <Pill tone="amber">Pending</Pill>
              <span className="muted" style={{ marginLeft: 8, fontSize: 12.5 }}>
                Waiting for the device to ACK the latest change.
              </span>
            </>
          )}
        </div>
        <button type="button" className="btn" onClick={onSave} disabled={put.isPending}>
          {put.isPending ? "Saving…" : "Save"}
        </button>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "120px 1fr", gap: "8px 12px", alignItems: "center", marginBottom: 14 }}>
        <label htmlFor="pr-region" className="muted">Region</label>
        <select
          id="pr-region"
          value={region}
          onChange={(e) => setRegion(e.target.value)}
          style={{ ...cellInput, maxWidth: 260 }}
        >
          {/* Placeholder until a region is chosen (validation rejects empty). */}
          {region === "" && <option value="">— select region —</option>}
          {/* Preserve a current value that isn't in the known list (e.g. a
              non-US code) so editing doesn't silently drop it. */}
          {region !== "" && !PR_REGIONS.some((r) => r.code === region) && (
            <option value={region}>{region}</option>
          )}
          {PR_REGIONS.map((r) => (
            <option key={r.code} value={r.code}>{r.label}</option>
          ))}
        </select>

        <label htmlFor="pr-camera-id" className="muted">Camera ID</label>
        <input
          id="pr-camera-id"
          value={cameraId}
          onChange={(e) => setCameraId(e.target.value)}
          placeholder="PR camera id (e.g. 66_3)"
          style={{ ...cellInput, maxWidth: 220 }}
        />

        <span className="muted">LPR camera URL</span>
        <span className="mono" style={{ fontSize: 11.5, color: "var(--ink-2)", wordBreak: "break-all" }}>
          {data.lprCameraRtspUrl || <span className="muted">— no LPR camera set in inventory —</span>}
        </span>
      </div>

      <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 6 }}>
        <strong style={{ fontSize: 12.5 }}>Webhooks</strong>
        <button type="button" onClick={addWebhook}
          style={{ fontSize: 12, padding: "2px 8px", background: "transparent", border: "1px solid var(--line, #ccc)", borderRadius: 4, cursor: "pointer" }}>
          + Add webhook
        </button>
      </div>
      {webhooks.length === 0 ? (
        <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>No webhooks configured.</p>
      ) : (
        <table style={{ width: "100%", borderCollapse: "collapse", fontSize: 12.5 }}>
          <thead>
            <tr style={{ textAlign: "left", color: "var(--ink-2)" }}>
              <th style={{ padding: "4px 6px" }}>Name</th>
              <th style={{ padding: "4px 6px" }}>URL</th>
              <th style={{ padding: "4px 6px", width: 70 }}>Enabled</th>
              <th style={{ padding: "4px 6px", width: 60 }}>Image</th>
              <th style={{ padding: "4px 6px", width: 70 }}>Caching</th>
              <th style={{ padding: "4px 6px", width: 30 }}></th>
            </tr>
          </thead>
          <tbody>
            {webhooks.map((w, i) => (
              <tr key={i} style={{ borderTop: "1px solid var(--line)" }}>
                <td style={{ padding: "4px 6px" }}>
                  <input aria-label={`webhook ${i} name`} value={w.name}
                    onChange={(e) => setWebhook(i, { name: e.target.value })} style={{ ...cellInput, width: "100%" }} />
                </td>
                <td style={{ padding: "4px 6px" }}>
                  <input aria-label={`webhook ${i} url`} value={w.url}
                    onChange={(e) => setWebhook(i, { url: e.target.value })} style={{ ...cellInput, width: "100%" }} />
                </td>
                <td style={{ padding: "4px 6px", textAlign: "center" }}>
                  <input type="checkbox" aria-label={`webhook ${i} enabled`} checked={w.enabled}
                    onChange={(e) => setWebhook(i, { enabled: e.target.checked })} />
                </td>
                <td style={{ padding: "4px 6px", textAlign: "center" }}>
                  <input type="checkbox" aria-label={`webhook ${i} image`} checked={w.image}
                    onChange={(e) => setWebhook(i, { image: e.target.checked })} />
                </td>
                <td style={{ padding: "4px 6px", textAlign: "center" }}>
                  <input type="checkbox" aria-label={`webhook ${i} caching`} checked={w.caching}
                    onChange={(e) => setWebhook(i, { caching: e.target.checked })} />
                </td>
                <td style={{ padding: "4px 6px", textAlign: "right" }}>
                  <button type="button" aria-label={`remove webhook ${i}`} onClick={() => removeWebhook(i)}
                    style={{ fontSize: 12, padding: "2px 6px", background: "transparent", border: "1px solid var(--line, #ccc)", borderRadius: 4, cursor: "pointer", color: "var(--red, #c00)" }}>
                    ✕
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {saveError && (
        <p role="alert" style={{ fontSize: 12.5, marginTop: 10, color: "var(--red, #c00)" }}>
          {saveError}
        </p>
      )}
    </div>
  );
}
