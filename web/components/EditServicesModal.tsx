"use client";

// EditServicesModal lets an operator edit a device's per-device
// service allow-list + reporting cadence (Phase 2 slice 2). Opens
// from the "Edit" button in the ServicesPanel header. PUTs the
// override to cp-api which publishes config.update on the cmd channel;
// polls /devices/{id} until lastAppliedCorrelationId matches the one
// returned by the PUT (or a 30s timeout fires), then dismisses.
//
// Minimal styling — fixed overlay + centered card with inline styles
// so this slice doesn't touch globals.css. A future cycle can promote
// it to a real Dialog primitive if more modals land.
import { useEffect, useRef, useState } from "react";
import type { Device } from "../lib/api/devices";
import { getDevice, putServiceConfig } from "../lib/api/devices";

interface Props {
  device: Device;
  onClose: () => void;
  onApplied: () => void;
}

type Status =
  | { kind: "editing" }
  | { kind: "saving" }
  | { kind: "waiting"; correlationId: string }
  | { kind: "applied" }
  | { kind: "error"; message: string };

const POLL_INTERVAL_MS = 2000;
const POLL_TIMEOUT_MS = 30_000;

export function EditServicesModal({ device, onClose, onApplied }: Props) {
  const initialList = device.serviceConfig.allowListOverride ?? device.services.map((s) => s.name);
  const initialInterval = device.serviceConfig.intervalOverride ?? "5m";

  const [allowListText, setAllowListText] = useState(initialList.join("\n"));
  const [interval, setInterval_] = useState(initialInterval);
  const [status, setStatus] = useState<Status>({ kind: "editing" });
  const pollTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const timeoutTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (pollTimer.current) clearTimeout(pollTimer.current);
      if (timeoutTimer.current) clearTimeout(timeoutTimer.current);
    };
  }, []);

  const allowList = allowListText
    .split("\n")
    .map((s) => s.trim())
    .filter((s) => s.length > 0);

  async function pollUntilApplied(correlationId: string) {
    timeoutTimer.current = setTimeout(() => {
      setStatus({
        kind: "error",
        message:
          "Override saved on the control plane but no agent ACK within 30s. The device may be offline; the change will apply on next reconnect.",
      });
    }, POLL_TIMEOUT_MS);

    const tick = async () => {
      try {
        const fresh = await getDevice(device.deviceId);
        if (fresh.serviceConfig.lastAppliedCorrelationId === correlationId) {
          if (timeoutTimer.current) clearTimeout(timeoutTimer.current);
          setStatus({ kind: "applied" });
          onApplied();
          setTimeout(onClose, 600);
          return;
        }
      } catch {
        // Transient poll failure — try again on the next interval.
      }
      pollTimer.current = setTimeout(tick, POLL_INTERVAL_MS);
    };
    pollTimer.current = setTimeout(tick, POLL_INTERVAL_MS);
  }

  async function handleSave(e: React.FormEvent) {
    e.preventDefault();
    setStatus({ kind: "saving" });
    try {
      const { correlationId } = await putServiceConfig(device.deviceId, {
        serviceAllowList: allowList,
        serviceStatusInterval: interval,
      });
      setStatus({ kind: "waiting", correlationId });
      void pollUntilApplied(correlationId);
    } catch (err) {
      setStatus({
        kind: "error",
        message: err instanceof Error ? err.message : "save failed",
      });
    }
  }

  const isBusy = status.kind === "saving" || status.kind === "waiting";

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Edit tracked services"
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0, 0, 0, 0.45)",
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        zIndex: 100,
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget && !isBusy) onClose();
      }}
    >
      <form
        onSubmit={handleSave}
        style={{
          background: "var(--bg)",
          color: "var(--ink)",
          padding: 24,
          borderRadius: 8,
          width: 480,
          maxWidth: "92vw",
          boxShadow: "0 16px 48px rgba(0,0,0,0.25)",
        }}
      >
        <h2 style={{ margin: 0, marginBottom: 14, fontSize: 18 }}>
          Edit tracked services
        </h2>
        <p className="muted" style={{ fontSize: 12.5, marginTop: 0 }}>
          One service name per line. Saving pushes a config.update to the
          agent; status flips to &quot;applied&quot; once the device ACKs
          (typically within seconds).
        </p>

        <label style={{ display: "block", marginTop: 12 }}>
          <div className="card-section-label" style={{ marginBottom: 4 }}>
            Services
          </div>
          <textarea
            value={allowListText}
            onChange={(e) => setAllowListText(e.target.value)}
            disabled={isBusy}
            rows={6}
            style={{
              width: "100%",
              fontFamily: "var(--mono, monospace)",
              fontSize: 13,
              padding: 8,
              border: "1px solid var(--line, #ccc)",
              borderRadius: 4,
              boxSizing: "border-box",
            }}
            aria-label="Service allow-list"
          />
        </label>

        <label style={{ display: "block", marginTop: 12 }}>
          <div className="card-section-label" style={{ marginBottom: 4 }}>
            Reporting interval
          </div>
          <input
            type="text"
            value={interval}
            onChange={(e) => setInterval_(e.target.value)}
            disabled={isBusy}
            placeholder="e.g. 5m, 30s, 2m"
            style={{
              width: 140,
              fontSize: 13,
              padding: "6px 8px",
              border: "1px solid var(--line, #ccc)",
              borderRadius: 4,
            }}
            aria-label="Reporting interval"
          />
          <span className="muted" style={{ fontSize: 12, marginLeft: 8 }}>
            Range: 30s &ndash; 1h.
          </span>
        </label>

        {status.kind === "saving" && (
          <p className="muted" style={{ fontSize: 12.5, marginTop: 14 }}>
            Saving&hellip;
          </p>
        )}
        {status.kind === "waiting" && (
          <p className="muted" style={{ fontSize: 12.5, marginTop: 14 }}>
            Waiting for agent ACK&hellip;
          </p>
        )}
        {status.kind === "applied" && (
          <p style={{ fontSize: 12.5, marginTop: 14, color: "green" }}>
            Applied.
          </p>
        )}
        {status.kind === "error" && (
          <p
            role="alert"
            style={{ fontSize: 12.5, marginTop: 14, color: "var(--red, #c33)" }}
          >
            {status.message}
          </p>
        )}

        <div
          style={{
            display: "flex",
            gap: 8,
            justifyContent: "flex-end",
            marginTop: 18,
          }}
        >
          <button
            type="button"
            onClick={onClose}
            disabled={isBusy}
            style={{
              padding: "6px 14px",
              fontSize: 13,
              background: "transparent",
              border: "1px solid var(--line, #ccc)",
              borderRadius: 4,
              cursor: "pointer",
            }}
          >
            {status.kind === "applied" ? "Close" : "Cancel"}
          </button>
          <button
            type="submit"
            disabled={isBusy || status.kind === "applied"}
            style={{
              padding: "6px 14px",
              fontSize: 13,
              background: "var(--ink)",
              color: "var(--ink-on-dark)",
              border: 0,
              borderRadius: 4,
              cursor: isBusy ? "wait" : "pointer",
            }}
          >
            Save
          </button>
        </div>
      </form>
    </div>
  );
}
