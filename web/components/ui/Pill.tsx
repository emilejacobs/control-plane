// Pill renders the small uppercase-label status chip used for cert status,
// agent-drift callouts, and presence rollups. Tones match the design tokens.
import type { ReactNode } from "react";

export type PillTone = "green" | "amber" | "red" | "neutral";

export function Pill({
  tone = "neutral",
  children,
}: {
  tone?: PillTone;
  children: ReactNode;
}) {
  return <span className={`pill ${tone}`}>{children}</span>;
}
