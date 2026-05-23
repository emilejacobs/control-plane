// PresenceChip — green dot + "Online" / gray dot + "Offline".
// Restyled to use the design-token .presence + .dot classes from
// globals.css. The "Online" / "Offline" text is exact (tests query it),
// and aria-label still spells the state out for screen readers.

interface Props {
  online: boolean;
}

export function PresenceChip({ online }: Props) {
  return (
    <span
      className={`presence ${online ? "online" : "offline"}`}
      aria-label={online ? "online" : "offline"}
    >
      <span className={`dot ${online ? "green" : "gray"}`} aria-hidden />
      <span className="label">{online ? "Online" : "Offline"}</span>
    </span>
  );
}
