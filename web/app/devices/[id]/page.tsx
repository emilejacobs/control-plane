"use client";

import { useParams } from "next/navigation";
import Link from "next/link";
import { useDevice, useNow } from "../../../lib/api/hooks";
import { UNASSIGNED } from "../../../lib/fleet";
import { PresenceChip } from "../../../components/PresenceChip";
import { CertExpiryIndicator } from "../../../components/CertExpiryIndicator";
import { ServicesPanel } from "../../../components/ServicesPanel";
import { Topbar } from "../../../components/ui/Topbar";
import { Card } from "../../../components/ui/Card";
import { KV } from "../../../components/ui/KV";
import { Pill } from "../../../components/ui/Pill";
import { Placeholder } from "../../../components/ui/Placeholder";
import { RequireAuth } from "../../../components/RequireAuth";
import { formatAgo } from "../../../lib/ago";

// DevicePage is the per-device view: static record + live presence + the
// mTLS cert expiry, polled every 10s (Issue 18).
//
// The design exposes panels (Services, live CPU/Memory, Cameras, Tailscale
// IP, etc.) that GET /devices/{id} does not yet return. Per the bundle
// integration decision, those panels do not render — the page surfaces
// only what the API actually provides.
//
// Test contract preserved: <h1>{hostname}, KV pairs by <dt> label, the
// CertExpiryIndicator's "Certificate expires …" text, the "N seconds ago"
// ago-string ticking between polls.
export default function DevicePage() {
  const { id } = useParams<{ id: string }>();
  const device = useDevice(id);
  const now = useNow();
  const d = device.data;

  // Cert "pill" tone derived from days remaining — mirrors the band logic
  // in CertExpiryIndicator without duplicating its text shape.
  const certTone = d?.certDaysRemaining == null
    ? "neutral"
    : d.certDaysRemaining < 0
      ? "red"
      : d.certDaysRemaining <= 30
        ? "amber"
        : "green";
  const certLabel =
    d?.certDaysRemaining == null
      ? "Unknown"
      : d.certDaysRemaining < 0
        ? "Expired"
        : `${d.certDaysRemaining}d left`;

  return (
    <RequireAuth>
      <Topbar />
      <main className="page">
        {device.isPending && (
          <div role="status" className="muted" style={{ padding: 12 }}>
            Loading device…
          </div>
        )}

        {device.isError && (
          <Card>
            <div role="alert" className="row" style={{ gap: 12 }}>
              <p style={{ margin: 0 }}>Could not load device.</p>
              <button className="btn" onClick={() => device.refetch()}>
                Refresh
              </button>
            </div>
          </Card>
        )}

        {d && (
          <>
            <div className="page-header">
              <div>
                <div className="crumbs">
                  <Link href="/devices">Fleet</Link>
                  <span className="sep">/</span>
                  {d.clientName ? (
                    <span>{d.clientName}</span>
                  ) : (
                    <span className="muted">{UNASSIGNED}</span>
                  )}
                  {d.siteName && (
                    <>
                      <span className="sep">/</span>
                      <span className="muted">{d.siteName}</span>
                    </>
                  )}
                </div>
                <h1 className="page-title mono" style={{ fontSize: 24 }}>
                  {d.hostname}
                </h1>
                <div className="row" style={{ gap: 12, marginTop: 8 }}>
                  <PresenceChip online={d.isOnline} />
                  <span className="muted" style={{ fontSize: 13 }}>
                    Last seen{" "}
                    {d.lastSeenAt ? (
                      <time>{formatAgo(d.lastSeenAt, now)}</time>
                    ) : (
                      "Never"
                    )}
                  </span>
                  <span style={{ width: 1, height: 16, background: "var(--line)" }} />
                  <Pill tone={certTone}>Cert · {certLabel}</Pill>
                </div>
              </div>
            </div>

            <div className="detail-grid" style={{ marginBottom: 16 }}>
              <Card label="System">
                <KV
                  items={[
                    ["Hostname", <span className="mono">{d.hostname}</span>],
                    ["Hardware kind", d.hardwareKind],
                    ["OS version", d.osVersion],
                  ]}
                />
              </Card>

              <Card label="Deployment">
                <KV
                  items={[
                    ["Client", d.clientName ?? UNASSIGNED],
                    ["Site", d.siteName ?? UNASSIGNED],
                    ["Agent version", <span className="mono">{d.agentVersion}</span>],
                    [
                      "Hardware UUID",
                      <span className="mono" style={{ fontSize: 11.5 }}>
                        {d.hardwareUuid}
                      </span>,
                    ],
                    ["Enrolled", d.enrolledAt.slice(0, 10)],
                    [
                      "IoT Thing ARN",
                      <span className="mono" style={{ fontSize: 11, color: "var(--ink-2)" }}>
                        {d.iotThingArn}
                      </span>,
                    ],
                  ]}
                />
              </Card>
            </div>

            <Card label="mTLS Certificate">
              <div className="row" style={{ gap: 24, alignItems: "flex-start", flexWrap: "wrap" }}>
                <div style={{ flex: 1, minWidth: 280 }}>
                  <div className="row" style={{ gap: 10 }}>
                    <div
                      className="stat-value"
                      style={{ fontSize: 26 }}
                    >
                      {d.certDaysRemaining == null
                        ? "Unknown"
                        : d.certDaysRemaining < 0
                          ? "Expired"
                          : `${d.certDaysRemaining} days`}
                    </div>
                    <Pill tone={certTone}>{certLabel}</Pill>
                  </div>
                  <div className="muted" style={{ marginTop: 4, fontSize: 12.5 }}>
                    <CertExpiryIndicator
                      expiresAt={d.certExpiresAt}
                      daysRemaining={d.certDaysRemaining}
                    />
                  </div>
                  {d.certDaysRemaining != null && (
                    <div className="cert-bar">
                      <div
                        className={`fill ${
                          d.certDaysRemaining < 0
                            ? "crit"
                            : d.certDaysRemaining <= 7
                              ? "crit"
                              : d.certDaysRemaining <= 30
                                ? "warn"
                                : ""
                        }`}
                        style={{
                          width: `${Math.max(0, Math.min(100, (d.certDaysRemaining / 365) * 100))}%`,
                        }}
                      />
                    </div>
                  )}
                </div>
              </div>
            </Card>

            <div style={{ height: 16 }} />

            <Card label="Services">
              <ServicesPanel services={d.services} now={now} />
            </Card>

            <div style={{ height: 16 }} />

            <Card label="Activity">
              <Placeholder
                label="ACTIVITY TIMELINE · Phase 2"
                height={180}
              />
              <p className="muted" style={{ fontSize: 12.5, marginTop: 10 }}>
                Per-device activity (heartbeats, agent updates, service state
                changes) lands with the events surface in Phase 2.
              </p>
            </Card>
          </>
        )}
      </main>
    </RequireAuth>
  );
}
