// PRTokenSettingsCard renders the account-wide Plate Recognizer token section
// of the Settings page (#84). Staff-only: a non-staff operator's 403 hides the
// card. The token is a secret — the card shows only set/not-set and lets staff
// (re)enter it; the value is never displayed.
"use client";

import { useState } from "react";
import { Card } from "./ui/Card";
import { usePRTokenStatus, useSetPRToken } from "../lib/api/hooks";
import { ApiError } from "../lib/api/auth";

export function PRTokenSettingsCard() {
  const status = usePRTokenStatus();
  const setToken = useSetPRToken();
  const [token, setTokenInput] = useState("");

  // Non-staff (or surface down): hide the card so the rest of Settings renders.
  if (status.error) {
    if (status.error instanceof ApiError && status.error.status === 403) return null;
    return null;
  }
  if (status.isLoading || !status.data) return null;

  const isSet = status.data.isSet;
  const errorMessage = setToken.error instanceof Error ? setToken.error.message : null;

  const onSave = () => {
    if (!token) return;
    setToken.mutate(token, { onSuccess: () => setTokenInput("") });
  };

  return (
    <Card label="Plate Recognizer token">
      <p className="muted">
        Account-wide token pushed to devices at Commission. Stored as a secret —
        never displayed.
      </p>
      <p>
        Status: <strong>{isSet ? "Set" : "Not set"}</strong>
      </p>
      <div className="row" style={{ gap: 8, alignItems: "center" }}>
        <input
          type="password"
          aria-label="Plate Recognizer token"
          placeholder={isSet ? "Replace token…" : "Enter token…"}
          value={token}
          disabled={setToken.isPending}
          onChange={(e) => setTokenInput(e.target.value)}
          style={{
            fontSize: 13,
            padding: "4px 8px",
            border: "1px solid var(--line, #ccc)",
            borderRadius: 4,
            minWidth: 280,
          }}
        />
        <button
          type="button"
          className="btn"
          onClick={onSave}
          disabled={setToken.isPending || token === ""}
        >
          Save
        </button>
        {setToken.isPending && (
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
    </Card>
  );
}
