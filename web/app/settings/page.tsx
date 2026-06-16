"use client";

import { RequireAuth } from "../../components/RequireAuth";
import { Topbar } from "../../components/ui/Topbar";
import { TaxonomySettingsCard } from "../../components/TaxonomySettingsCard";
import { PRTokenSettingsCard } from "../../components/PRTokenSettingsCard";
import { NotificationSettingsCard } from "../../components/NotificationSettingsCard";

// /settings — Phase 4 tenant-level configuration. The "Clients & Sites"
// card (ADR-033 § 8) is the first concrete section; other cards (alarm
// subs, retention, bootstrap-key rotation) ship as they land. Page
// chrome mirrors the rest of the dashboard so nav stays consistent.
export default function SettingsPage() {
  return (
    <RequireAuth>
      <Topbar />
      <main className="page">
        <div className="page-header">
          <div>
            <div className="crumbs">
              <span>uknomi</span>
              <span className="sep">/</span>
              <span style={{ color: "var(--ink)" }}>Settings</span>
            </div>
            <h1 className="page-title">Settings</h1>
            <p className="page-subtitle">
              Tenant configuration. More cards (alarm subscriptions,
              retention policies, bootstrap-key rotation) arrive as they
              ship.
            </p>
          </div>
        </div>
        <TaxonomySettingsCard />
        <PRTokenSettingsCard />
        <NotificationSettingsCard />
      </main>
    </RequireAuth>
  );
}
