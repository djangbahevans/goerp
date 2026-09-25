import { callAction } from "@goerp/sdk";
import type { ActionMenuItem } from "@goerp/sdk/components";
import { moduleLink } from "@goerp/sdk/nav";
import { toast } from "@goerp/sdk/notifications";
import { viewPathRegistry } from "@goerp/sdk/schema";
import { useMutation } from "@tanstack/react-query";
import type { ListAction } from "../list/list-view-types.js";
import { mapActionConfirm } from "../shared/map-action-confirm.js";

// Only "create"/"route"/"url" — the same depth list-actions.tsx renders.

export type KanbanNavigate = (opts: { to: string }) => unknown;

interface DynamicRouteCall {
  route: string;
  variables: unknown;
}

// One shared mutation for every route action, parameterized by route name
// and variables at call time — useAction()'s one-hook-per-static-route
// shape doesn't fit a manifest-declared, per-record set of route actions.
export function useKanbanRouteAction() {
  return useMutation({
    mutationFn: ({ route, variables }: DynamicRouteCall) => callAction(route, variables),
    onError: (err: unknown) => toast.error(err instanceof Error ? err.message : String(err)),
  });
}

export type KanbanRouteAction = ReturnType<typeof useKanbanRouteAction>;

// "create" has no id to target — navigates to the declared view only.
function resolveCreateAction(action: ListAction, module: string, navigate: KanbanNavigate): ActionMenuItem | undefined {
  if (!action.view) return undefined;
  const view = action.view;
  return {
    label: action.label,
    ...(action.icon ? { icon: action.icon } : {}),
    ...(action.permission ? { permission: action.permission } : {}),
    ...(action.confirm ? { confirm: mapActionConfirm(action.confirm) } : {}),
    onClick: () => {
      viewPathRegistry
        .resolve(view, module)
        .then((path) => {
          if (path) navigate({ to: moduleLink(path) });
          else toast.error(`"${view}" doesn't resolve to a route yet.`);
        })
        .catch((err: unknown) => toast.error(err instanceof Error ? err.message : String(err)));
    },
  };
}

// The bare record id fills the route's own `{id}` placeholder — unless a
// confirm dialog also collected an input value, in which case both need
// to reach the request: `{id, [confirm.input.field]: inputValue}`, the
// object call shape splitPathAndBody documents for a path with a
// placeholder plus body fields alongside it.
function resolveRouteAction(
  action: ListAction,
  routeAction: KanbanRouteAction,
  recordId: string | undefined,
): ActionMenuItem | undefined {
  if (!action.route) return undefined;
  const route = action.route;
  const confirm = action.confirm;
  return {
    label: action.label,
    ...(action.icon ? { icon: action.icon } : {}),
    ...(action.permission ? { permission: action.permission } : {}),
    ...(confirm ? { confirm: mapActionConfirm(confirm) } : {}),
    onClick: (inputValue?: string) => {
      // `id` last, not first — a manifest confirm.input.field literally
      // named "id" must not silently overwrite the real record id.
      const variables =
        confirm?.input && inputValue !== undefined ? { [confirm.input.field]: inputValue, id: recordId } : recordId;
      routeAction.mutate({ route, variables });
    },
  };
}

function resolveUrlAction(action: ListAction): ActionMenuItem | undefined {
  if (!action.url) return undefined;
  const url = action.url;
  return {
    label: action.label,
    ...(action.icon ? { icon: action.icon } : {}),
    ...(action.permission ? { permission: action.permission } : {}),
    ...(action.confirm ? { confirm: mapActionConfirm(action.confirm) } : {}),
    onClick: () => {
      window.open(url, "_blank", "noopener,noreferrer");
    },
  };
}

// Resolves card_actions/column_actions into plain ActionMenuItem[] data.
// recordId is the card's id for card_actions, undefined for column_actions.
export function resolveKanbanActionItems(
  actions: ListAction[],
  module: string,
  navigate: KanbanNavigate,
  routeAction: KanbanRouteAction,
  recordId?: string,
): ActionMenuItem[] {
  const items: ActionMenuItem[] = [];
  for (const action of actions) {
    const item =
      action.type === "create"
        ? resolveCreateAction(action, module, navigate)
        : action.type === "route"
          ? resolveRouteAction(action, routeAction, recordId)
          : action.type === "url"
            ? resolveUrlAction(action)
            : undefined;
    if (item) items.push(item);
  }
  return items;
}
