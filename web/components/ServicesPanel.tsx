// ServicesPanel renders the per-device service-status table for the
// per-device view (Phase 2, Issue 01). Reads directly from the
// device.services array on the GET /devices/{id} response — no separate
// fetch, so it ticks on the same 10-second poll the rest of the page
// uses.
//
// Visual contract: name (mono) | state pill | "running since N hours"
// (relative to lastReported, mirroring formatAgo's shape). Empty array
// renders a "no report yet" placeholder rather than an empty table so
// operators can distinguish "device isn't reporting" from "device has
// no allow-listed services" at a glance.
import type { DeviceService } from "../lib/api/devices";
import { Pill, type PillTone } from "./ui/Pill";
import { formatAgo } from "../lib/ago";

interface Props {
  services: DeviceService[];
  now: Date;
}

function toneFor(state: DeviceService["state"]): PillTone {
  switch (state) {
    case "running":
      return "green";
    case "stopped":
      return "red";
    case "unknown":
    default:
      return "neutral";
  }
}

export function ServicesPanel({ services, now }: Props) {
  if (services.length === 0) {
    return (
      <p className="muted" style={{ fontSize: 13, margin: 0 }}>
        No service-status report yet. Reports arrive every 5 minutes from
        agents with a configured allow-list.
      </p>
    );
  }
  return (
    <table
      className="services"
      style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}
    >
      <thead>
        <tr style={{ textAlign: "left", color: "var(--ink-2)" }}>
          <th style={{ padding: "6px 8px" }}>Service</th>
          <th style={{ padding: "6px 8px" }}>State</th>
          <th style={{ padding: "6px 8px" }}>Since</th>
        </tr>
      </thead>
      <tbody>
        {services.map((s) => (
          <tr key={s.name} style={{ borderTop: "1px solid var(--line)" }}>
            <td className="mono" style={{ padding: "6px 8px" }}>
              {s.name}
            </td>
            <td style={{ padding: "6px 8px" }}>
              <Pill tone={toneFor(s.state)}>{s.state}</Pill>
            </td>
            <td className="muted" style={{ padding: "6px 8px" }}>
              <time>{formatAgo(new Date(s.stateSince), now)}</time>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
