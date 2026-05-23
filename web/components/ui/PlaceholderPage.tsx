// PlaceholderPage — the standard "coming with Phase X" landing for nav
// items whose surface is not yet wired (Events, Operators, Settings).
// Keeps the topbar + page shell consistent so a deep-link or wrong-click
// reads as "the nav goes here, the surface lands later" rather than 404.
import { Topbar } from "./Topbar";
import { Card } from "./Card";
import { Placeholder } from "./Placeholder";

interface Props {
  title: string;
  subtitle: string;
  phase: string;
}

export function PlaceholderPage({ title, subtitle, phase }: Props) {
  return (
    <>
      <Topbar />
      <main className="page">
        <div className="page-header">
          <div>
            <div className="crumbs">
              <span>uknomi</span>
              <span className="sep">/</span>
              <span style={{ color: "var(--ink)" }}>{title}</span>
            </div>
            <h1 className="page-title">{title}</h1>
            <p className="page-subtitle">{subtitle}</p>
          </div>
        </div>
        <Card>
          <Placeholder label={`${title.toUpperCase()} · ${phase}`} height={280} />
          <p className="muted" style={{ marginTop: 14, fontSize: 13 }}>
            This surface is on the roadmap for {phase}. The nav slot is in
            place so deep-links keep working as it lands.
          </p>
        </Card>
      </main>
    </>
  );
}
