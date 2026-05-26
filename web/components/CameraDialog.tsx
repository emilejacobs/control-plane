"use client";

// CameraDialog is the add / edit / delete modal for one camera row
// (Phase 2 Edge UI rework, issue #2). Mode discriminator keeps the
// shape symmetric across the three CRUD verbs — same overlay, same
// form skeleton for add+edit, confirmation-only body for delete.
//
// Minimal styling — same inline-style approach as EditServicesModal
// so this slice doesn't grow globals.css. Promotion to a real Dialog
// primitive is a future polish cycle.
import { useState } from "react";
import type { CameraInput, Camera } from "../lib/api/devices";

export type CameraDialogMode =
  | { mode: "add" }
  | { mode: "edit"; camera: Camera }
  | { mode: "delete"; camera: Camera };

interface Props {
  mode: CameraDialogMode["mode"];
  camera?: Camera; // required for edit + delete; ignored on add
  onSubmit: (input: CameraInput) => Promise<void>;
  onClose: () => void;
}

export function CameraDialog({ mode, camera, onSubmit, onClose }: Props) {
  const [label, setLabel] = useState(camera?.label ?? "");
  const [rtspUrl, setRtspUrl] = useState(camera?.rtspUrl ?? "");
  const [isLpr, setIsLpr] = useState(camera?.isLpr ?? false);
  const [saving, setSaving] = useState(false);
  const [errorMessage, setErrorMessage] = useState<string | null>(null);

  const heading =
    mode === "add"
      ? "Add camera"
      : mode === "edit"
        ? `Edit camera — ${camera?.label ?? ""}`
        : `Delete camera — ${camera?.label ?? ""}`;

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    setSaving(true);
    setErrorMessage(null);
    try {
      await onSubmit({ label, rtspUrl, isLpr });
    } catch (err) {
      setErrorMessage(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

  async function handleConfirmDelete() {
    setSaving(true);
    setErrorMessage(null);
    try {
      // Delete reuses onSubmit's shape — caller wires it to deleteCamera
      // and ignores the input fields.
      await onSubmit({ label, rtspUrl, isLpr });
    } catch (err) {
      setErrorMessage(err instanceof Error ? err.message : String(err));
    } finally {
      setSaving(false);
    }
  }

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
          width: "min(480px, calc(100% - 32px))",
          boxShadow: "0 12px 32px rgba(0,0,0,0.2)",
        }}
      >
        <h2 style={{ margin: 0, fontSize: 18, marginBottom: 12 }}>{heading}</h2>

        {mode === "delete" ? (
          <>
            <p style={{ fontSize: 14, lineHeight: 1.5 }}>
              Delete camera <strong>{camera?.label}</strong>? This removes
              the row from the inventory and pushes the new list to the
              device. The agent's local cameras.json will reflect the
              change after the next ACK.
            </p>
            {errorMessage && (
              <p style={{ color: "var(--red, #c00)", fontSize: 13 }}>
                {errorMessage}
              </p>
            )}
            <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 16 }}>
              <button type="button" onClick={onClose} disabled={saving}>
                Cancel
              </button>
              <button
                type="button"
                onClick={handleConfirmDelete}
                disabled={saving}
                style={{ background: "var(--red, #c00)", color: "white", border: "none", padding: "6px 12px", borderRadius: 4 }}
              >
                Delete
              </button>
            </div>
          </>
        ) : (
          <form onSubmit={handleSave}>
            <label style={{ display: "block", fontSize: 13, marginBottom: 8 }}>
              Label
              <input
                type="text"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                required
                style={{ width: "100%", padding: "6px 8px", marginTop: 4, fontSize: 14 }}
              />
            </label>
            <label style={{ display: "block", fontSize: 13, marginBottom: 8 }}>
              RTSP URL
              <input
                type="text"
                value={rtspUrl}
                onChange={(e) => setRtspUrl(e.target.value)}
                required
                placeholder="rtsp://user:pass@host:554/stream"
                style={{
                  width: "100%",
                  padding: "6px 8px",
                  marginTop: 4,
                  fontSize: 13,
                  fontFamily: "monospace",
                }}
              />
            </label>
            <label style={{ display: "block", fontSize: 13, marginBottom: 12 }}>
              <input
                type="checkbox"
                checked={isLpr}
                onChange={(e) => setIsLpr(e.target.checked)}
                style={{ marginRight: 6 }}
              />
              LPR camera (feeds Plate Recognizer)
            </label>
            {errorMessage && (
              <p style={{ color: "var(--red, #c00)", fontSize: 13 }}>
                {errorMessage}
              </p>
            )}
            <div style={{ display: "flex", gap: 8, justifyContent: "flex-end", marginTop: 16 }}>
              <button type="button" onClick={onClose} disabled={saving}>
                Cancel
              </button>
              <button type="submit" disabled={saving}>
                Save
              </button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
