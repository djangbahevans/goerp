import { createContext, type ReactNode, useContext } from "react";

export interface CurrentView {
  viewName: string;
  moduleName: string;
  resource: string | null;
  recordId: string | null;
  embedded: boolean;
  params: URLSearchParams;
}

const CurrentViewContext = createContext<CurrentView | null>(null);

export function CurrentViewProvider({ value, children }: { value: CurrentView; children: ReactNode }): ReactNode {
  return <CurrentViewContext value={value}>{children}</CurrentViewContext>;
}

export function useCurrentView(): CurrentView | null {
  return useContext(CurrentViewContext);
}
