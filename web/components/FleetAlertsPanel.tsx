// FleetAlertsPanel renders the Overview alerts roll-up (#21): one expandable
// row per probe (red/yellow device counts) and per stopped service. It is
// alert-only — with no alerts it renders nothing, so the Overview page stays
// quiet when the fleet is healthy. Expanding a row drills down to the
// affected devices, joined against the fleet list (already polled by the
// page) for hostname + site, with a link to each device's detail page.
"use client";

import { useMemo, useState } from "react";
import type { FleetAlerts } from "../lib/api/fleet";
import type { DeviceSummary } from "../lib/api/devices";
import { Card } from "./ui/Card";
import { Pill } from "./ui/Pill";
import { Dot, type DotTone } from "./ui/Dot";

interface Props {
  alerts: FleetAlerts;
  devices: DeviceSummary[];
}

interface Affected {
  id: string;
  tone: Extract<DotTone, "red" | "amber">;
}

export function FleetAlertsPanel({ alerts, devices }: Props) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const byId = useMemo(
    () => new Map(devices.map((d) => [d.deviceId, d])),
    [devices],
  );

  if (alerts.probes.length === 0 && alerts.services.length === 0) {
    return null;
  }

  function toggle(key: string) {
    setExpanded((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  }

  function drilldown(affected: Affected[]) {
    return (
      <div style={{ padding: "2px 0 8px 18px" }}>
        {affected.map(({ id, tone }) => {
          const d = byId.get(id);
          return (
            <div className="list-row" key={id}>
              <Dot tone={tone} />
              <a
                href={`/devices/${id}`}
                className="hostname"
                style={{ color: "var(--ink)", textDecoration: "none" }}
              >
                {d ? d.hostname : id}
              </a>
              <span className="meta muted">{d?.siteName ?? "Unassigned"}</span>
            </div>
          );
        })}
      </div>
    );
  }

  function alertRow(key: string, name: string, pills: React.ReactNode, affected: Affected[]) {
    const open = expanded.has(key);
    return (
      <div key={key}>
        <button
          type="button"
          className="row alert-row"
          aria-expanded={open}
          onClick={() => toggle(key)}
          style={{
            width: "100%",
            gap: 8,
            padding: "8px 4px",
            background: "none",
            border: "none",
            borderTop: "1px solid var(--line)",
            cursor: "pointer",
            textAlign: "left",
          }}
        >
          <span className="mono">{name}</span>
          <div className="spacer" />
          {pills}
        </button>
        {open && drilldown(affected)}
      </div>
    );
  }

  return (
    <Card label="Fleet alerts" flush>
      <div style={{ padding: "8px 20px 12px" }}>
        {alerts.probes.length > 0 && (
          <>
            <div className="card-section-label" style={{ marginTop: 4, marginBottom: 4 }}>
              Health probes
            </div>
            {alerts.probes.map((p) =>
              alertRow(
                `probe:${p.probeName}`,
                p.probeName,
                <>
                  {p.red.length > 0 && <Pill tone="red">{p.red.length} red</Pill>}
                  {p.yellow.length > 0 && <Pill tone="amber">{p.yellow.length} yellow</Pill>}
                </>,
                [
                  ...p.red.map((id) => ({ id, tone: "red" as const })),
                  ...p.yellow.map((id) => ({ id, tone: "amber" as const })),
                ],
              ),
            )}
          </>
        )}

        {alerts.services.length > 0 && (
          <>
            <div className="card-section-label" style={{ marginTop: 18, marginBottom: 4 }}>
              Services
            </div>
            {alerts.services.map((s) =>
              alertRow(
                `service:${s.serviceName}`,
                s.serviceName,
                <Pill tone="red">{s.stopped.length} stopped</Pill>,
                s.stopped.map((id) => ({ id, tone: "red" as const })),
              ),
            )}
          </>
        )}
      </div>
    </Card>
  );
}
