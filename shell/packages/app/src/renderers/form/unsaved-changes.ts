import { createContext, useContext, useEffect } from "react";

export const UnsavedChangesContext = createContext<(unsaved: boolean) => void>(() => {});

// Tells the page hosting the form whether its unsaved-changes guard is active.
export function useReportUnsavedChanges(unsaved: boolean): void {
  const report = useContext(UnsavedChangesContext);
  useEffect(() => {
    report(unsaved);
    return () => report(false);
  }, [report, unsaved]);
}
