// KV renders the key/value definition list the System / Deployment / mTLS
// Cert cards use. Each row is a (term, value, mono?) tuple. The per-device
// view's tests query <dt>/<dd> pairs by label, so the structure stays a
// plain <dl> regardless of styling.
import type { ReactNode } from "react";

export type KVItem = [label: string, value: ReactNode, mono?: boolean];

export function KV({ items }: { items: KVItem[] }) {
  return (
    <dl className="kv">
      {items.map(([k, v, mono]) => (
        <KVRow key={k} label={k} value={v} mono={mono} />
      ))}
    </dl>
  );
}

function KVRow({ label, value, mono }: { label: string; value: ReactNode; mono?: boolean }) {
  return (
    <>
      <dt className="kv-key">{label}</dt>
      <dd className={`kv-val ${mono ? "mono" : ""}`}>{value}</dd>
    </>
  );
}
