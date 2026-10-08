import { useBlocker } from "@tanstack/react-router";
import { useRef } from "react";
import { isEditParam } from "./use-form-mode.js";

const inEditMode = (search: unknown) => isEditParam((search as Record<string, unknown>).edit);

export interface FormLeaveGuard {
  // True while a navigation away is held for the user's decision.
  blocked: boolean;
  stay: () => void;
  leave: () => void;
  // Runs a navigation the form triggers itself without asking.
  unguarded: <T>(navigation: () => Promise<T>) => Promise<T>;
}

// view-system.md §5 "Unsaved changes": while `active`, leaving the form's path or its edit mode
// asks first and closing or reloading the tab shows the browser's own prompt.
// Other search changes (a tab) keep the path and edit mode and are never held.
export function useFormLeaveGuard(active: boolean): FormLeaveGuard {
  const activeRef = useRef(active);
  activeRef.current = active;
  const unguardedCount = useRef(0);

  const blocker = useBlocker({
    shouldBlockFn: ({ current, next }) =>
      activeRef.current &&
      unguardedCount.current === 0 &&
      (current.pathname !== next.pathname || (inEditMode(current.search) && !inEditMode(next.search))),
    enableBeforeUnload: () => activeRef.current,
    withResolver: true,
  });

  return {
    blocked: blocker.status === "blocked",
    stay: () => blocker.reset?.(),
    leave: () => blocker.proceed?.(),
    unguarded: async (navigation) => {
      unguardedCount.current += 1;
      try {
        return await navigation();
      } finally {
        unguardedCount.current -= 1;
      }
    },
  };
}
