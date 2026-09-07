import type { ReactNode } from "react";

export interface PageLayoutProps {
  children: ReactNode;
}

export function PageLayout({ children }: PageLayoutProps): ReactNode {
  return <div className="space-y-6 bg-bg p-6 text-text">{children}</div>;
}
