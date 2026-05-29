"use client";

import { Topbar } from "../../components/ui/Topbar";
import { OperatorsManager } from "../../components/OperatorsManager";
import { RequireAuth } from "../../components/RequireAuth";

// /operators (#16) — staff-only management of CP operators: list, add (with a
// one-time generated temp password), edit role / site allowlist / reset 2FA,
// reset password, deactivate / reactivate. Non-staff callers are gated by the
// API's 403, which OperatorsManager renders as a "staff only" notice.
export default function OperatorsPage() {
  return (
    <RequireAuth>
      <Topbar />
      <main className="page">
        <div className="page-header">
          <div>
            <div className="crumbs">
              <span>uknomi</span>
              <span className="sep">/</span>
              <span style={{ color: "var(--ink)" }}>Operators</span>
            </div>
            <h1 className="page-title">Operators</h1>
            <p className="page-subtitle">
              People with access to this control plane.
            </p>
          </div>
        </div>
        <OperatorsManager />
      </main>
    </RequireAuth>
  );
}
