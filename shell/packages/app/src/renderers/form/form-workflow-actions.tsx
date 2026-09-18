import { ActionButton } from "@goerp/sdk/components";
import { useAction } from "@goerp/sdk/react";
import { modelRegistry, type WorkflowTransition } from "@goerp/sdk/schema";
import { useQuery } from "@tanstack/react-query";
import { titleCaseWords } from "../../chrome/title-case-words.js";
import type { Row } from "../list/list-view-types.js";
import { recordQueryKey } from "./use-form-record.js";

// One button per transition, its own useAction instance — ActionButton
// self-gates on `permission`, so no separate useOptionalPermission check
// is needed the way a plain (non-hook-owning) list item would.
function TransitionButton({
  module,
  resource,
  recordId,
  transition,
}: {
  module: string;
  resource: string;
  recordId: string;
  transition: WorkflowTransition;
}) {
  const transitionAction = useAction<unknown, string>(`${module}.${transition.action_name}`, {
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
  module: string;
  recordId: string | undefined;
  record: Row;
}

// view-system.md's "Auto-rendered transition buttons": one button per
// transition legal from the record's current state, off the resource's
// own .Workflow()-declared Selection field (goerp#864) — additive
// alongside header_actions, not a replacement for it. A transition's own
// `condition` is carried through on WorkflowTransition but not evaluated
// here — the shell's domain-expression interpreter (goerp#829) doesn't
// exist yet, the same "typed but unevaluated" posture header_actions'
// own `condition` field already has.
export function WorkflowActions({ resource, module, recordId, record }: WorkflowActionsProps) {
  const { data: model } = useQuery({
    queryKey: ["form-model-workflow", resource],
    queryFn: () => modelRegistry.resolve(resource),
  });

  if (recordId === undefined) return null;

  const field = model?.fields.find((f) => f.workflow);
  if (!field?.workflow) return null;

  const currentState = record[field.name];
  const legalTransitions = field.workflow.transitions.filter((t) => t.from === currentState);
  if (legalTransitions.length === 0) return null;

  return (
    <>
      {legalTransitions.map((transition) => (
        <TransitionButton
          key={transition.action_name}
          module={module}
          resource={resource}
          recordId={recordId}
          transition={transition}
        />
      ))}
    </>
  );
}
