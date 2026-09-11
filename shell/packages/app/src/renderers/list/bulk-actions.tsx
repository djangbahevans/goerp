import { useOptionalPermission } from "@goerp/sdk/auth";
import { ActionButton, AlertDialog } from "@goerp/sdk/components";
import type { BulkActionContextValue } from "@goerp/sdk/react";
import { BulkActionContext, useAction, useExport } from "@goerp/sdk/react";
import { componentRegistry } from "@goerp/sdk/schema";
import { useCallback, useMemo, useState } from "react";
import type { BulkAction } from "./list-view-types.js";

// view-system.md's "Bulk actions" — only "custom"/"route"/"export" are
// rendered, matching the header_actions ListActions convention (list-actions.tsx)
// of leaving create/import/report unimplemented rather than half-building them.

// Shared visibility gate for every bulk action button: permission, the
// action's own min_selected/max_selected range, and (custom actions only)
// that the named component actually resolves — extracted after the
// permission+range pair showed up identically in all three button
// components below, keeping the middle field-specific check the only
// thing each call site supplies.
function useBulkActionGate(action: BulkAction, selectedCount: number, hasRequiredField: boolean): boolean {
  const allowed = useOptionalPermission(action.permission);
  if (!allowed || !hasRequiredField) return false;
  const min = action.min_selected ?? 1;
  if (selectedCount < min) return false;
  if (action.max_selected !== undefined && selectedCount > action.max_selected) return false;
  return true;
}

interface BulkActionButtonProps {
  action: BulkAction;
  selectedCount: number;
}

function CustomBulkActionButton({
  action,
  selectedCount,
  onActivate,
}: BulkActionButtonProps & { onActivate: () => void }) {
  const visible = useBulkActionGate(
    action,
    selectedCount,
    action.component !== undefined && componentRegistry.has(action.component),
  );
  if (!visible) return null;

  return (
    <ActionButton variant={action.style} icon={action.icon} onClick={onActivate}>
      {action.label}
    </ActionButton>
  );
}

function RouteBulkActionButton({
  action,
  selectedCount,
  selectedIds,
  onSuccess,
}: BulkActionButtonProps & { selectedIds: string[]; onSuccess: () => void }) {
  const visible = useBulkActionGate(action, selectedCount, action.route !== undefined);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const routeAction = useAction<unknown, Record<string, unknown>>(action.route ?? "", {
    onSuccess: () => {
      setConfirmOpen(false);
      onSuccess();
    },
  });
  if (!visible) return null;

  const confirm = action.confirm;
  const fire = (inputValue?: string) => {
    const variables: Record<string, unknown> = { ...action.route_params, ids: selectedIds };
    if (confirm?.input && inputValue !== undefined) {
      variables[confirm.input.field] = inputValue;
    }
    routeAction.mutate(variables);
  };

  return (
    <>
      <ActionButton
        variant={action.style}
        icon={action.icon}
        loading={routeAction.isPending}
        onClick={() => (confirm ? setConfirmOpen(true) : fire())}
      >
        {action.label}
        {routeAction.isError && <span role="alert">{routeAction.error?.message}</span>}
      </ActionButton>
      {confirm && (
        <AlertDialog
          open={confirmOpen}
          title={confirm.title}
          description={confirm.message}
          confirmLabel={confirm.confirm_label}
          cancelLabel={confirm.cancel_label}
          confirmVariant={confirm.destructive ? "danger" : "primary"}
          {...(confirm.input ? { input: confirm.input } : {})}
          onConfirm={(inputValue) => fire(inputValue)}
          onCancel={() => setConfirmOpen(false)}
        />
      )}
    </>
  );
}

function ExportBulkActionButton({
  action,
  selectedCount,
  selectedIds,
}: BulkActionButtonProps & { selectedIds: string[] }) {
  const visible = useBulkActionGate(action, selectedCount, action.route !== undefined);
  const format = action.format ?? "csv";
  const exportAction = useExport(action.route ?? "", `export.${format}`);
  if (!visible) return null;

  return (
    <ActionButton
      variant={action.style}
      icon={action.icon}
      loading={exportAction.isPending}
      onClick={() => void exportAction.trigger({ format, ids: selectedIds }).catch(() => {})}
    >
      {action.label}
      {exportAction.isError && <span role="alert">{exportAction.error?.message}</span>}
    </ActionButton>
  );
}

export interface BulkActionsProps {
  actions: BulkAction[];
  selectedIds: string[];
  clearSelection: () => void;
}

function ActiveCustomPanel({
  action,
  selectedIds,
  isLoading,
  setLoading,
  onComplete,
  onCancel,
}: {
  action: BulkAction;
  selectedIds: string[];
  isLoading: boolean;
  setLoading: (loading: boolean) => void;
  onComplete: () => void;
  onCancel: () => void;
}) {
  const contextValue = useMemo<BulkActionContextValue>(
    () => ({ selectedIds, selectedCount: selectedIds.length, onComplete, onCancel, isLoading, setLoading }),
    [selectedIds, isLoading, setLoading, onComplete, onCancel],
  );

  // CustomBulkActionButton only activates an action once componentRegistry.has()
  // already confirmed this name resolves — resolve() itself never throws on
  // that path, but a name unregistered between render and click (or a
  // registry cleared mid-session, e.g. hot reload) shouldn't crash the
  // whole list view over one stale bulk-action reference.
  if (!action.component || !componentRegistry.has(action.component)) {
    return <div role="alert">"{action.component}" isn't a registered component — this bulk action can't be shown.</div>;
  }
  const Component = componentRegistry.resolve(action.component);

  return (
    <BulkActionContext.Provider value={contextValue}>
      <Component />
    </BulkActionContext.Provider>
  );
}

export function BulkActions({ actions, selectedIds, clearSelection }: BulkActionsProps) {
  const [activeCustom, setActiveCustom] = useState<BulkAction | null>(null);
  const [isLoading, setLoading] = useState(false);

  const onComplete = useCallback(() => {
    setActiveCustom(null);
    setLoading(false);
    clearSelection();
  }, [clearSelection]);

  const onCancel = useCallback(() => {
    setActiveCustom(null);
    setLoading(false);
  }, []);

  // Checked ahead of the "nothing selected" branch below: row checkboxes
  // stay live while a custom panel is open, so deselecting every row must
  // not silently drop the panel out from under the user mid-flow — only
  // onComplete/onCancel (the panel's own lifecycle) closes it.
  if (activeCustom) {
    return (
      <ActiveCustomPanel
        action={activeCustom}
        selectedIds={selectedIds}
        isLoading={isLoading}
        setLoading={setLoading}
        onComplete={onComplete}
        onCancel={onCancel}
      />
    );
  }

  if (selectedIds.length === 0) return null;

  return (
    <div role="toolbar" aria-label="Bulk actions" className="flex items-center gap-2">
      <span aria-live="polite" className="text-sm text-text-secondary">
        {selectedIds.length} selected
      </span>
      {actions.map((action) => {
        switch (action.type) {
          case "custom":
            return (
              <CustomBulkActionButton
                key={action.label}
                action={action}
                selectedCount={selectedIds.length}
                onActivate={() => setActiveCustom(action)}
              />
            );
          case "route":
            return (
              <RouteBulkActionButton
                key={action.label}
                action={action}
                selectedCount={selectedIds.length}
                selectedIds={selectedIds}
                onSuccess={clearSelection}
              />
            );
          case "export":
            return (
              <ExportBulkActionButton
                key={action.label}
                action={action}
                selectedCount={selectedIds.length}
                selectedIds={selectedIds}
              />
            );
          default:
            return null;
        }
      })}
    </div>
  );
}
