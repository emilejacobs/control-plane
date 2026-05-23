// Segmented control — the pill-shaped filter strip used on the fleet view
// (Presence / Cert filter) and elsewhere when a small set of mutually
// exclusive options needs inline selection.

export interface SegmentedOption<T extends string> {
  value: T;
  label: string;
  badge?: number;
}

interface Props<T extends string> {
  value: T;
  onChange: (next: T) => void;
  options: SegmentedOption<T>[];
}

export function Segmented<T extends string>({ value, onChange, options }: Props<T>) {
  return (
    <div className="segmented" role="tablist">
      {options.map((o) => (
        <button
          key={o.value}
          role="tab"
          aria-selected={value === o.value}
          className={value === o.value ? "is-active" : ""}
          onClick={() => onChange(o.value)}
          type="button"
        >
          {o.label}
          {o.badge != null && (
            <span className="muted" style={{ marginLeft: 6 }}>
              {o.badge}
            </span>
          )}
        </button>
      ))}
    </div>
  );
}
