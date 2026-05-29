// OperatorsManager is the staff-only /operators surface (#16): list every CP
// operator, add a new one (revealing the system-generated temp password
// once), edit role / site allowlist / reset TOTP, reset password, and
// deactivate / reactivate. Non-staff callers get 403 from the API, which we
// render as a "staff only" notice (mirrors the dashboard's 403-gate pattern).
"use client";

import { useMemo, useState } from "react";
import {
  useOperators,
  useCreateOperator,
  useUpdateOperator,
  useSetOperatorActive,
  useSitesTree,
} from "../lib/api/hooks";
import { ApiError } from "../lib/api/auth";
import type { Operator } from "../lib/api/operators";
import type { ClientWithSites } from "../lib/api/taxonomy";
import { Card } from "./ui/Card";
import { Pill } from "./ui/Pill";
import { Dot } from "./ui/Dot";

export function OperatorsManager() {
  const operators = useOperators();
  const sites = useSitesTree();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Operator | null>(null);
  // tempPassword holds a freshly-generated temp password to reveal once.
  const [tempPassword, setTempPassword] = useState<{ email: string; pw: string } | null>(null);

  if (operators.error instanceof ApiError && operators.error.status === 403) {
    return (
      <Card label="Operators">
        <p className="muted" style={{ margin: 0 }}>
          You need staff access to manage operators.
        </p>
      </Card>
    );
  }

  return (
    <>
      {tempPassword && (
        <TempPasswordNotice
          email={tempPassword.email}
          password={tempPassword.pw}
          onDismiss={() => setTempPassword(null)}
        />
      )}

      <Card
        label="Operators"
        flush
        actions={
          <button className="btn primary small" onClick={() => setAdding(true)}>
            Add operator
          </button>
        }
      >
        {operators.isPending ? (
          <div className="muted" style={{ padding: 16 }}>
            Loading operators…
          </div>
        ) : (
          <table className="table">
            <thead>
              <tr>
                <th>Email</th>
                <th>Role</th>
                <th>2FA</th>
                <th>Sites</th>
                <th>State</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {(operators.data ?? []).map((op) => (
                <tr key={op.id}>
                  <td style={{ fontWeight: 600 }}>{op.email}</td>
                  <td>
                    <Pill tone={op.isStaff ? "green" : "neutral"}>
                      {op.isStaff ? "Staff" : "Scoped"}
                    </Pill>
                  </td>
                  <td>
                    <Pill tone={op.totpEnrolled ? "green" : "amber"}>
                      {op.totpEnrolled ? "enrolled" : "pending"}
                    </Pill>
                  </td>
                  <td className="tabular">{op.isStaff ? "all" : op.siteIds.length}</td>
                  <td>
                    <span className="row" style={{ gap: 6 }}>
                      <Dot tone={op.deactivated ? "gray" : "green"} />
                      {op.deactivated ? "Deactivated" : "Active"}
                    </span>
                  </td>
                  <td style={{ textAlign: "right" }}>
                    <button className="btn ghost small" onClick={() => setEditing(op)}>
                      Edit
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>

      {adding && (
        <CreateOperatorModal
          clients={sites.data ?? []}
          onClose={() => setAdding(false)}
          onCreated={(email, pw) => {
            setAdding(false);
            setTempPassword({ email, pw });
          }}
        />
      )}
      {editing && (
        <EditOperatorModal
          operator={editing}
          clients={sites.data ?? []}
          onClose={() => setEditing(null)}
          onTempPassword={(email, pw) => {
            setEditing(null);
            setTempPassword({ email, pw });
          }}
        />
      )}
    </>
  );
}

function TempPasswordNotice({
  email,
  password,
  onDismiss,
}: {
  email: string;
  password: string;
  onDismiss: () => void;
}) {
  return (
    <Card label="Temporary password" className="temp-pw-notice">
      <p style={{ marginTop: 0 }}>
        Share this one-time password with <strong>{email}</strong> over a secure
        channel. It won’t be shown again — they’ll set their own on first login.
      </p>
      <code className="input mono" style={{ display: "block", padding: "8px 12px" }}>
        {password}
      </code>
      <div className="row" style={{ marginTop: 12 }}>
        <button className="btn" onClick={() => navigator.clipboard?.writeText(password)}>
          Copy
        </button>
        <button className="btn primary" onClick={onDismiss}>
          Done
        </button>
      </div>
    </Card>
  );
}

// SitePicker renders client-grouped site checkboxes for the allowlist.
function SitePicker({
  clients,
  selected,
  onToggle,
}: {
  clients: ClientWithSites[];
  selected: Set<string>;
  onToggle: (siteId: string) => void;
}) {
  return (
    <div style={{ maxHeight: 200, overflowY: "auto", border: "1px solid var(--line)", borderRadius: 8, padding: 8 }}>
      {clients.length === 0 && <p className="muted" style={{ margin: 4 }}>No sites available.</p>}
      {clients.map((c) => (
        <div key={c.id} style={{ marginBottom: 6 }}>
          <div className="card-section-label" style={{ margin: "2px 0" }}>
            {c.name}
          </div>
          {c.sites.map((s) => (
            <label key={s.id} className="row" style={{ gap: 8, fontSize: 13, padding: "2px 4px", cursor: "pointer" }}>
              <input type="checkbox" checked={selected.has(s.id)} onChange={() => onToggle(s.id)} />
              <span>{s.name}</span>
            </label>
          ))}
        </div>
      ))}
    </div>
  );
}

function CreateOperatorModal({
  clients,
  onClose,
  onCreated,
}: {
  clients: ClientWithSites[];
  onClose: () => void;
  onCreated: (email: string, tempPassword: string) => void;
}) {
  const create = useCreateOperator();
  const [email, setEmail] = useState("");
  const [isStaff, setIsStaff] = useState(false);
  const [sites, setSites] = useState<Set<string>>(new Set());

  function toggle(id: string) {
    setSites((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function submit() {
    create.mutate(
      { email, isStaff, siteIds: isStaff ? [] : [...sites] },
      { onSuccess: (res) => onCreated(res.operator.email, res.tempPassword) },
    );
  }

  return (
    <ModalShell title="Add operator" onClose={onClose}>
      <label className="field">
        <span className="label">Email</span>
        <input className="input" type="email" value={email} aria-label="Email"
          onChange={(e) => setEmail(e.target.value)} />
      </label>
      <label className="row" style={{ gap: 8, margin: "8px 0" }}>
        <input type="checkbox" checked={isStaff} onChange={(e) => setIsStaff(e.target.checked)} />
        <span>Staff (full fleet access)</span>
      </label>
      {!isStaff && (
        <>
          <span className="label">Site allowlist</span>
          <SitePicker clients={clients} selected={sites} onToggle={toggle} />
        </>
      )}
      {create.isError && (
        <p role="alert" className="pill red" style={{ display: "inline-flex", marginTop: 10 }}>
          {create.error instanceof ApiError && create.error.status === 409
            ? "That email is already in use."
            : "Could not create operator."}
        </p>
      )}
      <div className="row" style={{ marginTop: 14, justifyContent: "flex-end", gap: 8 }}>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn primary" disabled={!email || create.isPending} onClick={submit}>
          {create.isPending ? "Creating…" : "Create operator"}
        </button>
      </div>
    </ModalShell>
  );
}

function EditOperatorModal({
  operator,
  clients,
  onClose,
  onTempPassword,
}: {
  operator: Operator;
  clients: ClientWithSites[];
  onClose: () => void;
  onTempPassword: (email: string, tempPassword: string) => void;
}) {
  const update = useUpdateOperator(operator.id);
  const setActive = useSetOperatorActive(operator.id);
  const [isStaff, setIsStaff] = useState(operator.isStaff);
  const [sites, setSites] = useState<Set<string>>(new Set(operator.siteIds));
  const [resetTotp, setResetTotp] = useState(false);

  const dirty = useMemo(
    () =>
      isStaff !== operator.isStaff ||
      resetTotp ||
      !sameSet(sites, new Set(operator.siteIds)),
    [isStaff, resetTotp, sites, operator],
  );

  function toggle(id: string) {
    setSites((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function save() {
    update.mutate(
      { isStaff, siteIds: isStaff ? [] : [...sites], resetTotp },
      { onSuccess: () => onClose() },
    );
  }

  function resetPassword() {
    update.mutate(
      { resetPassword: true },
      { onSuccess: (res) => res.tempPassword && onTempPassword(operator.email, res.tempPassword) },
    );
  }

  return (
    <ModalShell title={`Edit ${operator.email}`} onClose={onClose}>
      <label className="row" style={{ gap: 8, margin: "4px 0" }}>
        <input type="checkbox" checked={isStaff} onChange={(e) => setIsStaff(e.target.checked)} />
        <span>Staff (full fleet access)</span>
      </label>
      {!isStaff && (
        <>
          <span className="label">Site allowlist</span>
          <SitePicker clients={clients} selected={sites} onToggle={toggle} />
        </>
      )}
      <label className="row" style={{ gap: 8, margin: "10px 0" }}>
        <input type="checkbox" checked={resetTotp} onChange={(e) => setResetTotp(e.target.checked)} />
        <span>Reset 2FA (force re-enrollment)</span>
      </label>

      <div className="row" style={{ gap: 8, marginTop: 6, flexWrap: "wrap" }}>
        <button className="btn" onClick={resetPassword} disabled={update.isPending}>
          Reset password
        </button>
        <button
          className="btn"
          onClick={() => setActive.mutate(operator.deactivated, { onSuccess: onClose })}
          disabled={setActive.isPending}
        >
          {operator.deactivated ? "Reactivate" : "Deactivate"}
        </button>
      </div>

      <div className="row" style={{ marginTop: 14, justifyContent: "flex-end", gap: 8 }}>
        <button className="btn" onClick={onClose}>Cancel</button>
        <button className="btn primary" disabled={!dirty || update.isPending} onClick={save}>
          {update.isPending ? "Saving…" : "Save changes"}
        </button>
      </div>
    </ModalShell>
  );
}

function ModalShell({
  title,
  onClose,
  children,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
}) {
  return (
    <div
      role="dialog"
      aria-label={title}
      style={{
        position: "fixed",
        inset: 0,
        background: "rgba(0,0,0,0.3)",
        display: "grid",
        placeItems: "center",
        zIndex: 50,
      }}
      onClick={onClose}
    >
      <div
        className="card"
        style={{ width: "min(440px, 92vw)", maxHeight: "90vh", overflowY: "auto" }}
        onClick={(e) => e.stopPropagation()}
      >
        <h2 className="card-section-label" style={{ marginTop: 0 }}>
          {title}
        </h2>
        {children}
      </div>
    </div>
  );
}

function sameSet(a: Set<string>, b: Set<string>): boolean {
  if (a.size !== b.size) return false;
  for (const x of a) if (!b.has(x)) return false;
  return true;
}
