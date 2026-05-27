// TaxonomySettingsCard renders the Clients & Sites section of the
// Settings page (ADR-033 § 8). Staff-only: a non-staff operator's
// 403 from GET /taxonomy/status hides the card entirely.
"use client";

import { useEffect, useState } from "react";
import { Card } from "./ui/Card";
import {
  getTaxonomyStatus,
  triggerTaxonomySync,
  type TaxonomyStatus,
} from "../lib/api/taxonomy";
import { ApiError } from "../lib/api/auth";

type LoadState =
  | { kind: "loading" }
  | { kind: "ready"; status: TaxonomyStatus }
  | { kind: "hidden" }; // 403 / forbidden — non-staff

export function TaxonomySettingsCard() {
  const [state, setState] = useState<LoadState>({ kind: "loading" });
  const [triggered, setTriggered] = useState(false);
  const [triggering, setTriggering] = useState(false);

  useEffect(() => {
    let cancelled = false;
    getTaxonomyStatus()
      .then((status) => {
        if (!cancelled) setState({ kind: "ready", status });
      })
      .catch((err) => {
        if (cancelled) return;
        if (err instanceof ApiError && err.status === 403) {
          setState({ kind: "hidden" });
          return;
        }
        // Treat other errors as hidden too — the rest of the
        // Settings page should still render even if the taxonomy
        // surface is down.
        setState({ kind: "hidden" });
      });
    return () => {
      cancelled = true;
    };
  }, []);

  if (state.kind === "loading") return null;
  if (state.kind === "hidden") return null;

  const { clientsTotal, sitesTotal, sitesActive, lastSyncedAt } = state.status;
  const lastSyncedLabel = lastSyncedAt
    ? new Date(lastSyncedAt).toLocaleString()
    : "Never";

  const onForce = async () => {
    setTriggering(true);
    try {
      await triggerTaxonomySync();
      setTriggered(true);
    } finally {
      setTriggering(false);
    }
  };

  return (
    <Card
      label="Clients & Sites"
      actions={
        <button
          type="button"
          className="btn"
          onClick={onForce}
          disabled={triggering}
        >
          Force sync now
        </button>
      }
    >
      <p>
        Last successful sync: <strong>{lastSyncedLabel}</strong>
      </p>
      <p>
        {clientsTotal} clients · {sitesTotal} sites ({sitesActive} active)
      </p>
      {triggered && <p>Sync started — refresh in ~1 minute.</p>}
    </Card>
  );
}
