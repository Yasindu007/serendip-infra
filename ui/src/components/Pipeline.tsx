import type { ReactNode } from "react";

export interface PipelineStage {
  id: string;
  name: string;
  status: ReactNode;
  /** The names of the Environments this one promotes to. */
  promotesTo: string[];
  body: ReactNode;
}

/**
 * EnvironmentPipeline draws a Project's Environments in the order releases
 * move through them (spec 020), each with what runs there and its actions.
 * The order is spoken too, so it isn't carried by layout alone.
 */
export function EnvironmentPipeline({ label, stages }: { label: string; stages: PipelineStage[] }) {
  return (
    <ol className="pipeline" aria-label={label}>
      {stages.map((s) => (
        <li key={s.id} className="pipeline-stage" aria-labelledby={`stage-${s.id}`}>
          <div className="pipeline-head">
            <h3 id={`stage-${s.id}`} className="pipeline-name">
              {s.name}
            </h3>
            {s.status}
          </div>
          {s.body}
          <p className="field-hint">{s.promotesTo.length > 0 ? `Promotes to ${s.promotesTo.join(", ")}.` : "Last in the pipeline."}</p>
        </li>
      ))}
    </ol>
  );
}
