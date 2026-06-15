"use client";

import { useMemo } from "react";
import { useDeviceSnapshots, useCaptureUrl, useRequestSnapshot } from "../lib/api/hooks";

// CameraSnapshot renders one camera's latest snapshot thumbnail plus a
// "Refresh snapshot" button (issue #8, ADR-030 § 7). It is self-contained: it
// reads the device's snapshot captures (shared react-query cache across cameras),
// picks the newest whose metadata.camera_id matches, resolves a signed URL for
// it, and triggers a fresh capture on demand. A new snapshot surfaces on the
// next captures poll after the agent ACKs.
interface Props {
  deviceId: string;
  cameraId: string;
}

export function CameraSnapshot({ deviceId, cameraId }: Props) {
  const snapshots = useDeviceSnapshots(deviceId);
  const refresh = useRequestSnapshot(deviceId);

  // The list is newest-first, so the first match for this camera is the latest.
  const latest = useMemo(
    () => (snapshots.data ?? []).find((c) => c.metadata?.camera_id === cameraId),
    [snapshots.data, cameraId],
  );
  const url = useCaptureUrl(latest?.id ?? null);

  const capturing = refresh.isPending;
  const errorMessage = refresh.error instanceof Error ? refresh.error.message : null;

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 4, alignItems: "flex-start" }}>
      <div
        style={{
          width: 96,
          height: 54,
          borderRadius: 4,
          border: "1px solid var(--line, #ccc)",
          background: "var(--bg-2, #f4f4f4)",
          overflow: "hidden",
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
        }}
      >
        {latest && url.data ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={url.data}
            alt={`Latest snapshot for ${cameraId}`}
            style={{ width: "100%", height: "100%", objectFit: "cover" }}
          />
        ) : (
          <span className="muted" style={{ fontSize: 10.5, textAlign: "center", padding: 4 }}>
            {snapshots.isPending ? "Loading…" : "No snapshot yet"}
          </span>
        )}
      </div>

      <button
        type="button"
        onClick={() => refresh.mutate(cameraId)}
        disabled={capturing}
        aria-label={`Refresh snapshot for ${cameraId}`}
        style={{
          fontSize: 11.5,
          padding: "2px 8px",
          background: "transparent",
          border: "1px solid var(--line, #ccc)",
          borderRadius: 4,
          cursor: capturing ? "not-allowed" : "pointer",
          opacity: capturing ? 0.6 : 1,
        }}
      >
        {capturing ? "Capturing…" : "Refresh snapshot"}
      </button>

      {errorMessage && (
        <span role="alert" style={{ fontSize: 10.5, color: "var(--red, #c00)" }}>
          {errorMessage}
        </span>
      )}
    </div>
  );
}
