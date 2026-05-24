// Home — the authenticated landing. Mirrors /overview so a deep-link to
// the root drops the operator directly into the fleet-health dashboard
// without a redirect bounce.
//
// Init guard: GET /auth/first-run is unauthenticated and cheap; if it
// reports an empty system, we route to /first-run instead of rendering
// Overview (which would 401 every API call and show an empty shell).
"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useFirstRunStatus } from "../lib/api/hooks";
import OverviewPage from "./overview/page";

export default function Home() {
  const router = useRouter();
  const { data } = useFirstRunStatus();

  useEffect(() => {
    if (data && !data.initialized) {
      router.replace("/first-run");
    }
  }, [data, router]);

  return <OverviewPage />;
}
