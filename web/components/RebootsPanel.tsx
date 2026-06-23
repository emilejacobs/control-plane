// RebootsPanel renders a device's recent reboot history (#157/#159): one row
// per distinct boot CP observed, with the previous-shutdown cause and how long
// ago CP detected it. Fed inline from the device record's recentReboots, so it
// ticks with the main device poll. A too-frequently-rebooting device shows a
// dense list here — the signal operators are after.
import type { DeviceReboot } from "../lib/api/devices";
import { Pill, type PillTone } from "./ui/Pill";
import { formatAgo } from "../lib/ago";

interface Props {
  reboots: DeviceReboot[];
  now: Date;
}

// toneFor colours the cause: a clean restart is benign (green); power loss /
// thermal / panic / forced are faults worth noticing (red); an unknown or
// missing cause is neutral.
function toneFor(cause: string | null): PillTone {
  if (!cause) return "neutral";
  if (cause === "clean restart") return "green";
  if (cause.startsWith("unknown")) return "neutral";
  return "red";
}

export function RebootsPanel({ reboots, now }: Props) {
  if (reboots.length === 0) {
    return (
      <p className="muted" style={{ fontSize: 13, margin: 0 }}>
        No reboots recorded yet. Boot info arrives once the device runs an agent
        that reports it.
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
          <th style={{ padding: "6px 8px" }}>Booted</th>
          <th style={{ padding: "6px 8px" }}>Cause</th>
          <th style={{ padding: "6px 8px" }}>Detected</th>
        </tr>
      </thead>
      <tbody>
        {reboots.map((rb) => (
          <tr key={rb.bootTime} style={{ borderTop: "1px solid var(--line)" }}>
            <td className="muted" style={{ padding: "6px 8px" }}>
              <time>{formatAgo(new Date(rb.bootTime), now)}</time>
            </td>
            <td style={{ padding: "6px 8px" }}>
              <Pill tone={toneFor(rb.shutdownCause)}>
                {rb.shutdownCause ?? "unknown"}
              </Pill>
            </td>
            <td className="muted" style={{ padding: "6px 8px" }}>
              <time>{formatAgo(new Date(rb.detectedAt), now)}</time>
            </td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}
