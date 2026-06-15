// Captures read + snapshot-trigger calls against cp-api (issue #8, ADR-030 § 7).
// The dashboard never proxies the bytes — it lists capture rows, then fetches a
// short-lived signed S3 URL per capture for the browser to load directly.
import { apiRequest } from "./client";
import { ApiError } from "./auth";

export type CaptureKind = "snapshot" | "audio" | "transcript";

export interface Capture {
  id: string;
  kind: CaptureKind;
  contentType: string;
  sizeBytes: number;
  // metadata is the per-kind payload; snapshots carry { camera_id }.
  metadata: Record<string, unknown>;
  createdAt: string; // RFC3339
}

interface CaptureWire {
  id: string;
  kind: CaptureKind;
  content_type: string;
  size_bytes: number;
  metadata: Record<string, unknown> | null;
  created_at: string;
}

// getDeviceCaptures lists a device's captures newest-first (site-scoped server
// side). An empty kind returns all kinds; pass "snapshot" for the camera grid.
export async function getDeviceCaptures(deviceId: string, kind?: CaptureKind): Promise<Capture[]> {
  const q = kind ? `?kind=${encodeURIComponent(kind)}` : "";
  const res = await apiRequest(`/devices/${deviceId}/captures${q}`);
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load captures");
  }
  const body = (await res.json()) as { captures: CaptureWire[] };
  return (body.captures ?? []).map((c) => ({
    id: c.id,
    kind: c.kind,
    contentType: c.content_type,
    sizeBytes: c.size_bytes,
    metadata: c.metadata ?? {},
    createdAt: c.created_at,
  }));
}

// getCaptureUrl fetches a short-lived signed download URL for one capture. The
// URL is what an <img>/<audio> src points at — it expires (~5 min) so it's
// re-fetched on staleness rather than cached forever.
export async function getCaptureUrl(captureId: string): Promise<string> {
  const res = await apiRequest(`/captures/${captureId}/url`);
  if (!res.ok) {
    throw new ApiError(res.status, "failed to load capture URL");
  }
  const body = (await res.json()) as { url: string };
  return body.url;
}

export interface SnapshotResult {
  correlationId: string;
  s3Key: string;
}

// requestSnapshot triggers an on-demand camera snapshot (POST
// /devices/{id}/snapshot). cp-api presigns + pushes camera.snapshot to the
// agent; the new row appears on the next captures poll.
export async function requestSnapshot(deviceId: string, cameraId: string): Promise<SnapshotResult> {
  const res = await apiRequest(`/devices/${deviceId}/snapshot`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ camera_id: cameraId }),
  });
  if (!res.ok) {
    throw new ApiError(res.status, "failed to request snapshot");
  }
  const body = (await res.json()) as { correlation_id: string; s3_key: string };
  return { correlationId: body.correlation_id, s3Key: body.s3_key };
}
