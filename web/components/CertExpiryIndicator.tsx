// CertExpiryIndicator surfaces a device's mTLS cert expiry on the
// per-device view — the early-warning signal for cert rotation (ADR-013,
// Issue 09). Restyled to consume the design-token color band from
// globals.css; the text shape is preserved verbatim so the page-level
// test (data-cert-status, "Certificate expires", "365 days") still
// passes after the visual restyle.

interface Props {
  expiresAt: string | null;
  daysRemaining: number | null;
}

type CertStatus = "green" | "yellow" | "red";

// certStatus maps days-remaining to a color band (Issue 09): green when
// comfortably ahead, yellow approaching, red when expiry is imminent or past.
function certStatus(daysRemaining: number): CertStatus {
  if (daysRemaining > 180) return "green";
  if (daysRemaining >= 30) return "yellow";
  return "red";
}

const statusColor: Record<CertStatus, string> = {
  green: "var(--green-ink)",
  yellow: "var(--amber-ink)",
  red: "var(--red-ink)",
};

export function CertExpiryIndicator({ expiresAt, daysRemaining }: Props) {
  // A row predating the cert-expiry migration carries no notAfter; say so
  // rather than leaving a silent gap where the indicator should be.
  if (expiresAt == null || daysRemaining == null) {
    return <p className="muted" style={{ margin: 0 }}>Certificate expiry unknown</p>;
  }

  const status = certStatus(daysRemaining);
  const unit = Math.abs(daysRemaining) === 1 ? "day" : "days";
  return (
    <p data-cert-status={status} style={{ margin: 0, fontSize: 13.5 }}>
      Certificate expires{" "}
      <time className="mono" dateTime={expiresAt}>
        {expiresAt.slice(0, 10)}
      </time>{" "}
      (
      <span style={{ color: statusColor[status], fontWeight: 600 }}>
        {daysRemaining} {unit}
      </span>{" "}
      remaining)
    </p>
  );
}
