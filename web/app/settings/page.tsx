"use client";

import { PlaceholderPage } from "../../components/ui/PlaceholderPage";
import { RequireAuth } from "../../components/RequireAuth";

// /settings — Phase 4. Tenant-level configuration: alarm subscriptions,
// bootstrap-key rotation cadence, retention policies.
export default function SettingsPage() {
  return (
    <RequireAuth>
      <PlaceholderPage
        title="Settings"
        subtitle="Tenant configuration: alarm subscriptions, bootstrap-key rotation, retention policies."
        phase="Phase 4"
      />
    </RequireAuth>
  );
}
