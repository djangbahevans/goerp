import type { ReactNode } from "react";

export interface TwoColumnLayoutProps {
  children: ReactNode;
  sidebar: ReactNode;
}

export function TwoColumnLayout({ children, sidebar }: TwoColumnLayoutProps): ReactNode {
  return (
    <div className="flex gap-8">
      <div className="w-2/3">{children}</div>
      <div className="w-1/3">{sidebar}</div>
    </div>
  );
}
