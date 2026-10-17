// The console's signature element (plan.md decision 11): where a resource
// is on its way from "saved" to "serving", drawn as a line of stages. Every
// stage comes from a real Condition; a stage with nothing behind it is not
// drawn.

export type StageState = "done" | "active" | "failed" | "pending";

export interface Stage {
  label: string;
  state: StageState;
  detail?: string;
}

const stateWords: Record<StageState, string> = {
  done: "done",
  active: "in progress",
  failed: "failed",
  pending: "not reached",
};

export function ReconciliationPath({ stages, label }: { stages: Stage[]; label: string }) {
  return (
    <ol className="path" aria-label={label}>
      {stages.map((s, i) => (
        <li key={s.label} className={`path-stage path-stage--${s.state}`}>
          <div className="path-line" aria-hidden="true">
            <span className="path-dot" />
            {i < stages.length - 1 && <span className="path-track" />}
          </div>
          <span className="path-label">
            {s.label}
            <span className="visually-hidden">: {stateWords[s.state]}</span>
          </span>
          {s.detail && <span className="path-detail">{s.detail}</span>}
        </li>
      ))}
    </ol>
  );
}
