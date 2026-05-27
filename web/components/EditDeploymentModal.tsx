"use client";

// EditDeploymentModal is the staff-only assign/unassign UI for a
// device's site + asset_number. Triggered from the Deployment card's
// Edit button; submits PUT /devices/{id}/deployment via the
// useUpdateDeviceDeployment mutation.
//
// Same inline-styled overlay shape as CameraDialog — keeps the slice
// off globals.css; promotion to a real Dialog primitive is a future
// polish cycle.
import { useState } from "react";
import type {
  ClientWithSites,
  DeploymentUpdate,
} from "../lib/api/taxonomy";

interface Props {
  tree: ClientWithSites[];
  currentSiteId: string | null;
  currentAssetNumber: string | null;
  saving: boolean;
  onSubmit: (update: DeploymentUpdate) => Promise<void>;
  onClose: () => void;
}

export function EditDeploymentModal({
  tree,
  currentSiteId,
  currentAssetNumber,
  saving,
  onSubmit,
  onClose,
}: Props) {
  // Empty string represents "Unassigned" — <select> value can't be
  // null, but our submission contract maps "" → null.
  const [siteId, setSiteId] = useState<string>(currentSiteId ?? "");
  const [assetNumber, setAssetNumber] = useState<string>(
    currentAssetNumber ?? "",
  );
  const [errorMessage, setErrorMessage] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    setSubmitting(true);
    setErrorMessage(null);
    try {
      await onSubmit({
        siteId: siteId === "" ? null : siteId,
        assetNumber: assetNumber === "" ? null : assetNumber,
      });
    } catch (err) {
      setErrorMessage(err instanceof Error ? err.message : String(err));
    } finally {
      setSubmitting(false);
    }
  }

  const busy = saving || submitting;

  return (
    <div
      role="dialog"
      aria-modal="true"
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,0.45)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 1000,
      }}
    >
      <div
        style={{
          background: "var(--bg, white)",
          borderRadius: 6,
          padding: 24,
          width: "min(520px, calc(100% - 32px))",
          boxShadow: "0 12px 32px rgba(0,0,0,0.2)",
        }}
      >
        <h2 style={{ margin: 0, fontSize: 18, marginBottom: 12 }}>
          Edit deployment
        </h2>
        <form onSubmit={handleSave}>
          <label
            htmlFor="deployment-site"
            style={{ display: "block", fontSize: 13, marginBottom: 12 }}
          >
            Site
            <select
              id="deployment-site"
              value={siteId}
              onChange={(e) => setSiteId(e.target.value)}
              style={{
                width: "100%",
                padding: "6px 8px",
                marginTop: 4,
                fontSize: 14,
              }}
            >
              <option value="">— Unassigned —</option>
              {tree.map((client) => (
                <optgroup key={client.id} label={client.name}>
                  {client.sites.map((site) => (
                    <option key={site.id} value={site.id}>
                      {site.name} · {site.brandName} · {site.externalId}
                      {site.active ? "" : " (inactive)"}
                    </option>
                  ))}
                </optgroup>
              ))}
            </select>
          </label>
          <label
            htmlFor="deployment-asset"
            style={{ display: "block", fontSize: 13, marginBottom: 12 }}
          >
            Asset number
            <input
              id="deployment-asset"
              type="text"
              value={assetNumber}
              onChange={(e) => setAssetNumber(e.target.value)}
              placeholder="e.g. UK-MAC-007"
              style={{
                width: "100%",
                padding: "6px 8px",
                marginTop: 4,
                fontSize: 14,
                fontFamily: "monospace",
              }}
            />
          </label>
          {errorMessage && (
            <p style={{ color: "var(--red, #c00)", fontSize: 13 }}>
              {errorMessage}
            </p>
          )}
          <div
            style={{
              display: "flex",
              gap: 8,
              justifyContent: "flex-end",
              marginTop: 16,
            }}
          >
            <button type="button" onClick={onClose} disabled={busy}>
              Cancel
            </button>
            <button type="submit" disabled={busy}>
              {busy ? "Saving…" : "Save"}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
