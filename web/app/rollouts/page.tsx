"use client";

import Link from "next/link";
import { useAgentRollout } from "../../lib/api/hooks";
import type { RolloutState } from "../../lib/api/rollouts";
import { currentOperator } from "../../lib/api/client";
import { Topbar } from "../../components/ui/Topbar";
import { Card } from "../../components/ui/Card";
import { Dot } from "../../components/ui/Dot";
import { Pill, type PillTone } from "../../components/ui/Pill";
import { RequireAuth } from "../../components/RequireAuth";
import { StartRolloutPanel } from "../../components/StartRolloutPanel";

// RolloutsPage — the operator-facing agent fleet-update surface (#42, ADR-035
// §4). This slice is the READ view: roll-up counts plus a per-device
// desired-vs-reported table, polled on the fleet's 10s cadence. Rollout state
// is derived (no campaign entity); the start / abort / promote-canary write
// controls land in a later slice over POST /agent-rollouts.
//
// The route's auth gate lives here; the view is site-scoped server-side
// (staff see the whole fleet, scoped operators see their sites' slice), so the
// component renders exactly what GET /fleet/agent-rollout returns.
export default function RolloutsPage() {
  return (
    <RequireAuth>
      <RolloutsBody />
    </RequireAuth>
  );
}

const STATE_LABEL: Record<RolloutState, { label: string; tone: PillTone }> = {
  done: { label: "Converged", tone: "green" },
  in_flight: { label: "In flight", tone: "amber" },
  untargeted: { label: "Untargeted", tone: "neutral" },
};

function StatePill({ state }: { state: RolloutState }) {
  const { label, tone } = STATE_LABEL[state];
  return <Pill tone={tone}>{label}</Pill>;
}

export function RolloutsBody() {
  const rollout = useAgentRollout();
  const counts = rollout.data?.counts;
  const devices = rollout.data?.devices ?? [];
  // The start control is staff-only (the POST /agent-rollouts is staff-gated
  // server-side too). Scoped operators still get the read view below.
  const isStaff = currentOperator()?.isStaff ?? false;

  return (
    <>
      <Topbar />
      <main className="page">
        <div className="page-header">
          <div>
            <div className="crumbs">
              <span>uknomi</span>
              <span className="sep">/</span>
              <span style={{ color: "var(--ink)" }}>Rollouts</span>
            </div>
            <h1 className="page-title">Agent rollouts</h1>
            <p className="page-subtitle">
              Desired-vs-reported agent version across the fleet · polling every 10s
            </p>
          </div>
        </div>

        {isStaff && <StartRolloutPanel />}

        {rollout.isPending && (
          <div role="status" className="muted" style={{ padding: 12 }}>
            Loading rollout…
          </div>
        )}

        {rollout.isError && (
          <Card>
            <div role="alert" className="row" style={{ gap: 12 }}>
              <p style={{ margin: 0 }}>Could not load the rollout view.</p>
              <button className="btn" onClick={() => rollout.refetch()}>
                Refresh
              </button>
            </div>
          </Card>
        )}

        {counts && (
          <div className="stat-grid">
            <div className="stat" data-testid="rollup-done">
              <div className="stat-label">Converged</div>
              <div className="stat-value">{counts.done}</div>
              <div className="stat-sub">reported matches desired</div>
            </div>
            <div className="stat" data-testid="rollup-in_flight">
              <div className="stat-label">In flight</div>
              <div className="stat-value">{counts.inFlight}</div>
              <div className="stat-sub">targeted, not yet converged</div>
            </div>
            <div className="stat" data-testid="rollup-untargeted">
              <div className="stat-label">Untargeted</div>
              <div className="stat-value">{counts.untargeted}</div>
              <div className="stat-sub">no desired version set</div>
            </div>
          </div>
        )}

        {rollout.data && (
          <Card label="Devices" flush>
            {devices.length === 0 ? (
              <div className="empty">
                <div className="title">No devices in scope</div>
                <div>Devices appear here once they enroll.</div>
              </div>
            ) : (
              <table className="table">
                <thead>
                  <tr>
                    <th>Device</th>
                    <th>Client / site</th>
                    <th>Reported &rarr; desired</th>
                    <th>State</th>
                  </tr>
                </thead>
                <tbody>
                  {devices.map((d) => (
                    <tr key={d.id}>
                      <td>
                        <span className="row" style={{ gap: 6 }}>
                          <Dot tone={d.isOnline ? "green" : "gray"} />
                          <Link
                            href={`/devices/${d.id}`}
                            className="hostname"
                            style={{ color: "var(--ink)", textDecoration: "none" }}
                          >
                            {d.hostname}
                          </Link>
                        </span>
                      </td>
                      <td className="muted">
                        {d.clientName
                          ? `${d.clientName} · ${d.siteName ?? "—"}`
                          : "Unassigned"}
                      </td>
                      <td className="tabular">
                        {d.reportedVersion || "—"}
                        <span style={{ color: "var(--ink-3)" }}> &rarr; </span>
                        {d.desiredVersion ?? "—"}
                      </td>
                      <td>
                        <StatePill state={d.state} />
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </Card>
        )}
      </main>
    </>
  );
}
