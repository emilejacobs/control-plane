"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { useDevices } from "../../lib/api/hooks";
import { groupDevices } from "../../lib/fleet";
import { PresenceChip } from "../../components/PresenceChip";
import { Topbar } from "../../components/ui/Topbar";
import { Card } from "../../components/ui/Card";
import { Dot } from "../../components/ui/Dot";
import { Segmented } from "../../components/ui/Segmented";
import type { DeviceSummary } from "../../lib/api/devices";

type PresenceFilter = "all" | "online" | "offline";

// DevicesPage — the fleet view. Devices land in a collapsible
// client → site → device tree polled every 10s (Issue 17). Search + the
// presence segmented filter are client-side over the in-memory list;
// cert / last_seen / agent-version per-row columns are deliberately
// omitted because GET /devices' summary payload does not carry them
// (those fields live on GET /devices/{id} — see the per-device view).
//
// State convention: collapsedClients/collapsedSites carry the *explicitly
// collapsed* ids, so the empty default = everything open on first paint.
// The tree reads as a list by default; collapse is a power-user opt-in.
//
// Test contract preserved: client headings level 2, site headings level
// 3, device rows in <li>, link name = hostname, presence chip text
// reads "Online" / "Offline".
export default function DevicesPage() {
  const devices = useDevices();
  const [query, setQuery] = useState("");
  const [presence, setPresence] = useState<PresenceFilter>("all");
  const [collapsedClients, setCollapsedClients] = useState<Set<string>>(new Set());
  const [collapsedSites, setCollapsedSites] = useState<Set<string>>(new Set());

  const filtering = query.trim() !== "" || presence !== "all";

  const filtered = useMemo(() => {
    const list = devices.data ?? [];
    const q = query.toLowerCase().trim();
    return list.filter((d) => {
      if (q) {
        const hay = `${d.hostname} ${d.clientName ?? ""} ${d.siteName ?? ""}`.toLowerCase();
        if (!hay.includes(q)) return false;
      }
      if (presence === "online" && !d.isOnline) return false;
      if (presence === "offline" && d.isOnline) return false;
      return true;
    });
  }, [devices.data, query, presence]);

  const groups = useMemo(() => groupDevices(filtered), [filtered]);

  const totals = useMemo(() => {
    const list = devices.data ?? [];
    const online = list.filter((d) => d.isOnline).length;
    return { total: list.length, online, offline: list.length - online };
  }, [devices.data]);

  function toggleClient(name: string) {
    setCollapsedClients((cur) => {
      const next = new Set(cur);
      next.has(name) ? next.delete(name) : next.add(name);
      return next;
    });
  }
  function toggleSite(key: string) {
    setCollapsedSites((cur) => {
      const next = new Set(cur);
      next.has(key) ? next.delete(key) : next.add(key);
      return next;
    });
  }
  function expandAll() {
    setCollapsedClients(new Set());
    setCollapsedSites(new Set());
  }
  function collapseAll() {
    setCollapsedClients(new Set(groups.map((g) => g.clientName)));
    setCollapsedSites(
      new Set(
        groups.flatMap((g) => g.sites.map((s) => `${g.clientName}//${s.siteName}`)),
      ),
    );
  }
  function clearFilters() {
    setQuery("");
    setPresence("all");
  }

  return (
    <>
      <Topbar />
      <main className="page">
        <div className="page-header">
          <div>
            <div className="crumbs">
              <span>uknomi</span>
              <span className="sep">/</span>
              <span style={{ color: "var(--ink)" }}>Fleet</span>
            </div>
            <h1 className="page-title">Devices</h1>
            <p className="page-subtitle">
              {devices.data
                ? `${filtered.length} of ${totals.total} devices · polling every 10s`
                : "Polling every 10s"}
            </p>
          </div>
          <div className="page-actions">
            <button className="btn" onClick={collapseAll}>
              Collapse all
            </button>
            <button className="btn" onClick={expandAll}>
              Expand all
            </button>
          </div>
        </div>

        {devices.isPending && (
          <div role="status" className="muted" style={{ padding: 12 }}>
            Loading devices…
          </div>
        )}

        {devices.isError && (
          <Card>
            <div role="alert" className="row" style={{ gap: 12 }}>
              <p style={{ margin: 0 }}>Could not load devices.</p>
              <button className="btn" onClick={() => devices.refetch()}>
                Refresh
              </button>
            </div>
          </Card>
        )}

        {devices.data && (
          <>
            <div className="card" style={{ marginBottom: 16, padding: 14 }}>
              <div className="row wrap" style={{ gap: 10 }}>
                <input
                  className="input search"
                  placeholder="Search by hostname, client, or site…"
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  style={{ maxWidth: 360 }}
                />
                <div className="row" style={{ gap: 8 }}>
                  <span className="muted" style={{ fontSize: 12 }}>
                    Presence
                  </span>
                  <Segmented<PresenceFilter>
                    value={presence}
                    onChange={setPresence}
                    options={[
                      { value: "all", label: "All", badge: totals.total },
                      { value: "online", label: "Online", badge: totals.online },
                      { value: "offline", label: "Offline", badge: totals.offline },
                    ]}
                  />
                </div>
                <div className="spacer" />
                {filtering && (
                  <button className="btn ghost small" onClick={clearFilters}>
                    Clear filters
                  </button>
                )}
              </div>
            </div>

            {totals.total === 0 && (
              <Card>
                <div className="empty">
                  <div className="title">No devices yet</div>
                  <div>
                    None are enrolled in the sites you can see. The first
                    install picks them up.
                  </div>
                </div>
              </Card>
            )}

            {totals.total > 0 && filtered.length === 0 && (
              <Card>
                <div className="empty">
                  <div className="title">No devices match these filters</div>
                  <div>Try widening the search or clearing filters.</div>
                </div>
              </Card>
            )}

            {filtered.length > 0 && (
              <div className="card flush">
                {groups.map((client) => {
                  const allDev = client.sites.flatMap((s) => s.devices);
                  const onlineCount = allDev.filter((d) => d.isOnline).length;
                  const clientOpen = filtering || !collapsedClients.has(client.clientName);
                  return (
                    <section key={client.clientName}>
                      <div
                        className="tree-row"
                        onClick={() => toggleClient(client.clientName)}
                        role="button"
                        aria-expanded={clientOpen}
                        tabIndex={0}
                        onKeyDown={(e) => {
                          if (e.key === "Enter" || e.key === " ") {
                            e.preventDefault();
                            toggleClient(client.clientName);
                          }
                        }}
                      >
                        <span className={`chev${clientOpen ? " open" : ""}`}>
                          <svg width={10} height={10} viewBox="0 0 10 10" fill="none" aria-hidden>
                            <path
                              d="M3.5 2 L6.5 5 L3.5 8"
                              stroke="currentColor"
                              strokeWidth={1.5}
                              strokeLinecap="round"
                              strokeLinejoin="round"
                            />
                          </svg>
                        </span>
                        <h2
                          className="client-name"
                          style={{ margin: 0, fontWeight: 700, fontSize: 14 }}
                        >
                          {client.clientName}
                        </h2>
                        <span className="count-chip">{allDev.length}</span>
                        <span className="meta">
                          <span className="row" style={{ gap: 6 }}>
                            <Dot
                              tone={
                                onlineCount === allDev.length
                                  ? "green"
                                  : onlineCount === 0
                                    ? "gray"
                                    : "amber"
                              }
                            />
                            <span>
                              {onlineCount}/{allDev.length} online
                            </span>
                          </span>
                          <span className="muted">{client.sites.length} sites</span>
                        </span>
                      </div>
                      {clientOpen &&
                        client.sites.map((site) => {
                          const key = `${client.clientName}//${site.siteName}`;
                          return (
                            <SiteRow
                              key={site.siteName}
                              site={site}
                              open={filtering || !collapsedSites.has(key)}
                              onToggle={() => toggleSite(key)}
                            />
                          );
                        })}
                    </section>
                  );
                })}
              </div>
            )}
          </>
        )}
      </main>
    </>
  );
}

interface SiteRowProps {
  site: { siteName: string; devices: DeviceSummary[] };
  open: boolean;
  onToggle: () => void;
}

function SiteRow({ site, open, onToggle }: SiteRowProps) {
  const online = site.devices.filter((d) => d.isOnline).length;
  return (
    <section>
      <div
        className="tree-row is-site"
        onClick={onToggle}
        role="button"
        aria-expanded={open}
        tabIndex={0}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onToggle();
          }
        }}
      >
        <span className={`chev${open ? " open" : ""}`}>
          <svg width={10} height={10} viewBox="0 0 10 10" fill="none" aria-hidden>
            <path
              d="M3.5 2 L6.5 5 L3.5 8"
              stroke="currentColor"
              strokeWidth={1.5}
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </span>
        <h3 className="site-name" style={{ margin: 0, fontWeight: 600, fontSize: 13 }}>
          {site.siteName}
        </h3>
        <span className="count-chip">{site.devices.length}</span>
        <span className="meta">
          <span className="row" style={{ gap: 6 }}>
            <Dot
              tone={online === site.devices.length ? "green" : online === 0 ? "gray" : "amber"}
            />
            <span>
              {online}/{site.devices.length}
            </span>
          </span>
        </span>
      </div>
      {open && (
        <ul style={{ listStyle: "none", margin: 0, padding: 0 }}>
          {site.devices.map((d) => (
            <li key={d.deviceId} className="tree-row is-device">
              <Dot tone={d.isOnline ? "green" : "gray"} />
              <Link
                href={`/devices/${d.deviceId}`}
                className="hostname"
                style={{ color: "var(--ink)", textDecoration: "none" }}
              >
                {d.hostname}
              </Link>
              <span className="meta">
                <PresenceChip online={d.isOnline} />
              </span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}
