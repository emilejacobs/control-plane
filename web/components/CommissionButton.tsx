// CommissionButton triggers Commission on an assigned device (#91). The CP
// mints a Tailscale key, gathers the ALPR license/token, and pushes cameras +
// the secrets to the device. Disabled until a site is assigned (Assign precedes
// Commission, ADR-036).
"use client";

import { useCommissionDevice } from "../lib/api/hooks";

interface Props {
  deviceId: string;
  assigned: boolean;
}

export function CommissionButton({ deviceId, assigned }: Props) {
  const commission = useCommissionDevice(deviceId);
  const errorMessage = commission.error instanceof Error ? commission.error.message : null;

  return (
    <div className="row" style={{ gap: 8, alignItems: "center" }}>
      <button
        type="button"
        className="btn"
        disabled={!assigned || commission.isPending}
        onClick={() => commission.mutate()}
      >
        {commission.isPending ? "Commissioning…" : "Commission"}
      </button>
      {!assigned && <span className="muted">Assign a site first</span>}
      {commission.isSuccess && (
        <span className="muted" role="status">
          Commission started
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
