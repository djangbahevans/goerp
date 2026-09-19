import { ActionButton } from "@goerp/sdk/components";
import { moduleNameOf, useAction } from "@goerp/sdk/react";
import { modelRegistry, type WorkflowTransition } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { titleCaseWords } from "../../chrome/title-case-words.js";
import { useConditionEvaluator } from "../../conditions/use-condition-evaluator.js";
import type { Row } from "../list/list-view-types.js";
import { recordQueryKey } from "./use-form-record.js";

// One button per transition, its own useAction instance — ActionButton
// self-gates on `permission`, so no separate useOptionalPermission check
// is needed the way a plain (non-hook-owning) list item would.
//
// routeModule is resource's own owning module ("{module}.{model}", split
// via moduleNameOf), not necessarily the module that declared the form
// view rendering this button — a view can reference a resource from
// another module (manifest-spec.md's "{module}.{view_name}" cross-module
// view reference), and a transition's route is always registered under
// the resource's own module (RegisterModelWorkflowActions), not the
// view's. Using the wrong one would resolve against a nonexistent route.
function TransitionButton({
  routeModule,
  resource,
  recordId,
  transition,
}: {
  routeModule: string;
  resource: string;
  recordId: string;
  transition: WorkflowTransition;
}) {
  const transitionAction = useAction<unknown, string>(`${routeModule}.${transition.action_name}`, {
    invalidates: [recordQueryKey(resource, recordId)],
  });

  return (
    <ActionButton
      permission={transition.permission}
      loading={transitionAction.isPending}
      onClick={() => transitionAction.mutate(recordId)}
    >
      {titleCaseWords(transition.action_name, /[-_]/)}
      {transitionAction.isError && <span role="alert">{transitionAction.error?.message}</span>}
    </ActionButton>
  );
}

export interface WorkflowActionsProps {
  resource: string;
  recordId: string | undefined;
  record: Row;
}

// view-system.md's "Auto-rendered transition buttons": one button per
// transition legal from the record's current state, off the resource's
// own .Workflow()-declared Selection field (goerp#864) — additive
// alongside header_actions, not a replacement for it. A transition's own
// `condition` further narrows the legal set against the live record.
export function WorkflowActions({ resource, recordId, record }: WorkflowActionsProps) {
  const conditions = useConditionEvaluator(`${resource} form`);
  const { data: model } = useQuery({
    queryKey: ["form-model-workflow", resource],
    queryFn: () => modelRegistry.resolve(resource),
    enabled: recordId !== undefined,
  });

  if (recordId === undefined) return null;

  const field = model?.fields.find((f) => f.workflow);
  const routeModule = moduleNameOf(resource);
  if (!field?.workflow || routeModule === undefined) return null;

  const currentState = record[field.name];
  const legalTransitions = field.workflow.transitions.filter(
    (t) =>
      t.from === currentState &&
      conditions.isVisible(t.condition, `workflow transition "${t.action_name}" condition`, record),
  );
  if (legalTransitions.length === 0) return null;

  return (
    <>
      {legalTransitions.map((transition) => (
        <TransitionButton
          key={transition.action_name}
          routeModule={routeModule}
          resource={resource}
          recordId={recordId}
          transition={transition}
        />
      ))}
    </>
  );
}
