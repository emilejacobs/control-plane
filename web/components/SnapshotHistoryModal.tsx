"use client";

import { useMemo, useState } from "react";
import { useDeviceSnapshots, useCaptureUrl } from "../lib/api/hooks";
import type { Capture } from "../lib/api/captures";
import { SnapshotLightbox } from "./SnapshotLightbox";

// SnapshotHistoryModal lists a camera's snapshots over time (#9): a paginated,
// newest-first list, each row a timestamp + thumbnail; clicking a row opens the
// full-size lightbox. Opened by clicking the camera's thumbnail — it supersedes
// the single-shot lightbox.
interface Props {
  deviceId: string;
  cameraId: string;
  onClose: () => void;
}

const PAGE_SIZE = 8;

export function SnapshotHistoryModal({ deviceId, cameraId, onClose }: Props) {
  const snapshots = useDeviceSnapshots(deviceId);
  const rows = useMemo(
    () => (snapshots.data ?? []).filter((c) => c.metadata?.camera_id === cameraId),
    [snapshots.data, cameraId],
  );
  const [page, setPage] = useState(0);
  const [zoomed, setZoomed] = useState<Capture | null>(null);

  const pageCount = Math.max(1, Math.ceil(rows.length / PAGE_SIZE));
  const clampedPage = Math.min(page, pageCount - 1);
  const pageRows = rows.slice(clampedPage * PAGE_SIZE, clampedPage * PAGE_SIZE + PAGE_SIZE);

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={`Snapshot history for ${cameraId}`}
      onClick={onClose}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,0.45)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 1000,
        padding: 24,
      }}
    >
      <div
        onClick={(e) => e.stopPropagation()}
        style={{
          background: "var(--bg, white)",
          borderRadius: 6,
          padding: 20,
          width: "min(560px, calc(100% - 32px))",
          maxHeight: "calc(100% - 64px)",
          overflow: "auto",
          boxShadow: "0 12px 32px rgba(0,0,0,0.2)",
        }}
      >
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: 12 }}>
          <h2 style={{ margin: 0, fontSize: 16 }}>Snapshot history</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            style={{ background: "transparent", border: "1px solid var(--line, #ccc)", borderRadius: 4, padding: "4px 10px", fontSize: 12, cursor: "pointer" }}
          >
            Close
          </button>
        </div>

        {snapshots.isPending ? (
          <p className="muted" style={{ fontSize: 13 }}>Loading…</p>
        ) : rows.length === 0 ? (
          <p className="muted" style={{ fontSize: 13 }}>No snapshots yet for this camera.</p>
        ) : (
          <>
            <ul style={{ listStyle: "none", margin: 0, padding: 0, display: "flex", flexDirection: "column", gap: 6 }}>
              {pageRows.map((c) => (
                <HistoryRow key={c.id} deviceId={deviceId} capture={c} onOpen={() => setZoomed(c)} />
              ))}
            </ul>
            {pageCount > 1 && (
              <div className="row" style={{ gap: 10, alignItems: "center", justifyContent: "center", marginTop: 12, fontSize: 12.5 }}>
                <button type="button" disabled={clampedPage === 0} onClick={() => setPage(clampedPage - 1)} className="btn">
                  Prev
                </button>
                <span className="muted">
                  Page {clampedPage + 1} of {pageCount}
                </span>
                <button type="button" disabled={clampedPage >= pageCount - 1} onClick={() => setPage(clampedPage + 1)} className="btn">
                  Next
                </button>
              </div>
            )}
          </>
        )}
      </div>

      {zoomed && <HistoryLightbox captureId={zoomed.id} cameraId={cameraId} onClose={() => setZoomed(null)} />}
    </div>
  );
}

// HistoryRow renders one snapshot row: its capture time + a thumbnail (signed
// URL). Clicking opens the full-size lightbox.
function HistoryRow({ deviceId, capture, onOpen }: { deviceId: string; capture: Capture; onOpen: () => void }) {
  const url = useCaptureUrl(capture.id);
  void deviceId;
  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        aria-label={`View full-size snapshot from ${formatTime(capture.createdAt)}`}
        style={{
          width: "100%",
          display: "flex",
          alignItems: "center",
          gap: 12,
          padding: 6,
          background: "transparent",
          border: "1px solid var(--line, #eee)",
          borderRadius: 4,
          cursor: "pointer",
          textAlign: "left",
        }}
      >
        <span
          style={{ width: 72, height: 40, flexShrink: 0, borderRadius: 3, overflow: "hidden", background: "var(--bg-2, #f4f4f4)", display: "inline-flex", alignItems: "center", justifyContent: "center" }}
        >
          {url.data ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={url.data} alt={`Snapshot from ${formatTime(capture.createdAt)}`} style={{ width: "100%", height: "100%", objectFit: "cover" }} />
          ) : (
            <span className="muted" style={{ fontSize: 9 }}>…</span>
          )}
        </span>
        <span style={{ fontSize: 13 }}>{formatTime(capture.createdAt)}</span>
      </button>
    </li>
  );
}

// HistoryLightbox fetches the signed URL for the selected capture and shows it
// full-size.
function HistoryLightbox({ captureId, cameraId, onClose }: { captureId: string; cameraId: string; onClose: () => void }) {
  const url = useCaptureUrl(captureId);
  if (!url.data) return null;
  return <SnapshotLightbox url={url.data} alt={`Snapshot for ${cameraId}, full size`} onClose={onClose} />;
}

function formatTime(rfc3339: string): string {
  const d = new Date(rfc3339);
  return Number.isNaN(d.getTime()) ? rfc3339 : d.toLocaleString();
}
