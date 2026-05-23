// Sparkline renders an SVG line+area chart from a normalized [0..1]-ish
// number array. Used on the Overview KPI tiles for the 24h trend strip.
// Aspect ratio is fixed; the container scales it.

interface Props {
  points: number[];
  color?: string;
}

export function Sparkline({ points, color = "var(--green)" }: Props) {
  if (!points.length) return null;
  const w = 100;
  const h = 28;
  const max = Math.max(...points);
  const min = Math.min(...points);
  const span = Math.max(0.001, max - min);
  const line = points
    .map((p, i) => {
      const x = (i / (points.length - 1)) * w;
      const y = h - ((p - min) / span) * h;
      return `${i === 0 ? "M" : "L"}${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(" ");
  const area = `${line} L${w},${h} L0,${h} Z`;
  return (
    <svg
      className="spark"
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      aria-hidden
    >
      <path d={area} fill={color} opacity="0.12" />
      <path
        d={line}
        fill="none"
        stroke={color}
        strokeWidth="1.4"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
    </svg>
  );
}
