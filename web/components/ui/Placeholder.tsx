// Placeholder — striped tile used where a Phase-2 feature lives but has no
// data yet (e.g. the Overview 24h activity chart, the device-detail
// Cameras panel). The label keeps the placeholder honest about what is
// missing rather than implying a future state.

interface Props {
  label: string;
  height?: number;
  ratio?: string;
}

export function Placeholder({ label, height, ratio = "16/9" }: Props) {
  return (
    <div
      style={{
        aspectRatio: height ? undefined : ratio,
        height,
        background:
          "repeating-linear-gradient(45deg, oklch(94% 0.005 250) 0 8px, oklch(96% 0.005 250) 8px 16px)",
        borderRadius: "var(--r-md)",
        display: "grid",
        placeItems: "center",
        color: "var(--ink-3)",
        fontFamily: "var(--font-mono)",
        fontSize: 11,
        border: "1px solid var(--line)",
        textAlign: "center",
        padding: 12,
      }}
    >
      {label}
    </div>
  );
}
