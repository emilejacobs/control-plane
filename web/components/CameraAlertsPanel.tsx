// CameraAlertsPanel — the Overview's fleet-wide offline-camera overview (#152).
// Lists currently-offline cameras (longest outage first) with their site and
// how long they've been down; a count badge + an escalating dot/duration colour
// (amber recent → red ≥24h) make the worst outages pop. Empty + error states
// keep an absent list unambiguous rather than looking broken.
"use client";

import Link from "next/link";
import { useFleetCameras } from "../lib/api/hooks";
import { Card } from "./ui/Card";
import { Pill } from "./ui/Pill";
import { Dot } from "./ui/Dot";
import { formatAgo } from "../lib/ago";

const LONG_OUTAGE_MS = 24 * 60 * 60 * 1000;

function isLong(statusChangedAt: string | null, now: Date): boolean {
  if (!statusChangedAt) return false;
  return now.getTime() - new Date(statusChangedAt).getTime() >= LONG_OUTAGE_MS;
}

export function CameraAlertsPanel() {
  const query = useFleetCameras();
  // Recomputed each render; the 10s poll re-renders so the durations tick.
  const now = new Date();
  const data = query.data;

  const anyLong = !!data && data.cameras.some((c) => isLong(c.statusChangedAt, now));
  const badge =
    data && data.offline > 0 ? (
      <Pill tone={anyLong ? "red" : "amber"}>{data.offline} offline</Pill>
    ) : null;

  return (
    <Card label="Camera alerts" actions={badge} flush>
      <div style={{ padding: "8px 20px 12px" }}>
        {query.isPending && (
          <p className="muted" style={{ fontSize: 13, margin: "8px 0" }}>Loading…</p>
        )}
        {query.error && (
          <p role="alert" style={{ fontSize: 13, margin: "8px 0", color: "var(--red-ink)" }}>
            Failed to load camera status.
          </p>
        )}
        {data && data.offline === 0 && (
          <div className="muted" style={{ fontSize: 13, padding: "10px 0" }}>
            {data.total === 0 ? "No cameras in inventory." : `All ${data.total} cameras online.`}
          </div>
        )}
        {data &&
          data.cameras.map((c) => {
            const long = isLong(c.statusChangedAt, now);
            return (
              <div className="list-row" key={c.cameraId}>
                <Dot tone={long ? "red" : "amber"} />
                <Link
                  href={`/devices/${c.deviceId}`}
                  className="hostname"
                  style={{ color: "var(--ink)", textDecoration: "none" }}
                >
                  {c.label}
                </Link>
                <span className="meta muted">{c.siteName ?? "Unassigned"}</span>
                <span
                  style={{
                    marginLeft: "auto",
                    fontSize: 12,
                    color: long ? "var(--red-ink)" : "var(--amber-ink)",
                  }}
                >
                  {c.statusChangedAt ? formatAgo(new Date(c.statusChangedAt), now) : "offline"}
                </span>
              </div>
            );
          })}

        {data && data.total > 0 && (
          <div
            style={{
              marginTop: 11,
              paddingTop: 9,
              borderTop: "1px solid var(--line)",
              fontSize: 12,
            }}
          >
            <Link href="/devices" style={{ color: "var(--ink-3)", textDecoration: "none" }}>
              View all {data.total} cameras &rarr;
            </Link>
          </div>
        )}
      </div>
    </Card>
  );
}
