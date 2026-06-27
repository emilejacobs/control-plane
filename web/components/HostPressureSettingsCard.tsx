// HostPressureSettingsCard renders the Settings → Host pressure section. It
// tunes the red/yellow scoring thresholds for the host_net_pressure probe
// (store13/mesa ephemeral-port-exhaustion incident). Staff-only: a non-staff
// 403 hides the card. Edits apply fleet-wide on the next probe (~5 min) — no
// agent redeploy, because CP scores the probe at ingest.
"use client";

import { useEffect, useState } from "react";
import { Card } from "./ui/Card";
import { useHostPressureThresholds, useSetHostPressureThresholds } from "../lib/api/hooks";

export function HostPressureSettingsCard() {
  const settings = useHostPressureThresholds();
  const save = useSetHostPressureThresholds();

  const [warnPct, setWarnPct] = useState("40");
  const [critPct, setCritPct] = useState("60");
  const [cwWarn, setCwWarn] = useState("100");
  const [cwCrit, setCwCrit] = useState("400");

  useEffect(() => {
    if (settings.data) {
      setWarnPct(String(settings.data.ephemeralWarnPct));
      setCritPct(String(settings.data.ephemeralCritPct));
      setCwWarn(String(settings.data.closeWaitWarn));
      setCwCrit(String(settings.data.closeWaitCrit));
    }
  }, [settings.data]);

  if (settings.error) {
    // Non-staff (403) or surface down: hide so the rest of Settings renders.
    return null;
  }
  if (settings.isLoading || !settings.data) return null;

  const onSave = () => {
    save.mutate({
      ephemeralWarnPct: parseFloat(warnPct) || 0,
      ephemeralCritPct: parseFloat(critPct) || 0,
      closeWaitWarn: parseInt(cwWarn, 10) || 0,
      closeWaitCrit: parseInt(cwCrit, 10) || 0,
    });
  };

  const saveError = save.error instanceof Error ? save.error.message : null;
  const inputStyle = { width: 100, fontSize: 13, padding: "4px 8px" } as const;

  return (
    <Card label="Host pressure">
      <p className="muted">
        Scoring thresholds for the <code>host_net_pressure</code> probe. A device
        crossing the critical line goes red and fires a fleet alert; the warning
        line shows on the device page only. Edits apply within ~5 minutes — no
        deploy needed.
      </p>

      <div className="row" style={{ gap: 24, flexWrap: "wrap", marginTop: 12 }}>
        <div>
          <label htmlFor="hp-warn-pct">Ephemeral pool warn %</label>
          <input
            id="hp-warn-pct"
            aria-label="Ephemeral pool warn percent"
            type="number"
            value={warnPct}
            disabled={save.isPending}
            onChange={(e) => setWarnPct(e.target.value)}
            style={{ display: "block", ...inputStyle }}
          />
        </div>
        <div>
          <label htmlFor="hp-crit-pct">Ephemeral pool critical %</label>
          <input
            id="hp-crit-pct"
            aria-label="Ephemeral pool critical percent"
            type="number"
            value={critPct}
            disabled={save.isPending}
            onChange={(e) => setCritPct(e.target.value)}
            style={{ display: "block", ...inputStyle }}
          />
        </div>
        <div>
          <label htmlFor="hp-cw-warn">CLOSE_WAIT warn</label>
          <input
            id="hp-cw-warn"
            aria-label="CLOSE_WAIT warn"
            type="number"
            value={cwWarn}
            disabled={save.isPending}
            onChange={(e) => setCwWarn(e.target.value)}
            style={{ display: "block", ...inputStyle }}
          />
        </div>
        <div>
          <label htmlFor="hp-cw-crit">CLOSE_WAIT critical</label>
          <input
            id="hp-cw-crit"
            aria-label="CLOSE_WAIT critical"
            type="number"
            value={cwCrit}
            disabled={save.isPending}
            onChange={(e) => setCwCrit(e.target.value)}
            style={{ display: "block", ...inputStyle }}
          />
        </div>
      </div>

      {saveError && (
        <p className="muted" style={{ color: "var(--danger)", marginTop: 8 }}>
          {saveError}
        </p>
      )}

      <div style={{ marginTop: 12 }}>
        <button className="btn" onClick={onSave} disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save thresholds"}
        </button>
      </div>
    </Card>
  );
}
