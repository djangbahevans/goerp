import type { ReactNode } from "react";

export interface PageLayoutProps {
  children: ReactNode;
}

export function PageLayout({ children }: PageLayoutProps): ReactNode {
  return <div className="bg-bg p-6 text-fg">{children}</div>;
}
