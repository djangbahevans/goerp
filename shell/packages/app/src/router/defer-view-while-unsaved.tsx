import { toast } from "@goerp/sdk/notifications";
import type { ResolvedView } from "@goerp/sdk/schema";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { UnsavedChangesContext } from "../renderers/form/unsaved-changes.js";

export const VIEW_UPDATE_DEFERRED_MESSAGE = "This screen was updated. Save or discard your changes to see the update.";

const screenOf = (view: ResolvedView) => `${view.module}:${view.viewName}:${view.recordId ?? ""}`;

// A hot reload must not swap the declaration under an in-progress edit: while the shown form has
// unsaved changes, an updated declaration for the same screen waits until the form is saved or
// discarded. Opening another screen applies at once.
export function DeferViewWhileUnsaved({
  view,
  children,
}: {
  view: ResolvedView;
  children: (view: ResolvedView) => ReactNode;
}): ReactNode {
  const [unsaved, setUnsaved] = useState(false);
  const [shown, setShown] = useState(view);
  const update = JSON.stringify(view.declaration);
  const deferred =
    unsaved && shown !== view && screenOf(shown) === screenOf(view) && JSON.stringify(shown.declaration) !== update;
  if (shown !== view && !deferred) setShown(view);

  // The router resolves a new view object on every reload, so the same update arrives repeatedly.
  const announced = useRef<string | null>(null);
  useEffect(() => {
    if (!deferred || announced.current === update) return;
    announced.current = update;
    toast.info(VIEW_UPDATE_DEFERRED_MESSAGE);
  }, [deferred, update]);

  return <UnsavedChangesContext value={setUnsaved}>{children(deferred ? shown : view)}</UnsavedChangesContext>;
}
