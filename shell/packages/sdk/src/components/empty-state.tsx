import type { ReactNode } from "react";

export interface EmptyStateProps {
  // Lucide icon name — surfaced as a data attribute rather than rendered;
  // no icon library is wired in yet, same posture as ActionButton's icon.
  icon?: string | undefined;
  title: string;
  description?: string | undefined;
  action?: ReactNode | undefined;
}

export function EmptyState({ icon, title, description, action }: EmptyStateProps): ReactNode {
  return (
    <div data-icon={icon} className="flex flex-col items-center py-6 text-center">
      <h3 className="font-medium text-md text-text">{title}</h3>
      {description !== undefined && <p className="mt-1 max-w-sm text-sm text-text-secondary">{description}</p>}
      {action !== undefined && <div className="mt-4">{action}</div>}
    </div>
  );
}
