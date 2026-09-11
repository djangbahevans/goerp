import { useOptionalPermission } from "@goerp/sdk/auth";
import { ActionButton } from "@goerp/sdk/components";
import { moduleLink } from "@goerp/sdk/nav";
import { useAction } from "@goerp/sdk/react";
import { viewPathRegistry } from "@goerp/sdk/schema";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import type { ListAction } from "./list-view-types.js";

// Only "create"/"route"/"url" — the depth goerp#575's own scope covers.
// export/import/report/custom action types aren't rendered.

function CreateActionButton({ action, module }: { action: ListAction; module: string }) {
  // Called unconditionally, ahead of the `view` check below, so a
  // malformed action (permission set, `view` missing) still hits the same
  // fail-fast PermissionProvider requirement as a well-formed one — same
  // contract UrlActionButton enforces.
  const allowed = useOptionalPermission(action.permission);
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);
  const view = action.view;
  if (!allowed || !view) return null;

  return (
    // No `permission` prop here — `allowed` above already gates rendering
    // entirely, so ActionButton's own internal check would just repeat it.
    <ActionButton
      variant={action.style}
      icon={action.icon}
      onClick={() => {
        setError(null);
        viewPathRegistry
          .resolve(view, module)
          .then((path) => {
            if (path) void navigate({ to: moduleLink(path) });
            else setError(`"${view}" doesn't resolve to a route yet.`);
          })
          .catch((err: unknown) => setError(err instanceof Error ? err.message : String(err)));
      }}
    >
      {action.label}
      {error && <span role="alert">{error}</span>}
    </ActionButton>
  );
}

function RouteActionButton({ action }: { action: ListAction }) {
  const allowed = useOptionalPermission(action.permission);
  const routeAction = useAction(action.route ?? "");
  if (!allowed || !action.route) return null;

  return (
    // No `permission` prop here — `allowed` above already gates rendering
    // entirely, so ActionButton's own internal check would just repeat it.
    <ActionButton
      variant={action.style}
      icon={action.icon}
      loading={routeAction.isPending}
      onClick={() => routeAction.mutate(undefined)}
    >
      {action.label}
      {routeAction.isError && <span role="alert">{routeAction.error?.message}</span>}
    </ActionButton>
  );
}

function UrlActionButton({ action }: { action: ListAction }) {
  const allowed = useOptionalPermission(action.permission);
  if (!allowed || !action.url) return null;

  return (
    <a href={action.url} target="_blank" rel="noreferrer">
      {action.label}
    </a>
  );
}

export interface ListActionsProps {
  actions: ListAction[];
  module: string;
}

export function ListActions({ actions, module }: ListActionsProps) {
  return (
    <div className="flex items-center gap-2">
      {actions.map((action) => {
        switch (action.type) {
          case "create":
            return <CreateActionButton key={action.label} action={action} module={module} />;
          case "route":
            return <RouteActionButton key={action.label} action={action} />;
          case "url":
            return <UrlActionButton key={action.label} action={action} />;
          default:
            return null;
        }
      })}
    </div>
  );
}
