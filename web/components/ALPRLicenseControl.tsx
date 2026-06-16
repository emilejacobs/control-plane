// ALPRLicenseControl is the per-device Plate Recognizer license field (#84) on
// the device page. The license is a secret: the control shows only set/not-set
// (from licenseSet) and lets staff (re)enter it — the value is never fetched.
// Saving PUTs to /devices/{id}/alpr-license; CP pushes it to the device at
// Commission.
"use client";

import { useState } from "react";
import { useSetALPRLicense } from "../lib/api/hooks";

interface Props {
  deviceId: string;
  licenseSet: boolean;
}

export function ALPRLicenseControl({ deviceId, licenseSet }: Props) {
  const set = useSetALPRLicense(deviceId);
  const [license, setLicense] = useState("");
  const errorMessage = set.error instanceof Error ? set.error.message : null;

  const onSave = () => {
    if (!license) return;
    set.mutate(license, { onSuccess: () => setLicense("") });
  };

  return (
    <div className="row" style={{ gap: 8, alignItems: "center", fontSize: 12.5 }}>
      <span className="muted">ALPR license</span>
      <strong>{licenseSet ? "Set" : "Not set"}</strong>
      <input
        type="password"
        aria-label="ALPR license"
        placeholder={licenseSet ? "Replace license…" : "Enter license…"}
        value={license}
        disabled={set.isPending}
        onChange={(e) => setLicense(e.target.value)}
        style={{
          fontSize: 12.5,
          padding: "2px 6px",
          border: "1px solid var(--line, #ccc)",
          borderRadius: 4,
          minWidth: 200,
        }}
      />
      <button
        type="button"
        className="btn"
        onClick={onSave}
        disabled={set.isPending || license === ""}
      >
        Save
      </button>
      {set.isPending && (
        <span className="muted" role="status">
          Saving…
        </span>
      )}
      {errorMessage && (
        <span role="alert" style={{ color: "var(--red, #c00)" }}>
          {errorMessage}
        </span>
      )}
    </div>
  );
}
