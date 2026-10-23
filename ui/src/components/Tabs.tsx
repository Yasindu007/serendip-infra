import { Link } from "../lib/router";

/**
 * Tabs are links to sections of one page, kept in the address (?tab=), so
 * a tab can be bookmarked, shared and opened in a new tab like any link.
 */
export function Tabs({ label, base, current, tabs }: { label: string; base: string; current: string; tabs: Array<{ id: string; label: string }> }) {
  return (
    <nav className="tabs" aria-label={label}>
      {tabs.map((t, i) => (
        <Link key={t.id} to={i === 0 ? base : `${base}?tab=${t.id}`} aria-current={current === t.id ? "page" : undefined}>
          {t.label}
        </Link>
      ))}
    </nav>
  );
}
