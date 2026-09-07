import type { ReactNode } from "react";

export interface TwoColumnLayoutProps {
  children: ReactNode;
  sidebar?: ReactNode | undefined;
}

export function TwoColumnLayout({ children, sidebar }: TwoColumnLayoutProps): ReactNode {
  if (!sidebar) {
    return <div>{children}</div>;
  }

  return (
    <div className="flex flex-col gap-6 md:flex-row">
      <div className="md:w-2/3">{children}</div>
      <div className="border-border border-t md:w-1/3 md:border-t-0 md:border-l">{sidebar}</div>
    </div>
  );
}
