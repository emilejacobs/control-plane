"use client";

// LogsPanel — operator-driven on-demand log tail (Phase 2 slice 3).
// Lives on the per-device page. Picks a log name from the device's
// allow-list, clicks Fetch, polls until the agent ACKs (or 30s
// timeout), renders the lines in a scrollable <pre>.
//
// Allow-list is hardcoded here to match the Mac per-OS list in
// internal/agent/logtail.go PerOSAllowList(). Per-device variation
// (Linux, custom paths) is a future slice — keep the dashboard
// synced manually until then.
import { useEffect, useRef, useState } from "react";
import { postLogTail, getLogTail, type LogTail } from "../lib/api/devices";

interface Props {
  deviceId: string;
}

// Mirrors internal/agent/logtail.go PerOSAllowList()["darwin"] keys.
// Add a Linux variant when the Linux fleet matters operationally.
const MAC_LOGS: ReadonlyArray<{ name: string; label: string }> = [
  { name: "agent", label: "uknomi-agent (stdout)" },
  { name: "agent-error", label: "uknomi-agent (stderr / slog)" },
  { name: "webui", label: "Edge UI (stdout)" },
  { name: "webui-error", label: "Edge UI (stderr)" },
  { name: "setup", label: "Setup script" },
  { name: "install", label: "macOS installer" },
  { name: "activation", label: "Edge UI activation" },
];

type FetchState =
  | { kind: "idle" }
  | { kind: "starting" }
  | { kind: "polling"; correlationId: string }
  | { kind: "done"; tail: LogTail }
  | { kind: "error"; message: string };

const POLL_INTERVAL_MS = 2000;
const POLL_TIMEOUT_MS = 30_000;

export function LogsPanel({ deviceId }: Props) {
  const [logName, setLogName] = useState<string>(MAC_LOGS[0].name);
  const [lines, setLines] = useState<number>(200);
  const [state, setState] = useState<FetchState>({ kind: "idle" });
  const pollTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const timeoutTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    return () => {
      if (pollTimer.current) clearTimeout(pollTimer.current);
      if (timeoutTimer.current) clearTimeout(timeoutTimer.current);
    };
  }, []);

  async function pollUntilDone(correlationId: string) {
    timeoutTimer.current = setTimeout(() => {
      setState({
        kind: "error",
        message: `Timed out waiting for agent ACK after 30s (correlation_id ${correlationId}). The device may be offline; the request stays pending and the agent will fulfill on next reconnect, but the dashboard stopped polling.`,
      });
    }, POLL_TIMEOUT_MS);

    const tick = async () => {
      try {
        const tail = await getLogTail(deviceId, correlationId);
        if (tail.status === "done" || tail.status === "error") {
          if (timeoutTimer.current) clearTimeout(timeoutTimer.current);
          setState({ kind: "done", tail });
          return;
        }
      } catch {
        // Transient poll error — try again next interval.
      }
      pollTimer.current = setTimeout(tick, POLL_INTERVAL_MS);
    };
    pollTimer.current = setTimeout(tick, POLL_INTERVAL_MS);
  }

  async function handleFetch(e: React.FormEvent) {
    e.preventDefault();
    if (pollTimer.current) clearTimeout(pollTimer.current);
    if (timeoutTimer.current) clearTimeout(timeoutTimer.current);
    setState({ kind: "starting" });
    try {
      const { correlationId } = await postLogTail(deviceId, { logName, lines });
      setState({ kind: "polling", correlationId });
      void pollUntilDone(correlationId);
    } catch (err) {
      setState({
        kind: "error",
        message: err instanceof Error ? err.message : "fetch failed",
      });
    }
  }

  const busy = state.kind === "starting" || state.kind === "polling";

  return (
    <div>
      <form
        onSubmit={handleFetch}
        style={{ display: "flex", gap: 8, alignItems: "end", flexWrap: "wrap" }}
      >
        <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          <span className="card-section-label">Log</span>
          <select
            value={logName}
            onChange={(e) => setLogName(e.target.value)}
            disabled={busy}
            style={{
              padding: "6px 8px",
              fontSize: 13,
              border: "1px solid var(--line, #ccc)",
              borderRadius: 4,
              minWidth: 220,
            }}
            aria-label="Log name"
          >
            {MAC_LOGS.map((l) => (
              <option key={l.name} value={l.name}>
                {l.label}
              </option>
            ))}
          </select>
        </label>
        <label style={{ display: "flex", flexDirection: "column", gap: 4 }}>
          <span className="card-section-label">Lines</span>
          <input
            type="number"
            value={lines}
            onChange={(e) => setLines(Number(e.target.value))}
            min={1}
            max={500}
            disabled={busy}
            style={{
              width: 80,
              padding: "6px 8px",
              fontSize: 13,
              border: "1px solid var(--line, #ccc)",
              borderRadius: 4,
            }}
            aria-label="Lines to fetch"
          />
        </label>
        <button
          type="submit"
          disabled={busy}
          style={{
            padding: "7px 16px",
            fontSize: 13,
            background: "var(--ink)",
            color: "var(--ink-on-dark)",
            border: 0,
            borderRadius: 4,
            cursor: busy ? "wait" : "pointer",
          }}
        >
          {state.kind === "starting" || state.kind === "polling"
            ? "Fetching…"
            : "Fetch"}
        </button>
      </form>

      <div style={{ marginTop: 14 }}>
        {state.kind === "idle" && (
          <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
            Pick a log and click Fetch to read the last N lines from the device.
            Request times out after 30s if the device is offline.
          </p>
        )}
        {state.kind === "starting" && (
          <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
            Sending request to agent&hellip;
          </p>
        )}
        {state.kind === "polling" && (
          <p className="muted" style={{ fontSize: 12.5, margin: 0 }}>
            Waiting for agent ACK&hellip; (corr_id {state.correlationId})
          </p>
        )}
        {state.kind === "error" && (
          <p
            role="alert"
            style={{ fontSize: 12.5, margin: 0, color: "var(--red, #c33)" }}
          >
            {state.message}
          </p>
        )}
        {state.kind === "done" && state.tail.status === "error" && (
          <p
            role="alert"
            style={{ fontSize: 12.5, margin: 0, color: "var(--red, #c33)" }}
          >
            Agent returned error: {state.tail.errorCode}
            {state.tail.errorMessage ? ` — ${state.tail.errorMessage}` : ""}
          </p>
        )}
        {state.kind === "done" && state.tail.status === "done" && (
          <div>
            {state.tail.truncated && (
              <p
                className="muted"
                style={{
                  fontSize: 12,
                  margin: "0 0 6px 0",
                  color: "var(--amber, #b87400)",
                }}
              >
                Truncated to fit a single response (requested{" "}
                {state.tail.truncatedFrom} lines; most recent bytes shown).
              </p>
            )}
            <pre
              style={{
                background: "var(--bg-tinted, #f5f5f0)",
                padding: 10,
                fontSize: 12,
                fontFamily: "var(--mono, monospace)",
                whiteSpace: "pre-wrap",
                wordBreak: "break-word",
                maxHeight: "60vh",
                overflow: "auto",
                margin: 0,
                border: "1px solid var(--line, #ddd)",
                borderRadius: 4,
              }}
            >
              {state.tail.content ?? ""}
            </pre>
          </div>
        )}
      </div>
    </div>
  );
}
