// Derives a Component's reconciliation path from its real Conditions and its
// Application's (core/reconcile/application.go). Nothing is inferred beyond
// what a Condition says; a stage with no Condition behind it yet is
// "pending", never "done".
import type { Application, Component, Condition } from "../api/types";
import type { Stage, StageState } from "../components/ReconciliationPath";
import { conditionKind, findCondition } from "../components/Status";

function stateOf(c: Condition | undefined): StageState {
  switch (conditionKind(c)) {
    case "ok":
      return "done";
    case "progress":
      return "active";
    case "error":
      return "failed";
    default:
      return c ? "failed" : "pending";
  }
}

export function exposedPorts(c: Component) {
  return (c.spec.ports ?? []).filter((p) => p.expose);
}

export function componentStages(component: Component, app: Application | undefined): Stage[] {
  const stages: Stage[] = [{ label: "Saved", state: "done", detail: `Version ${component.metadata.resourceVersion}` }];

  const appReady = findCondition(app?.status.conditions, "Ready");
  stages.push({
    label: "Application ready",
    state: app ? stateOf(appReady) : "failed",
    detail: app ? (appReady?.reason ?? "Not reconciled yet") : "Owning application not found",
  });

  const ready = findCondition(component.status.conditions, "Ready");
  const deployed = stateOf(ready);
  stages.push({
    label: "Deployed",
    // While the application isn't ready, the component's own state can only
    // be "waiting"; show that rather than a stale failure.
    state: stages[1]!.state === "done" ? deployed : deployed === "done" ? "done" : "pending",
    detail: ready?.reason ?? "Not reconciled yet",
  });

  if (exposedPorts(component).length > 0) {
    const reachable = findCondition(component.status.conditions, "Reachable");
    const serving = component.status.endpoints?.length ?? 0;
    stages.push({
      label: "Reachable",
      state: stateOf(reachable),
      detail: reachable?.status === "True" ? `${serving} endpoint${serving === 1 ? "" : "s"} serving` : (reachable?.reason ?? "Not checked yet"),
    });
  }
  return stages;
}
