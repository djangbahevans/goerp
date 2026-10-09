import type { ReactNode } from "react";

export interface PageHeaderProps {
  title: string;
  subtitle?: string | undefined;
  actions?: ReactNode | undefined;
}

export function PageHeader({ title, subtitle, actions }: PageHeaderProps): ReactNode {
  return (
    <header className="mb-6 flex flex-wrap items-start justify-between gap-4">
      <div className="flex flex-col gap-1">
        <h1 className="font-semibold text-text text-xl">{title}</h1>
        {subtitle !== undefined && <p className="text-sm text-text-secondary">{subtitle}</p>}
      </div>
      {actions !== undefined && <div className="flex max-w-full flex-wrap items-center gap-2">{actions}</div>}
    </header>
  );
}
