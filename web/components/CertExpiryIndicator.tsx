// CertExpiryIndicator surfaces a device's mTLS cert expiry on the per-device
// view — the early-warning signal for cert rotation (ADR-013, Issue 09). It
// shows the expiry date and the whole days remaining.

interface Props {
  expiresAt: string | null;
  daysRemaining: number | null;
}

type CertStatus = "green" | "yellow" | "red";

// certStatus maps days-remaining to a color band (Issue 09): green when
// comfortably ahead, yellow approaching, red when expiry is imminent or past.
// The threshold days (30, 180) fall in the safer adjacent band.
function certStatus(daysRemaining: number): CertStatus {
  if (daysRemaining > 180) return "green";
  if (daysRemaining >= 30) return "yellow";
  return "red";
}

const statusColor: Record<CertStatus, string> = {
  green: "#16a34a",
  yellow: "#ca8a04",
  red: "#dc2626",
};

export function CertExpiryIndicator({ expiresAt, daysRemaining }: Props) {
  if (expiresAt == null || daysRemaining == null) return null;

  const status = certStatus(daysRemaining);
  const unit = Math.abs(daysRemaining) === 1 ? "day" : "days";
  return (
    <p data-cert-status={status}>
      Certificate expires{" "}
      <time dateTime={expiresAt}>{expiresAt.slice(0, 10)}</time> (
      <span style={{ color: statusColor[status] }}>
        {daysRemaining} {unit}
      </span>{" "}
      remaining)
    </p>
  );
}
