// Gauge — reusable radial-arc KPI gauge for the fleet overview (#151).
//
// A 270° arc whose fill length encodes value/max and whose colour encodes a
// health tone (green healthy / amber attention). The percentage + count render
// in the centre; label + sub sit below. Pure presentational — no data fetching,
// so every overview gauge (devices, version, cameras, services) shares one
// tested implementation.

export type GaugeTone = "success" | "warning" | "danger" | "neutral";

interface GaugeProps {
  value: number;
  max: number;
  label: string;
  sub?: string;
  tone?: GaugeTone;
}

// 270° (three-quarter) arc of an r=46 circle: circumference 2π·46 ≈ 289.03,
// of which 0.75 ≈ 216.77 is drawn. The bottom quarter is the open gap.
const CIRC = 289.03;
const ARC = 216.77;

const ARC_COLOR: Record<GaugeTone, string> = {
  success: "var(--green)",
  warning: "var(--amber)",
  danger: "var(--red)",
  neutral: "var(--ink-4)",
};

const SUB_COLOR: Record<GaugeTone, string> = {
  success: "var(--green-ink)",
  warning: "var(--amber-ink)",
  danger: "var(--red-ink)",
  neutral: "var(--ink-3)",
};

// gaugeTone maps a 0..1 ratio to a health tone: green at/above the healthy
// threshold, amber below. Centralised so every overview gauge colours
// consistently (PRD: fixed green/amber threshold, no red tier on the gauges).
export function gaugeTone(ratio: number, healthy = 0.95): GaugeTone {
  return ratio >= healthy ? "success" : "warning";
}

export function Gauge({ value, max, label, sub, tone = "neutral" }: GaugeProps) {
  const ratio = max > 0 ? Math.min(1, Math.max(0, value / max)) : 0;
  const pct = Math.round(ratio * 100);
  return (
    <div className="gauge-card">
      <svg
        className="gauge"
        viewBox="0 0 120 106"
        role="img"
        aria-label={`${label}: ${pct}% (${value} of ${max})`}
      >
        <circle
          cx="60" cy="54" r="46" fill="none"
          stroke="var(--line-2)" strokeWidth="10" strokeLinecap="round"
          strokeDasharray={`${ARC} ${CIRC}`}
          transform="rotate(135 60 54)"
        />
        <circle
          cx="60" cy="54" r="46" fill="none"
          stroke={ARC_COLOR[tone]} strokeWidth="10" strokeLinecap="round"
          strokeDasharray={`${(ARC * ratio).toFixed(2)} ${CIRC}`}
          transform="rotate(135 60 54)"
        />
        <text x="60" y="51" textAnchor="middle" className="gauge-pct">{pct}%</text>
        <text x="60" y="69" textAnchor="middle" className="gauge-count">{value} / {max}</text>
      </svg>
      <div className="gauge-label">{label}</div>
      {sub && (
        <div className="gauge-sub" style={{ color: SUB_COLOR[tone] }}>{sub}</div>
      )}
    </div>
  );
}
