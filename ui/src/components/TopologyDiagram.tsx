import type { TopologyView } from "../api/types";

const COL = 220;
const ROW = 64;
const BOX_W = 168;
const BOX_H = 40;
const PAD = 8;

/**
 * Columns from callers to callees: a Component sits one column right of the
 * furthest Component that calls it. A cycle can't push anything further
 * than the number of Components.
 */
export function layers(view: TopologyView): Map<string, number> {
  const level = new Map(view.components.map((c) => [c.id, 0]));
  for (let i = 0; i < view.components.length; i++) {
    let moved = false;
    for (const d of view.dependencies) {
      const from = level.get(d.from);
      const to = level.get(d.to);
      if (from === undefined || to === undefined || to > from || from + 1 >= view.components.length) continue;
      level.set(d.to, from + 1);
      moved = true;
    }
    if (!moved) break;
  }
  return level;
}

const short = (s: string) => (s.length > 20 ? `${s.slice(0, 19)}…` : s);

/**
 * TopologyDiagram draws an Application's declared calls between its
 * Components (spec 020): what's declared, not live traffic. It's a picture
 * of the table beside it, which carries the same facts for screen readers.
 */
export function TopologyDiagram({ view, label }: { view: TopologyView; label: string }) {
  const level = layers(view);
  const rows = new Map<number, number>();
  const pos = new Map<string, { x: number; y: number }>();
  for (const c of view.components) {
    const l = level.get(c.id) ?? 0;
    const r = rows.get(l) ?? 0;
    rows.set(l, r + 1);
    pos.set(c.id, { x: PAD + l * COL, y: PAD + r * ROW });
  }
  const width = PAD * 2 + (Math.max(0, ...level.values()) * COL + BOX_W);
  const height = PAD * 2 + Math.max(1, ...rows.values()) * ROW - (ROW - BOX_H);
  return (
    <div className="table-wrap">
      <svg className="topology" viewBox={`0 0 ${width} ${height}`} width={width} height={height} role="img" aria-label={label}>
        <defs>
          <marker id="topology-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
            <path d="M0 0 10 5 0 10z" className="topology-arrow" />
          </marker>
        </defs>
        {view.dependencies.map((d) => {
          const a = pos.get(d.from);
          const b = pos.get(d.to);
          if (!a || !b) return null;
          const x1 = a.x + BOX_W;
          const y1 = a.y + BOX_H / 2;
          const x2 = b.x;
          const y2 = b.y + BOX_H / 2;
          const bend = Math.max(40, Math.abs(x2 - x1) / 2);
          return <path key={`${d.from}-${d.to}-${d.port}`} className="topology-edge" d={`M${x1} ${y1} C${x1 + bend} ${y1} ${x2 - bend} ${y2} ${x2} ${y2}`} markerEnd="url(#topology-arrow)" />;
        })}
        {view.components.map((c) => {
          const p = pos.get(c.id)!;
          return (
            <g key={c.id}>
              <title>{c.name}</title>
              <rect className="topology-node" x={p.x} y={p.y} width={BOX_W} height={BOX_H} rx={4} />
              <text className="topology-label" x={p.x + 12} y={p.y + BOX_H / 2} dominantBaseline="central">
                {short(c.name)}
              </text>
            </g>
          );
        })}
      </svg>
    </div>
  );
}
