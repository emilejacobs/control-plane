"use client";

import { useEffect, useMemo, useState } from "react";
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

  // Lightbox: the thumbnail is rendered tiny, but the S3 object is the full
  // image, so clicking enlarges the same signed URL. Escape closes it.
  const [zoomed, setZoomed] = useState(false);
  const hasImage = Boolean(latest && url.data);
  useEffect(() => {
    if (!zoomed) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") setZoomed(false);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [zoomed]);

  return (
    <div style={{ display: "flex", flexDirection: "column", gap: 4, alignItems: "flex-start" }}>
      {hasImage ? (
        <button
          type="button"
          onClick={() => setZoomed(true)}
          aria-label={`Enlarge snapshot for ${cameraId}`}
          title="Click to enlarge"
          style={{
            width: 96,
            height: 54,
            padding: 0,
            borderRadius: 4,
            border: "1px solid var(--line, #ccc)",
            background: "var(--bg-2, #f4f4f4)",
            overflow: "hidden",
            cursor: "zoom-in",
            display: "block",
          }}
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={url.data}
            alt={`Latest snapshot for ${cameraId}`}
            style={{ width: "100%", height: "100%", objectFit: "cover", display: "block" }}
          />
        </button>
      ) : (
        <div
          style={{
            width: 96,
            height: 54,
            borderRadius: 4,
            border: "1px solid var(--line, #ccc)",
            background: "var(--bg-2, #f4f4f4)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
          }}
        >
          <span className="muted" style={{ fontSize: 10.5, textAlign: "center", padding: 4 }}>
            {snapshots.isPending ? "Loading…" : "No snapshot yet"}
          </span>
        </div>
      )}

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

      {zoomed && url.data && (
        <div
          role="dialog"
          aria-modal="true"
          aria-label={`Snapshot for ${cameraId}`}
          onClick={() => setZoomed(false)}
          style={{
            position: "fixed",
            inset: 0,
            background: "rgba(0,0,0,0.8)",
            display: "flex",
            alignItems: "center",
            justifyContent: "center",
            zIndex: 1000,
            cursor: "zoom-out",
            padding: 24,
          }}
        >
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img
            src={url.data}
            alt={`Snapshot for ${cameraId}, full size`}
            onClick={(e) => e.stopPropagation()}
            style={{
              maxWidth: "92vw",
              maxHeight: "92vh",
              objectFit: "contain",
              borderRadius: 4,
              boxShadow: "0 12px 40px rgba(0,0,0,0.5)",
              cursor: "default",
            }}
          />
          <button
            type="button"
            onClick={() => setZoomed(false)}
            aria-label="Close"
            style={{
              position: "fixed",
              top: 16,
              right: 16,
              background: "rgba(0,0,0,0.5)",
              color: "#fff",
              border: "1px solid rgba(255,255,255,0.4)",
              borderRadius: 4,
              padding: "6px 12px",
              fontSize: 13,
              cursor: "pointer",
            }}
          >
            Close
          </button>
        </div>
      )}
    </div>
  );
}
