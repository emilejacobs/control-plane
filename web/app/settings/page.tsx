"use client";

import { RequireAuth } from "../../components/RequireAuth";
import { TaxonomySettingsCard } from "../../components/TaxonomySettingsCard";

// /settings — Phase 4 tenant-level configuration: alarm subscriptions,
// bootstrap-key rotation cadence, retention policies. The
// Clients & Sites section (ADR-033 § 8) is the first concrete card;
// other configuration moves out of placeholder as it ships.
export default function SettingsPage() {
  return (
    <RequireAuth>
      <div className="page-shell">
        <header className="page-head">
          <h1>Settings</h1>
        </header>
        <TaxonomySettingsCard />
      </div>
    </RequireAuth>
  );
}
