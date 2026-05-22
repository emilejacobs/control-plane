// PresenceChip shows a device's online/offline state at a glance: a green
// dot for online, gray for offline, with the state spelled out alongside.
export function PresenceChip({ online }: { online: boolean }) {
  return (
    <span aria-label={online ? "online" : "offline"}>
      <span aria-hidden style={{ color: online ? "#16a34a" : "#9ca3af" }}>
        ●
      </span>{" "}
      {online ? "Online" : "Offline"}
    </span>
  );
}
