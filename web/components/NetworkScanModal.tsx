"use client";

// NetworkScanModal renders the operator's view of one network.scan
// request: a spinner while pending, a table of {ip, vendor, ports} when
// done, the agent's error message when error. One-click "Add as camera"
// per row populates the parent's Add Camera dialog with the IP
// pre-filled — the operator's main install-flow shortcut (ADR-030 § 2).
//
// Inline-style approach matches CameraDialog and EditServicesModal —
// no globals.css growth this slice. Promotion to a real Dialog
// primitive is a future polish cycle.
import { useState } from "react";
import type { NetworkScan } from "../lib/api/devices";

interface Props {
  // scan is null between kicking a scan off and the first poll landing — the
  // modal opens immediately on click (#12) and shows the pending state until
  // the row arrives.
  scan: NetworkScan | null;
  onClose: () => void;
  // onAddCamera is fired with the candidate's IP; the parent opens its
  // Add Camera dialog with that IP pre-filled.
  onAddCamera: (ip: string) => void;
  // onRescan re-triggers the scan. Called with undefined for auto-detect
  // (empty CIDR field) or the operator-entered CIDR override (#12).
  onRescan: (cidr?: string) => void;
}

// isValidCidr is a client-side guard for the optional subnet override: an IPv4
// address with octets 0–255 and a /0–/32 prefix. The agent re-validates server
// side; this just stops obviously-malformed input from being submitted.
export function isValidCidr(value: string): boolean {
  const m = value.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})\/(\d{1,2})$/);
  if (!m) return false;
  const octets = [m[1], m[2], m[3], m[4]].map(Number);
  if (octets.some((o) => o > 255)) return false;
  const prefix = Number(m[5]);
  return prefix <= 32;
}

export function NetworkScanModal({ scan, onClose, onAddCamera, onRescan }: Props) {
  const [cidr, setCidr] = useState("");
  const trimmed = cidr.trim();
  const cidrInvalid = trimmed !== "" && !isValidCidr(trimmed);
  // null scan = just kicked off; "pending" = polling in flight. Both block a
  // re-scan so the operator can't stack overlapping requests.
  const pending = scan === null || scan.status === "pending";

  function handleRescan() {
    if (cidrInvalid || pending) return;
    onRescan(trimmed === "" ? undefined : trimmed);
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
          width: "min(640px, calc(100% - 32px))",
          maxHeight: "calc(100% - 64px)",
          overflow: "auto",
          boxShadow: "0 12px 32px rgba(0,0,0,0.2)",
        }}
      >
        <div
          style={{
            display: "flex",
            justifyContent: "space-between",
            alignItems: "center",
            marginBottom: 12,
          }}
        >
          <h2 style={{ margin: 0, fontSize: 18 }}>Network scan</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label="Close"
            style={{
              background: "transparent",
              border: "1px solid var(--line, #ccc)",
              borderRadius: 4,
              padding: "4px 10px",
              cursor: "pointer",
              fontSize: 12,
            }}
          >
            Close
          </button>
        </div>

        <div
          style={{
            display: "flex",
            gap: 8,
            alignItems: "flex-start",
            marginBottom: 14,
            paddingBottom: 14,
            borderBottom: "1px solid var(--line, #eee)",
          }}
        >
          <label style={{ flex: 1, fontSize: 12.5 }}>
            <span style={{ display: "block", color: "var(--ink-2, #666)", marginBottom: 4 }}>
              Subnet (CIDR) — leave blank to auto-detect
            </span>
            <input
              type="text"
              aria-label="Subnet (CIDR)"
              placeholder="e.g. 192.168.1.0/24"
              value={cidr}
              onChange={(e) => setCidr(e.target.value)}
              style={{
                width: "100%",
                boxSizing: "border-box",
                padding: "6px 8px",
                fontSize: 13,
                border: `1px solid ${cidrInvalid ? "var(--red, #c00)" : "var(--line, #ccc)"}`,
                borderRadius: 4,
              }}
            />
            {cidrInvalid && (
              <span role="alert" style={{ display: "block", marginTop: 4, color: "var(--red, #c00)", fontSize: 11.5 }}>
                Enter a valid CIDR, e.g. 192.168.1.0/24.
              </span>
            )}
          </label>
          <button
            type="button"
            onClick={handleRescan}
            disabled={pending || cidrInvalid}
            style={{
              alignSelf: "flex-end",
              padding: "6px 12px",
              fontSize: 12.5,
              background: "transparent",
              border: "1px solid var(--line, #ccc)",
              borderRadius: 4,
              cursor: pending || cidrInvalid ? "not-allowed" : "pointer",
              opacity: pending || cidrInvalid ? 0.55 : 1,
              whiteSpace: "nowrap",
            }}
          >
            Re-scan
          </button>
        </div>

        {pending && (
          <p style={{ fontSize: 14, margin: 0 }}>
            Scanning the device's LAN — this typically takes ~10 seconds…
          </p>
        )}

        {scan?.status === "error" && (
          <div role="alert" style={{ fontSize: 14 }}>
            <p style={{ margin: 0, color: "var(--red, #c00)" }}>
              {scan.errorMessage ?? "Scan failed."}
            </p>
            {scan.errorCode && (
              <p
                className="mono"
                style={{
                  margin: "6px 0 0 0",
                  fontSize: 11.5,
                  color: "var(--ink-2, #666)",
                }}
              >
                {scan.errorCode}
              </p>
            )}
          </div>
        )}

        {scan?.status === "done" && scan.hosts !== null && scan.hosts.length === 0 && (
          <p className="muted" style={{ fontSize: 13, margin: 0 }}>
            No candidate cameras found on the LAN. Verify the device is
            on the right subnet and try again.
          </p>
        )}

        {scan?.status === "done" && scan.hosts !== null && scan.hosts.length > 0 && (
          <table
            style={{ width: "100%", borderCollapse: "collapse", fontSize: 13 }}
          >
            <thead>
              <tr style={{ textAlign: "left", color: "var(--ink-2)" }}>
                <th style={{ padding: "6px 8px" }}>IP</th>
                <th style={{ padding: "6px 8px" }}>Vendor</th>
                <th style={{ padding: "6px 8px" }}>Open ports</th>
                <th style={{ padding: "6px 8px", width: 130 }}></th>
              </tr>
            </thead>
            <tbody>
              {scan.hosts.map((h) => (
                <tr
                  key={h.ip}
                  style={{ borderTop: "1px solid var(--line)" }}
                >
                  <td className="mono" style={{ padding: "6px 8px" }}>{h.ip}</td>
                  <td style={{ padding: "6px 8px" }}>
                    {h.vendor || (
                      <span className="muted" style={{ fontSize: 12 }}>—</span>
                    )}
                  </td>
                  <td
                    className="mono"
                    style={{ padding: "6px 8px", fontSize: 11.5 }}
                  >
                    {h.openPorts.length === 0
                      ? "—"
                      : h.openPorts.join(", ")}
                  </td>
                  <td style={{ padding: "6px 8px", textAlign: "right" }}>
                    <button
                      type="button"
                      onClick={() => onAddCamera(h.ip)}
                      style={{
                        fontSize: 12,
                        padding: "2px 8px",
                        background: "transparent",
                        border: "1px solid var(--line, #ccc)",
                        borderRadius: 4,
                        cursor: "pointer",
                      }}
                    >
                      Add as camera
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
