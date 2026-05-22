// formatAgo renders the gap between two instants as a human ago-string —
// "12 seconds ago", "3 minutes ago", "2 hours ago", "5 days ago". The
// per-device view (Issue 18) recomputes it client-side so it ticks between
// the 10s polls. A future instant clamps to "0 seconds ago".

function plural(n: number, unit: string): string {
  return `${n} ${unit}${n === 1 ? "" : "s"} ago`;
}

export function formatAgo(from: Date, now: Date): string {
  const seconds = Math.max(0, Math.floor((now.getTime() - from.getTime()) / 1000));
  if (seconds < 60) return plural(seconds, "second");
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return plural(minutes, "minute");
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return plural(hours, "hour");
  return plural(Math.floor(hours / 24), "day");
}
