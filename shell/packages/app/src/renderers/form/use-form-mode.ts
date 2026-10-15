import { useNavigate, useSearch } from "@tanstack/react-router";
import { useCallback, useState } from "react";

export type FormMode = "display" | "edit";

export interface FormModeHandle {
  mode: FormMode;
  // Switches an existing record to edit mode, as a history entry so Back returns to display mode.
  edit: () => void;
  // Returns to display mode, replacing the edit entry so Back does not return to it.
  leaveEdit: () => Promise<void>;
}

export const isEditParam = (value: unknown) => value !== undefined && value !== false && value !== "false";

function withEdit(prev: Record<string, unknown>): Record<string, unknown> {
  return { ...prev, edit: true };
}

function withoutEdit(prev: Record<string, unknown>): Record<string, unknown> {
  const { edit: _edit, ...rest } = prev;
  return rest;
}

// An existing record is in display mode unless `edit` is set; new and autosave forms are always in
// edit mode, read-only ones in display mode. A `local` form (in a modal) keeps `edit` in state.
export function useFormMode({
  isNew,
  autosave,
  readonly,
  local = false,
}: {
  isNew: boolean;
  autosave: boolean;
  readonly: boolean;
  local?: boolean;
}): FormModeHandle {
  // No route-level search validator types this; cast once, as use-list-state.ts does.
  const navigate = useNavigate() as unknown as (opts: {
    search: (prev: Record<string, unknown>) => Record<string, unknown>;
    replace?: boolean;
  }) => Promise<void>;
  const search = useSearch({ strict: false }) as Record<string, unknown>;

  const [localEdit, setLocalEdit] = useState(false);

  const edit = useCallback(() => {
    if (local) setLocalEdit(true);
    else void navigate({ search: (prev) => withEdit(prev) });
  }, [local, navigate]);
  const leaveEdit = useCallback(async () => {
    if (local) setLocalEdit(false);
    else await navigate({ search: (prev) => withoutEdit(prev), replace: true });
  }, [local, navigate]);

  if (readonly) return { mode: "display", edit, leaveEdit };
  if (isNew || autosave) return { mode: "edit", edit, leaveEdit };
  return { mode: (local ? localEdit : isEditParam(search.edit)) ? "edit" : "display", edit, leaveEdit };
}
