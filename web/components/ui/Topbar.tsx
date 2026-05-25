// Topbar — sticky brand + primary nav for the authenticated dashboard.
// Active state is derived from the current pathname so a deep-link such as
// /devices/dev-1 still highlights "Fleet". The auth shells (login,
// first-run, totp-enroll) deliberately do not render the topbar — they
// have their own full-screen shell.
"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { Dot } from "./Dot";
import { clearTokens } from "../../lib/api/client";
import { logout } from "../../lib/api/auth";

interface NavItem {
  id: string;
  label: string;
  href: string;
  // Which path prefixes count as "this tab active". /devices/<id> is still
  // the Fleet tab; /overview is itself.
  prefixes: string[];
}

const NAV: NavItem[] = [
  { id: "overview", label: "Overview", href: "/overview", prefixes: ["/overview", "/"] },
  { id: "fleet", label: "Fleet", href: "/devices", prefixes: ["/devices"] },
  { id: "operators", label: "Operators", href: "/operators", prefixes: ["/operators"] },
  { id: "settings", label: "Settings", href: "/settings", prefixes: ["/settings"] },
];

function isActive(item: NavItem, pathname: string): boolean {
  // Root "/" only activates Overview; otherwise prefer the longest matching
  // prefix so /devices wins over "/" for the Fleet tab.
  if (pathname === "/") return item.id === "overview";
  return item.prefixes.some(
    (p) => p !== "/" && (pathname === p || pathname.startsWith(`${p}/`)),
  );
}

interface Props {
  userInitials?: string;
}

export function Topbar({ userInitials = "EJ" }: Props) {
  // usePathname returns null when called outside a Next.js routing
  // context — notably in the vitest tests that render pages in
  // isolation. Treat null as the root path so the test environment
  // does not crash on the active-state calc.
  const pathname = usePathname() ?? "/";
  const router = useRouter();

  async function onSignOut() {
    // Revoke the refresh token server-side first (fire-and-forget — see
    // logout()'s comment for why this is best-effort), then clear local
    // tokens and navigate. The order matters: clearing before revoking
    // would lose the refresh-token value we need to send.
    await logout();
    clearTokens();
    router.push("/login");
  }

  return (
    <header className="topbar">
      <Link
        href="/overview"
        className="topbar-brand"
        style={{ color: "inherit", textDecoration: "none" }}
      >
        <span className="topbar-logo" aria-hidden>
          <svg width={14} height={14} viewBox="0 0 14 14" fill="none">
            <path
              d="M3.5 6.5c.7 1.6 2 2.6 3.5 2.6s2.8-1 3.5-2.6"
              stroke="currentColor"
              strokeWidth={1.6}
              strokeLinecap="round"
            />
          </svg>
        </span>
        <span>uknomi</span>
        <span className="topbar-sub">
          <span
            style={{
              color: "var(--ink-on-dark-2)",
              paddingLeft: 6,
              borderLeft: "1px solid oklch(30% 0.005 250)",
            }}
          >
            Control&nbsp;Plane
          </span>
        </span>
      </Link>
      <span className="topbar-pill" aria-label="status">
        <Dot tone="green" />
        Online
      </span>
      <nav className="topbar-nav" aria-label="Primary">
        {NAV.map((n) => {
          const active = isActive(n, pathname);
          return (
            <Link
              key={n.id}
              href={n.href}
              className={`topbar-nav-item${active ? " is-active" : ""}`}
              aria-current={active ? "page" : undefined}
            >
              {n.label}
            </Link>
          );
        })}
        <div className="topbar-user" title="Account">
          {userInitials}
        </div>
        <button
          type="button"
          className="btn ghost small"
          onClick={onSignOut}
          style={{ marginLeft: 8, color: "var(--ink-on-dark)" }}
        >
          Sign out
        </button>
      </nav>
    </header>
  );
}
