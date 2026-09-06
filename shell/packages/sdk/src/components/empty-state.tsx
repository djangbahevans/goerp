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
    <div data-icon={icon}>
      <h3>{title}</h3>
      {description !== undefined && <p>{description}</p>}
      {action !== undefined && <div>{action}</div>}
    </div>
  );
}
