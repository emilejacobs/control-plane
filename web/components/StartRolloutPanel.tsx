"use client";

import { useState } from "react";
import { useAgentVersions, useSitesTree, useStartRollout } from "../lib/api/hooks";
import type { RolloutTarget } from "../lib/api/rollouts";
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
  selectedDeviceIds?: string[];
  onStarted?: () => void;
}

export function StartRolloutPanel({ selectedDeviceIds = [], onStarted }: Props = {}) {
  const versions = useAgentVersions();
  const sites = useSitesTree();
  const start = useStartRollout();

  const [version, setVersion] = useState("");
  const [mode, setMode] = useState<TargetMode>("all");
  const [siteId, setSiteId] = useState("");

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
              <option value="">Select a site…</option>
              {(sites.data ?? []).map((c) => (
                <optgroup key={c.id} label={c.name}>
                  {c.sites.map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}
                    </option>
                  ))}
                </optgroup>
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
