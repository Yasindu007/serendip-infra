import { useEffect, useRef } from "react";
import type { LogLine } from "../api/types";

/**
 * LogView shows workload log lines as plain text: never HTML, never
 * interpreted (a line can hold anything the workload printed). While
 * following, it keeps the newest line in view unless the person has
 * scrolled up to read.
 */
export function LogView({ lines, label, following }: { lines: LogLine[]; label: string; following: boolean }) {
  const box = useRef<HTMLDivElement>(null);
  const pinned = useRef(true);

  useEffect(() => {
    const el = box.current;
    if (el && following && pinned.current) el.scrollTop = el.scrollHeight;
  }, [lines, following]);

  if (lines.length === 0) return <p className="state-body">No log lines in this range.</p>;
  return (
    <div
      ref={box}
      className="log"
      role="log"
      aria-label={label}
      tabIndex={0}
      translate="no"
      onScroll={(e) => {
        const el = e.currentTarget;
        pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 24;
      }}
    >
      {lines.map((l, i) => (
        <div key={i} className="log-line">
          <time dateTime={l.time} className="log-meta">
            {l.time ? l.time.slice(11, 23) : ""}
          </time>
          <span className="log-meta">{l.instance}</span>
          <span className="log-text">{l.line}</span>
        </div>
      ))}
    </div>
  );
}
