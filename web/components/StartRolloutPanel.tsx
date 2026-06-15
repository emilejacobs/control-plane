"use client";

import { useMemo, useState } from "react";
import { useAgentVersions, useStartRollout } from "../lib/api/hooks";
import type { RolloutDevice, RolloutTarget } from "../lib/api/rollouts";
import { Card } from "./ui/Card";

// StartRolloutPanel is the staff-only "start a rollout" control (#42): pick a
// published version + a target — the entire fleet, a specific site, or an
// explicit device subset (the canary, Slice C) — and POST /agent-rollouts. The
// page gates this to staff; the POST is staff-only server-side too, so a
// non-staff caller still gets a surfaced 403.
//
// The "Selected devices" mode is driven by the page's table-selection state
// (selectedDeviceIds); onStarted fires after a successful start so the page can
// clear the selection.
type TargetMode = "all" | "site" | "devices";

interface Props {
  devices?: RolloutDevice[];
  selectedDeviceIds?: string[];
  onStarted?: () => void;
}

// SiteOption is one entry in the "Specific site" dropdown, derived from the
// sites devices are actually assigned to.
interface SiteOption {
  id: string;
  siteName: string;
  clientName: string | null;
}

// rolloutSiteOptions collapses the in-scope devices to the distinct sites they
// are assigned to (#64). Building the dropdown from devices.site_id — rather
// than the active-taxonomy tree — guarantees the offered id matches ≥1 device,
// so site targeting can't return a spurious no_targets when a site is re-keyed
// in the external source.
function rolloutSiteOptions(devices: RolloutDevice[]): SiteOption[] {
  const byId = new Map<string, SiteOption>();
  for (const d of devices) {
    if (d.siteId === null || byId.has(d.siteId)) continue;
    byId.set(d.siteId, {
      id: d.siteId,
      siteName: d.siteName ?? d.siteId,
      clientName: d.clientName,
    });
  }
  return [...byId.values()].sort((a, b) => {
    const client = (a.clientName ?? "").localeCompare(b.clientName ?? "");
    return client !== 0 ? client : a.siteName.localeCompare(b.siteName);
  });
}

export function StartRolloutPanel({ devices = [], selectedDeviceIds = [], onStarted }: Props = {}) {
  const versions = useAgentVersions();
  const start = useStartRollout();

  const [version, setVersion] = useState("");
  const [mode, setMode] = useState<TargetMode>("all");
  const [siteId, setSiteId] = useState("");

  const siteOptions = useMemo(() => rolloutSiteOptions(devices), [devices]);
  const versionList = versions.data ?? [];
  // Default to the newest (the catalog comes newest-first) until the operator
  // picks one explicitly.
  const effectiveVersion = version || versionList[0] || "";
  const selectedCount = selectedDeviceIds.length;

  const canStart =
    effectiveVersion !== "" &&
    !start.isPending &&
    (mode === "all" ||
      (mode === "site" && siteId !== "") ||
      (mode === "devices" && selectedCount > 0));

  function onStart() {
    let target: RolloutTarget;
    if (mode === "site") target = { kind: "site", siteId };
    else if (mode === "devices") target = { kind: "devices", deviceIds: selectedDeviceIds };
    else target = { kind: "all" };
    start.mutate({ version: effectiveVersion, target }, { onSuccess: () => onStarted?.() });
  }

  const errorMessage =
    start.error instanceof Error ? start.error.message : null;

  return (
    <Card label="Start a rollout">
      <div style={{ display: "grid", gap: 14, padding: "4px 2px" }}>
        <label className="field">
          <span className="field-label">Target version</span>
          <select
            aria-label="Target version"
            value={effectiveVersion}
            disabled={versions.isPending || versionList.length === 0}
            onChange={(e) => setVersion(e.target.value)}
          >
            {versionList.length === 0 && <option value="">No versions published</option>}
            {versionList.map((v) => (
              <option key={v} value={v}>
                {v}
              </option>
            ))}
          </select>
        </label>

        <fieldset className="field" style={{ border: "none", padding: 0, margin: 0 }}>
          <span className="field-label">Target</span>
          <div className="row" style={{ gap: 16 }}>
            <label className="row" style={{ gap: 6 }}>
              <input
                type="radio"
                name="rollout-target"
                checked={mode === "all"}
                onChange={() => setMode("all")}
              />
              Entire fleet
            </label>
            <label className="row" style={{ gap: 6 }}>
              <input
                type="radio"
                name="rollout-target"
                checked={mode === "site"}
                onChange={() => setMode("site")}
              />
              Specific site
            </label>
            <label
              className="row"
              style={{ gap: 6, opacity: selectedCount === 0 ? 0.5 : 1 }}
              title={selectedCount === 0 ? "Tick devices in the table below" : undefined}
            >
              <input
                type="radio"
                name="rollout-target"
                checked={mode === "devices"}
                disabled={selectedCount === 0}
                onChange={() => setMode("devices")}
              />
              Selected devices ({selectedCount})
            </label>
          </div>
        </fieldset>

        {mode === "site" && (
          <label className="field">
            <span className="field-label">Site</span>
            <select
              aria-label="Site"
              value={siteId}
              onChange={(e) => setSiteId(e.target.value)}
            >
              <option value="">
                {siteOptions.length === 0 ? "No assigned sites in scope" : "Select a site…"}
              </option>
              {siteOptions.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.clientName ? `${s.clientName} · ${s.siteName}` : s.siteName}
                </option>
              ))}
            </select>
          </label>
        )}

        <div className="row" style={{ gap: 12, alignItems: "center" }}>
          <button className="btn primary" onClick={onStart} disabled={!canStart}>
            {start.isPending ? "Starting…" : "Start rollout"}
          </button>
          {start.isSuccess && start.data && (
            <span role="status" className="muted">
              Targeted {start.data.targeted} device{start.data.targeted === 1 ? "" : "s"} ·
              pushed to {start.data.pushed} online now
            </span>
          )}
        </div>

        {errorMessage && (
          <div role="alert" className="muted" style={{ color: "var(--danger, #b00)" }}>
            {errorMessage}
          </div>
        )}
      </div>
    </Card>
  );
}
