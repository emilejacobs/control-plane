// HealthPanel renders the per-device fleet-health-probe snapshot (Phase 2,
// issue #19): one row per probe with a status pill (green/amber/red), the
// OS-agnostic signal token, a probe-specific detail line, and how long ago
// it was observed. Fed by GET /devices/{id}/health-probes (its own poll),
// so it ticks independently of the main device record.
//
// The probe names + states are OS-agnostic by construction (ADR-034) — this
// component never special-cases macOS vs Linux, only the probe name.
import type { HealthProbe } from "../lib/api/devices";
import { Pill, type PillTone } from "./ui/Pill";
import { formatAgo } from "../lib/ago";

interface Props {
  probes: HealthProbe[];
  now: Date;
}

function toneFor(status: HealthProbe["status"]): PillTone {
  switch (status) {
    case "green":
      return "green";
    case "yellow":
      return "amber";
    case "red":
      return "red";
    default:
      return "neutral";
  }
}

// detailLine renders the most operator-useful fact from a probe's details
// payload. Kept small and probe-aware so the table stays scannable; the
// full details object is available via the API for deeper digging.
function detailLine(p: HealthProbe): string {
  const d = p.details ?? {};
  switch (p.name) {
    case "whisper_model": {
      const variant = d.variant as string | undefined;
      const sizeMb = d.size_mb as number | undefined;
      if (variant) return sizeMb != null ? `${variant} (${sizeMb} MB)` : variant;
      if (Array.isArray(d.models)) return `${(d.models as unknown[]).length} models installed`;
      return p.state;
    }
    case "boot_sanity": {
      const boots = d.boots_last_7d as number | undefined;
      return boots != null ? `${boots} reboots / 7d` : p.state;
    }
    case "gui_session": {
      const user = d.console_user as string | undefined;
      return user ? `console: ${user}` : p.state;
    }
    case "host_net_pressure": {
      const pct = d.ephemeral_pct as number | undefined;
      const closeWait = d.close_wait as number | undefined;
      if (pct == null) return p.state;
      const parts = [`ephemeral ports: ${pct.toFixed(1)}% of pool`];
      if (closeWait != null) parts.push(`CLOSE_WAIT: ${closeWait}`);
      return parts.join(" · ");
    }
    default:
      return p.state;
  }
}

export function HealthPanel({ probes, now }: Props) {
  if (probes.length === 0) {
    return (
      <p className="muted" style={{ fontSize: 13, margin: 0 }}>
        No health-probe report yet. Reports arrive every 5 minutes from the
        device agent.
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
          <th style={{ padding: "6px 8px" }}>Probe</th>
          <th style={{ padding: "6px 8px" }}>Status</th>
          <th style={{ padding: "6px 8px" }}>Detail</th>
          <th style={{ padding: "6px 8px" }}>Observed</th>
        </tr>
      </thead>
      <tbody>
        {probes.map((p) => (
          <tr key={p.name} style={{ borderTop: "1px solid var(--line)" }}>
            <td className="mono" style={{ padding: "6px 8px" }}>
              {p.name}
            </td>
            <td style={{ padding: "6px 8px" }}>
              <Pill tone={toneFor(p.status)}>{p.state}</Pill>
            </td>
            <td className="muted" style={{ padding: "6px 8px" }}>
              {detailLine(p)}
            </td>
            <td className="muted" style={{ padding: "6px 8px" }}>
              <time>{formatAgo(new Date(p.lastObservedAt), now)}</time>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
