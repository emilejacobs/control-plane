// Dot is the small colored status indicator used in pills, presence chips,
// fleet rows, and the activity timeline. Tones map to design tokens in
// globals.css (.dot.green / .amber / .red / .gray).

export type DotTone = "green" | "amber" | "red" | "gray";

export function Dot({ tone = "gray" }: { tone?: DotTone }) {
  return <span className={`dot ${tone}`} aria-hidden />;
}
