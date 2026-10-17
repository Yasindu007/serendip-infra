import type { ReactNode } from "react";
import { Link } from "../lib/router";
import { Icon } from "./Icon";

const nav: Array<{ label: string; to?: string }> = [
  { label: "Overview", to: "/" },
  { label: "Applications", to: "/applications" },
  // Shown so the shape of the product is honest, never as working links
  // (constitution 6, spec 016 "labelled, not mocked").
  { label: "Secrets" },
  { label: "Volumes" },
];

export function Shell({ path, actor, onSignOut, banner, children }: { path: string; actor?: string; onSignOut?: () => void; banner?: ReactNode; children: ReactNode }) {
  const section = path === "/" ? "/" : "/" + (path.split("/")[1] ?? "");
  return (
    <div className="shell">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <nav className="rail" aria-label="Primary">
        <div className="rail-brand">
          <Icon name="layers" size={18} />
          <span>Serendip</span>
        </div>
        {nav.map((item) =>
          item.to ? (
            <Link key={item.label} to={item.to} aria-current={section === item.to ? "page" : undefined}>
              {item.label}
            </Link>
          ) : (
            <span key={item.label} className="rail-item rail-item--unavailable">
              {item.label} (not available yet)
            </span>
          ),
        )}
        {actor && (
          <div className="rail-foot">
            <span>Signed in as {actor.replace(/^person:/, "")}</span>
            {onSignOut && (
              <button type="button" onClick={onSignOut}>
                Sign out
              </button>
            )}
          </div>
        )}
      </nav>
      <div className="main">
        {banner}
        <main id="main" className="content" tabIndex={-1}>
          {children}
        </main>
      </div>
    </div>
  );
}

export function PageHeader({ crumbs, title, status, actions, meta }: { crumbs?: Array<{ label: string; to: string }>; title: string; status?: ReactNode; actions?: ReactNode; meta?: ReactNode }) {
  return (
    <header className="page-header">
      {crumbs && crumbs.length > 0 && (
        <nav className="breadcrumb" aria-label="Breadcrumb">
          {crumbs.map((c, i) => (
            <span key={c.to}>
              {i > 0 && " / "}
              <Link to={c.to}>{c.label}</Link>
            </span>
          ))}
        </nav>
      )}
      <div className="page-title-row">
        <h1 className="page-title">{title}</h1>
        {status}
        {actions && <div className="page-actions">{actions}</div>}
      </div>
      {meta && <p className="page-meta">{meta}</p>}
    </header>
  );
}
