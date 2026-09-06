import { PermissionContext } from "@goerp/sdk/auth";
import { moduleLink } from "@goerp/sdk/nav";
import { useAction } from "@goerp/sdk/react";
import { viewPathRegistry } from "@goerp/sdk/schema";
import { useNavigate } from "@tanstack/react-router";
import { useContext } from "react";
import type { ListAction } from "./list-view-types.js";

// Only "create"/"route"/"url" — the depth goerp#575's own scope covers.
// export/import/report/custom action types aren't rendered.
function usePermitted(permission: string | undefined): boolean {
  const permissions = useContext(PermissionContext);
  if (!permissions) {
    throw new Error("ListActions must be used within a PermissionProvider");
  }
  return !permission || permissions.check(permission);
}

function CreateActionButton({ action, module }: { action: ListAction; module: string }) {
  const permitted = usePermitted(action.permission);
  const navigate = useNavigate();
  const view = action.view;
  if (!permitted || !view) return null;

  return (
    <button
      type="button"
      onClick={() => {
        void viewPathRegistry.resolve(view, module).then((path) => {
          if (path) void navigate({ to: moduleLink(path) });
        });
      }}
    >
      {action.label}
    </button>
  );
}

function RouteActionButton({ action }: { action: ListAction }) {
  const permitted = usePermitted(action.permission);
  const routeAction = useAction(action.route ?? "");
  if (!permitted || !action.route) return null;

  return (
    <button type="button" onClick={() => routeAction.mutate(undefined)} disabled={routeAction.isPending}>
      {action.label}
      {routeAction.isError && <span role="alert">{routeAction.error?.message}</span>}
    </button>
  );
}

function UrlActionButton({ action }: { action: ListAction }) {
  const permitted = usePermitted(action.permission);
  if (!permitted || !action.url) return null;

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
    <div>
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
