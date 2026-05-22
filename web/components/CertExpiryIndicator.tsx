// CertExpiryIndicator surfaces a device's mTLS cert expiry on the per-device
// view — the early-warning signal for cert rotation (ADR-013, Issue 09). It
// shows the expiry date and the whole days remaining.

interface Props {
  expiresAt: string | null;
  daysRemaining: number | null;
}

export function CertExpiryIndicator({ expiresAt, daysRemaining }: Props) {
  if (expiresAt == null || daysRemaining == null) return null;

  const unit = Math.abs(daysRemaining) === 1 ? "day" : "days";
  return (
    <p>
      Certificate expires{" "}
      <time dateTime={expiresAt}>{expiresAt.slice(0, 10)}</time> (
      <span>
        {daysRemaining} {unit}
      </span>{" "}
      remaining)
    </p>
  );
}
