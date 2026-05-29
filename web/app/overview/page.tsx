"use client";

import { useMemo } from "react";
import Link from "next/link";
import { useDevices, useFleetAlerts } from "../../lib/api/hooks";
import { groupDevices } from "../../lib/fleet";
import { Topbar } from "../../components/ui/Topbar";
import { Card } from "../../components/ui/Card";
import { Dot } from "../../components/ui/Dot";
import { Pill } from "../../components/ui/Pill";
import { FleetAlertsPanel } from "../../components/FleetAlertsPanel";
import { RequireAuth } from "../../components/RequireAuth";

// OverviewPage — fleet health at a glance.
//
// All KPIs draw from GET /devices summary: online ratio, site/client
// counts, cert-expiring-≤30d count, agent-version-drift count, plus
// the offline + cert-expiring-soonest rollups in the "Needs attention"
// panel. A fleet-wide event stream / 24h activity rollup was
// considered and dropped (low signal: alarms already page the
// actionable things; a firehose of heartbeat events dilutes attention).
//
// The /overview route's auth gate lives on this component. OverviewBody
// is the unguarded body, which app/page.tsx (the root /) reuses behind
// its own composite first-run + token gate — wrapping the body twice
// would race the root's /first-run redirect.
export default function OverviewPage() {
  return (
    <RequireAuth>
      <OverviewBody />
    </RequireAuth>
  );
}

export function OverviewBody() {
  const devices = useDevices();
  const fleetAlerts = useFleetAlerts();

  const stats = useMemo(() => {
    const list = devices.data ?? [];
    const online = list.filter((d) => d.isOnline).length;
    const sites = new Set<string>();
    const clients = new Set<string>();
    for (const d of list) {
      if (d.siteName && d.clientName) {
        sites.add(`${d.clientName}//${d.siteName}`);
      }
      if (d.clientName) clients.add(d.clientName);
    }
    // Phase 2 Chain A: cert + agent_version aggregates. certExpiring is
    // the count of devices whose cert is ≤ 30d out (or already expired).
    // agentVersionCount is the distinct-version count across the fleet —
    // 1 means everyone's on the same build, >1 means drift to call out.
    const certExpiring = list.filter(
      (d) => d.certDaysRemaining != null && d.certDaysRemaining <= 30,
    ).length;
    const versions = new Set<string>();
    for (const d of list) if (d.agentVersion) versions.add(d.agentVersion);
    return {
      total: list.length,
      online,
      offline: list.length - online,
      sites: sites.size,
      clients: clients.size,
      certExpiring,
      agentVersionCount: versions.size,
    };
  }, [devices.data]);

  const offlineLongest = useMemo(() => {
    return (devices.data ?? [])
      .filter((d) => !d.isOnline)
      .slice(0, 5);
  }, [devices.data]);

  // Phase 2 Chain A: top 3 devices by soonest cert expiry — surfaces in
  // the "Needs attention" panel so an operator sees what to rotate next.
  const certExpiringSoonest = useMemo(() => {
    return (devices.data ?? [])
      .filter((d) => d.certDaysRemaining != null)
      .sort((a, b) => (a.certDaysRemaining ?? 0) - (b.certDaysRemaining ?? 0))
      .slice(0, 3);
  }, [devices.data]);

  const groups = useMemo(() => groupDevices(devices.data ?? []), [devices.data]);

  return (
    <>
      <Topbar />
      <main className="page">
        <div className="page-header">
          <div>
            <div className="crumbs">
              <span>uknomi</span>
              <span className="sep">/</span>
              <span style={{ color: "var(--ink)" }}>Overview</span>
            </div>
            <h1 className="page-title">Fleet overview</h1>
            <p className="page-subtitle">
              {devices.data
                ? `${stats.total} devices across ${stats.clients} clients · ${stats.sites} sites · polling every 10s`
                : "Polling every 10s"}
            </p>
          </div>
          <div className="page-actions">
            <Link className="btn primary" href="/devices">
              Open fleet
            </Link>
          </div>
        </div>

        {devices.isPending && (
          <div role="status" className="muted" style={{ padding: 12 }}>
            Loading fleet…
          </div>
        )}

        {devices.isError && (
          <Card>
            <div role="alert" className="row" style={{ gap: 12 }}>
              <p style={{ margin: 0 }}>Could not load the fleet.</p>
              <button className="btn" onClick={() => devices.refetch()}>
                Refresh
              </button>
            </div>
          </Card>
        )}

        {devices.data && (
          <>
            <div className="stat-grid">
              <div className="stat">
                <div className="stat-label">Online</div>
                <div className="stat-value">
                  {stats.online}
                  <span
                    style={{
                      color: "var(--ink-3)",
                      fontSize: 16,
                      fontWeight: 500,
                    }}
                  >
                    {" "}
                    / {stats.total}
                  </span>
                </div>
                <div className="stat-sub">
                  {stats.total > 0
                    ? `${Math.round((stats.online / stats.total) * 100)}% available · ${stats.offline} offline`
                    : "No devices enrolled"}
                </div>
              </div>

              <div className="stat">
                <div className="stat-label">Cert expiring &le; 30d</div>
                <div className="stat-value">{stats.certExpiring}</div>
                <div className="stat-sub">
                  {stats.certExpiring === 0
                    ? "All certs > 30 days out"
                    : `${stats.certExpiring} of ${stats.total} need rotation soon`}
                </div>
              </div>

              <div className="stat">
                <div className="stat-label">Agent version drift</div>
                <div className="stat-value">{stats.agentVersionCount}</div>
                <div className="stat-sub">
                  {stats.agentVersionCount <= 1
                    ? "Fleet on one version"
                    : `${stats.agentVersionCount} distinct versions in fleet`}
                </div>
              </div>

            </div>

            {fleetAlerts.data &&
              (fleetAlerts.data.probes.length > 0 ||
                fleetAlerts.data.services.length > 0) && (
                // Alert-only: render (and reserve spacing) only when there's
                // something to show. .overview-row.single gives the card the
                // same 16px bottom margin that separates the Needs attention
                // row from the Clients card below.
                <div className="overview-row single">
                  <FleetAlertsPanel
                    alerts={fleetAlerts.data}
                    devices={devices.data}
                  />
                </div>
              )}

            <div className="overview-row">
              <Card label="Needs attention" flush>
                <div style={{ padding: "8px 20px 12px" }}>
                  <div
                    className="card-section-label"
                    style={{ marginTop: 4, marginBottom: 4 }}
                  >
                    Offline
                  </div>
                  {offlineLongest.length === 0 ? (
                    <div
                      className="muted"
                      style={{ padding: "10px 0", fontSize: 13 }}
                    >
                      All devices online.
                    </div>
                  ) : (
                    offlineLongest.map((d) => (
                      <div className="list-row" key={d.deviceId}>
                        <Dot tone="gray" />
                        <Link
                          href={`/devices/${d.deviceId}`}
                          className="hostname"
                          style={{
                            color: "var(--ink)",
                            textDecoration: "none",
                          }}
                        >
                          {d.hostname}
                        </Link>
                        <span className="meta muted">
                          {d.siteName ?? "Unassigned"}
                        </span>
                      </div>
                    ))
                  )}

                  <div
                    className="card-section-label"
                    style={{ marginTop: 18, marginBottom: 4 }}
                  >
                    Cert expiring soonest
                  </div>
                  {certExpiringSoonest.length === 0 ? (
                    <div
                      className="muted"
                      style={{ padding: "10px 0", fontSize: 13 }}
                    >
                      No cert data yet.
                    </div>
                  ) : (
                    certExpiringSoonest.map((d) => (
                      <div className="list-row" key={d.deviceId}>
                        <Dot
                          tone={
                            (d.certDaysRemaining ?? 0) < 0
                              ? "red"
                              : (d.certDaysRemaining ?? 0) <= 30
                                ? "amber"
                                : "gray"
                          }
                        />
                        <Link
                          href={`/devices/${d.deviceId}`}
                          className="hostname"
                          style={{
                            color: "var(--ink)",
                            textDecoration: "none",
                          }}
                        >
                          {d.hostname}
                        </Link>
                        <span className="meta muted">
                          {(d.certDaysRemaining ?? 0) < 0
                            ? `expired ${Math.abs(d.certDaysRemaining ?? 0)}d ago`
                            : `${d.certDaysRemaining}d left`}
                        </span>
                      </div>
                    ))
                  )}
                </div>
              </Card>
            </div>

            <Card
              label="Clients"
              flush
              actions={
                <Link className="btn ghost small" href="/devices">
                  Open fleet →
                </Link>
              }
            >
              {groups.length === 0 ? (
                <div className="empty">
                  <div className="title">No clients yet</div>
                  <div>Devices appear here once they enroll.</div>
                </div>
              ) : (
                <table className="table">
                  <thead>
                    <tr>
                      <th>Client</th>
                      <th>Sites</th>
                      <th>Devices</th>
                      <th>Online</th>
                    </tr>
                  </thead>
                  <tbody>
                    {groups.map((c) => {
                      const all = c.sites.flatMap((s) => s.devices);
                      const online = all.filter((d) => d.isOnline).length;
                      return (
                        <tr key={c.clientName}>
                          <td style={{ fontWeight: 600 }}>{c.clientName}</td>
                          <td>{c.sites.length}</td>
                          <td className="tabular">{all.length}</td>
                          <td>
                            <span className="row" style={{ gap: 6 }}>
                              <Dot
                                tone={
                                  online === all.length
                                    ? "green"
                                    : online === 0
                                      ? "gray"
                                      : "amber"
                                }
                              />
                              <span className="tabular">
                                {online}/{all.length}
                              </span>
                              {online > 0 && online < all.length && (
                                <Pill tone="amber">{all.length - online} offline</Pill>
                              )}
                            </span>
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
              )}
            </Card>
          </>
        )}
      </main>
    </>
  );
}
