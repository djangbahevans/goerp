import { useOptionalPermission } from "@goerp/sdk/auth";
import { ActionButton, AlertDialog } from "@goerp/sdk/components";
import type { BulkActionContextValue } from "@goerp/sdk/react";
import { BulkActionContext, useAction, useExport } from "@goerp/sdk/react";
import { componentRegistry } from "@goerp/sdk/schema";
import { useState } from "react";
import type { BulkAction } from "./list-view-types.js";

// view-system.md's "Bulk actions" — only "custom"/"route"/"export" are
// rendered, matching the header_actions ListActions convention (list-actions.tsx)
// of leaving create/import/report unimplemented rather than half-building them.

function withinSelectedRange(action: BulkAction, count: number): boolean {
  const min = action.min_selected ?? 1;
  if (count < min) return false;
  if (action.max_selected !== undefined && count > action.max_selected) return false;
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
  const allowed = useOptionalPermission(action.permission);
  if (!allowed || !action.component || !withinSelectedRange(action, selectedCount)) return null;

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
  const allowed = useOptionalPermission(action.permission);
  const [confirmOpen, setConfirmOpen] = useState(false);
  const routeAction = useAction<unknown, Record<string, unknown>>(action.route ?? "", {
    onSuccess: () => {
      setConfirmOpen(false);
      onSuccess();
    },
  });
  if (!allowed || !action.route || !withinSelectedRange(action, selectedCount)) return null;

  const confirm = action.confirm;
  const fire = (inputValue?: string) => {
    const variables: Record<string, unknown> = { ids: selectedIds };
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
          {...(confirm.input ? { input: { ...confirm.input } } : {})}
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
  const allowed = useOptionalPermission(action.permission);
  const format = action.format ?? "csv";
  const exportAction = useExport(action.route ?? "", `export.${format}`);
  if (!allowed || !action.route || !withinSelectedRange(action, selectedCount)) return null;

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

export function BulkActions({ actions, selectedIds, clearSelection }: BulkActionsProps) {
  const [activeCustom, setActiveCustom] = useState<BulkAction | null>(null);
  const [isLoading, setLoading] = useState(false);

  if (selectedIds.length === 0) return null;

  if (activeCustom) {
    // Guarded by the same `!action.component` check CustomBulkActionButton
    // uses to hide the button in the first place — activeCustom can only
    // be set to an action that already passed it.
    const Component = componentRegistry.resolve(activeCustom.component as string);
    const contextValue: BulkActionContextValue = {
      selectedIds,
      selectedCount: selectedIds.length,
      onComplete: () => {
        setActiveCustom(null);
        setLoading(false);
        clearSelection();
      },
      onCancel: () => {
        setActiveCustom(null);
        setLoading(false);
      },
      isLoading,
      setLoading,
    };
    return (
      <BulkActionContext.Provider value={contextValue}>
        <Component />
      </BulkActionContext.Provider>
    );
  }

  return (
    <div role="toolbar" aria-label="Bulk actions">
      <span>{selectedIds.length} selected</span>
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
