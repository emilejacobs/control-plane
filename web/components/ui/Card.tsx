// Card is the section container used across the dashboard. `flush` removes
// the default padding so a table / list can run edge-to-edge inside, while
// still carrying a labeled header row.
import type { ReactNode } from "react";

interface Props {
  label?: ReactNode;
  actions?: ReactNode;
  flush?: boolean;
  className?: string;
  children: ReactNode;
}

export function Card({ label, actions, flush, className = "", children }: Props) {
  const cls = `card ${flush ? "flush " : ""}${className}`.trim();
  if (flush && label) {
    return (
      <section className={cls}>
        <header className="card-head">
          <div className="card-section-label" style={{ margin: 0 }}>
            {label}
          </div>
          <div className="spacer" />
          {actions}
        </header>
        {children}
      </section>
    );
  }
  if (label) {
    return (
      <section className={cls}>
        <div className="row" style={{ marginBottom: 14 }}>
          <div className="card-section-label" style={{ margin: 0 }}>
            {label}
          </div>
          <div className="spacer" />
          {actions}
        </div>
        {children}
      </section>
    );
  }
  return <section className={cls}>{children}</section>;
}
