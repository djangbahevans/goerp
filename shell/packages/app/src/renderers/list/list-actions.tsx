import { apiClient, downloadBlob } from "@goerp/sdk";
import { useOptionalPermission } from "@goerp/sdk/auth";
import {
  ActionButton,
  ActionMenu,
  type ActionMenuItem,
  AlertDialog,
  actionButtonClassName,
  Icon,
} from "@goerp/sdk/components";
import { moduleLink } from "@goerp/sdk/nav";
import { toast } from "@goerp/sdk/notifications";
import { actionRegistry, dispatch, splitPathAndBody, useAction, useExport } from "@goerp/sdk/react";
import { viewPathRegistry } from "@goerp/sdk/schema";
import { useNavigate } from "@tanstack/react-router";
import { useState } from "react";
import { mapActionConfirm } from "../shared/map-action-confirm.js";
import type { ListAction } from "./list-view-types.js";

// export/import/custom action types still aren't rendered.

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

// Shared by useConfirmGate and MenuActionButton, so both wire `input` the same way.
function ConfirmAlertDialog({
  confirm,
  open,
  onConfirm,
  onCancel,
}: {
  confirm: NonNullable<ListAction["confirm"]>;
  open: boolean;
  onConfirm: (inputValue?: string) => void;
  onCancel: () => void;
}) {
  return (
    <AlertDialog
      open={open}
      title={confirm.title}
      description={confirm.message}
      confirmLabel={confirm.confirm_label}
      cancelLabel={confirm.cancel_label}
      confirmVariant={confirm.destructive ? "danger" : "primary"}
      {...(confirm.input ? { input: confirm.input } : {})}
      onConfirm={onConfirm}
      onCancel={onCancel}
    />
  );
}

// `confirm` gates the same way RouteBulkActionButton's does (bulk-actions.tsx).
function useConfirmGate(confirm: ListAction["confirm"], fire: (inputValue?: string) => void) {
  const [confirmOpen, setConfirmOpen] = useState(false);
  return {
    onActivate: () => (confirm ? setConfirmOpen(true) : fire()),
    dialog: confirm ? (
      <ConfirmAlertDialog
        confirm={confirm}
        open={confirmOpen}
        onConfirm={(inputValue) => {
          fire(inputValue);
          setConfirmOpen(false);
        }}
        onCancel={() => setConfirmOpen(false)}
      />
    ) : null,
  };
}

// route_params plus the dialog's collected input, keyed by confirm.input.field.
// undefined (not {}) when neither contributes anything, so a route with no
// {param} placeholder sends no body at all.
function routeVariables(
  routeParams: ListAction["route_params"],
  confirm: ListAction["confirm"],
  inputValue: string | undefined,
): Record<string, unknown> | undefined {
  const variables: Record<string, unknown> = { ...routeParams };
  if (confirm?.input && inputValue !== undefined) {
    variables[confirm.input.field] = inputValue;
  }
  return Object.keys(variables).length > 0 ? variables : undefined;
}

function RouteActionButton({ action }: { action: ListAction }) {
  const allowed = useOptionalPermission(action.permission);
  const routeAction = useAction<unknown, Record<string, unknown> | undefined>(action.route ?? "");
  const { onActivate, dialog } = useConfirmGate(action.confirm, (inputValue) =>
    routeAction.mutate(routeVariables(action.route_params, action.confirm, inputValue)),
  );
  if (!allowed || !action.route) return null;

  return (
    <>
      {/* No `permission` prop here — `allowed` above already gates rendering
      entirely, so ActionButton's own internal check would just repeat it. */}
      <ActionButton variant={action.style} icon={action.icon} loading={routeAction.isPending} onClick={onActivate}>
        {action.label}
        {routeAction.isError && <span role="alert">{routeAction.error?.message}</span>}
      </ActionButton>
      {dialog}
    </>
  );
}

function ReportActionButton({ action }: { action: ListAction }) {
  const allowed = useOptionalPermission(action.permission);
  const exportAction = useExport(action.report ?? "", `${action.label ?? "report"}.${action.format ?? "pdf"}`);
  const { onActivate, dialog } = useConfirmGate(action.confirm, (inputValue) => {
    void exportAction.trigger(routeVariables(action.route_params, action.confirm, inputValue) ?? {}).catch(() => {});
  });
  if (!allowed || !action.report) return null;

  return (
    <>
      <ActionButton variant={action.style} icon={action.icon} loading={exportAction.isPending} onClick={onActivate}>
        {action.label}
        {exportAction.isError && <span role="alert">{exportAction.error?.message}</span>}
      </ActionButton>
      {dialog}
    </>
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

// Matches ListActions' own top-level switch — unsupported types are
// filtered out of menuItems below rather than left as a dead click.
const MENU_ITEM_TYPES = new Set(["route", "report", "url", "create", "separator"]);

// Fires one menu item imperatively rather than via useAction/useExport — a
// menu's items are a runtime-sized array, so a fixed hook-per-item set
// isn't possible the way it is for the single static action each
// top-level *ActionButton above owns.
async function fireHeaderMenuItem(
  item: ListAction,
  module: string,
  navigate: ReturnType<typeof useNavigate>,
  inputValue?: string,
): Promise<void> {
  const variables = routeVariables(item.route_params, item.confirm, inputValue);
  switch (item.type) {
    case "route": {
      if (!item.route) return;
      const route = await actionRegistry.resolve(item.route);
      const { path, body } = splitPathAndBody(route.path, variables);
      await dispatch(apiClient, route.method, path, body);
      return;
    }
    case "report": {
      if (!item.report) return;
      const route = await actionRegistry.resolve(item.report);
      const blob = await apiClient.getBlob(route.path, variables ? { params: variables } : undefined);
      downloadBlob(blob, `${item.label ?? "report"}.${item.format ?? "pdf"}`);
      return;
    }
    case "url":
      if (item.url) window.open(item.url, "_blank", "noreferrer");
      return;
    case "create": {
      if (!item.view) return;
      const path = await viewPathRegistry.resolve(item.view, module);
      if (path) void navigate({ to: moduleLink(path) });
      else throw new Error(`"${item.view}" doesn't resolve to a route yet.`);
      return;
    }
    default:
      return;
  }
}

function MenuActionButton({ action, module }: { action: ListAction; module: string }) {
  const allowed = useOptionalPermission(action.permission);
  const navigate = useNavigate();
  if (!allowed) return null;

  // ActionMenu owns the confirm-gate itself (per-item `confirm`) — this
  // just maps ListAction's snake_case wire shape into it and fires for
  // real once ActionMenu calls back with (or without) a collected input.
  const menuItems: ActionMenuItem[] = (action.items ?? [])
    .filter((item) => MENU_ITEM_TYPES.has(item.type))
    .map((item) =>
      item.type === "separator"
        ? { type: "separator" }
        : {
            type: "item",
            label: item.label,
            icon: item.icon,
            permission: item.permission,
            variant: item.style === "danger" ? "danger" : "default",
            confirm: mapActionConfirm(item.confirm),
            onClick: (inputValue?: string) => {
              fireHeaderMenuItem(item, module, navigate, inputValue).catch((err: unknown) => {
                toast.error(err instanceof Error ? err.message : String(err));
              });
            },
          },
    );

  return (
    <ActionMenu
      label={action.label ?? ""}
      items={menuItems}
      trigger={({ ref, open, disabled, onClick, onKeyDown }) => (
        <button
          ref={ref}
          type="button"
          aria-haspopup="menu"
          aria-expanded={open}
          disabled={disabled}
          onClick={onClick}
          onKeyDown={onKeyDown}
          className={actionButtonClassName(action.style === "danger" ? "danger" : (action.style ?? "secondary"), "md")}
        >
          {action.icon && <Icon name={action.icon} size={16} aria-hidden="true" />}
          {action.label}
        </button>
      )}
    />
  );
}

export interface ListActionsProps {
  actions: ListAction[];
  module: string;
  // view-system.md's suppressed-actions contract: hides "create" when embedded, unless show_create_action.
  embedded?: boolean;
  showCreateAction?: boolean;
}

export function ListActions({ actions, module, embedded, showCreateAction }: ListActionsProps) {
  const suppressCreate = embedded && !showCreateAction;
  const visibleActions = suppressCreate ? actions.filter((action) => action.type !== "create") : actions;

  return (
    <div className="flex items-center gap-2">
      {visibleActions.map((action, index) => {
        switch (action.type) {
          case "create":
            return <CreateActionButton key={action.label ?? index} action={action} module={module} />;
          case "route":
            return <RouteActionButton key={action.label ?? index} action={action} />;
          case "report":
            return <ReportActionButton key={action.label ?? index} action={action} />;
          case "url":
            return <UrlActionButton key={action.label ?? index} action={action} />;
          case "menu":
            return <MenuActionButton key={action.label ?? index} action={action} module={module} />;
          default:
            return null;
        }
      })}
    </div>
  );
}
