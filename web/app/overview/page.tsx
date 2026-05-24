"use client";

import { useMemo } from "react";
import Link from "next/link";
import { useDevices } from "../../lib/api/hooks";
import { groupDevices } from "../../lib/fleet";
import { Topbar } from "../../components/ui/Topbar";
import { Card } from "../../components/ui/Card";
import { Dot } from "../../components/ui/Dot";
import { Pill } from "../../components/ui/Pill";
import { Placeholder } from "../../components/ui/Placeholder";
import { RequireAuth } from "../../components/RequireAuth";

// OverviewPage — fleet health at a glance.
//
// KPIs that draw from real data (GET /devices summary): Online ratio,
// site + client counts. KPIs that require fields the summary endpoint
// does not yet carry (cert expiry per device, agent version drift, 24h
// activity, event stream) render as Phase-2 placeholders rather than
// fake numbers — see the bundle integration decision: "Build from
// useDevices() only; everything else is a Phase 2 callout."
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
    return {
      total: list.length,
      online,
      offline: list.length - online,
      sites: sites.size,
      clients: clients.size,
    };
  }, [devices.data]);

  const offlineLongest = useMemo(() => {
    return (devices.data ?? [])
      .filter((d) => !d.isOnline)
      .slice(0, 5);
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
                <div className="stat-value muted">—</div>
                <div className="stat-sub muted">
                  Lands when GET /devices summary carries cert data · Phase 2
                </div>
              </div>

              <div className="stat">
                <div className="stat-label">Agent version drift</div>
                <div className="stat-value muted">—</div>
                <div className="stat-sub muted">
                  Lands when GET /devices summary carries agent_version · Phase 2
                </div>
              </div>

              <div className="stat">
                <div className="stat-label">Activity (24h)</div>
                <div className="stat-value muted">—</div>
                <div className="stat-sub muted">
                  Heartbeat metrics land with the events surface · Phase 2
                </div>
              </div>
            </div>

            <div className="overview-row">
              <Card label="Recent events" flush>
                <div style={{ padding: "8px 20px 14px" }}>
                  <Placeholder
                    label="EVENT STREAM · Phase 2"
                    height={140}
                  />
                  <p
                    className="muted"
                    style={{ fontSize: 12.5, marginTop: 10 }}
                  >
                    Heartbeats, cert rotations, operator actions, and service
                    state changes will land here as the events surface comes
                    online.
                  </p>
                </div>
              </Card>

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
                  <Placeholder
                    label="CERT EXPIRY ROLLUP · Phase 2"
                    height={64}
                  />
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
