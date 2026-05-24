// Home — the authenticated landing. Mirrors /overview so a deep-link to
// the root drops the operator directly into the fleet-health dashboard
// without a redirect bounce.
//
// Two gates, in order:
//   1. /auth/first-run uninitialized  → /first-run (claim the admin)
//   2. initialized but no tokens      → /login
// Both routes are still rendered (no flash of an empty shell) — the
// useEffects fire after first paint and the gates are typically resolved
// before the operator notices.
"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useFirstRunStatus } from "../lib/api/hooks";
import { currentTokens } from "../lib/api/client";
import OverviewPage from "./overview/page";

export default function Home() {
  const router = useRouter();
  const { data } = useFirstRunStatus();

  useEffect(() => {
    if (!data) return;
    if (!data.initialized) {
      router.replace("/first-run");
      return;
    }
    if (currentTokens() === null) {
      router.replace("/login");
    }
  }, [data, router]);

  return <OverviewPage />;
}
