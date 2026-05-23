"use client";

import { PlaceholderPage } from "../../components/ui/PlaceholderPage";

// /operators — Phase 2/3. Local-operator invite + site-scoped allowlist
// management. The AuthZ machinery is exercised today (every Phase 1 staff
// account is '*'-scoped); this surface lights up when non-staff operators
// arrive.
export default function OperatorsPage() {
  return (
    <PlaceholderPage
      title="Operators"
      subtitle="People with access to this control plane. Invite, scope to clients/sites, rotate keys."
      phase="Phase 2/3"
    />
  );
}
