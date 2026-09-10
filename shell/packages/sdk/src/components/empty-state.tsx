import type { ReactNode } from "react";

export interface EmptyStateProps {
  // Lucide icon name — surfaced as a data attribute rather than rendered;
  // no icon library is wired in yet, same posture as ActionButton's icon.
  icon?: string | undefined;
  title: string;
  description?: string | undefined;
  action?: ReactNode | undefined;
  // "compact" is the quieter, tighter treatment for a routine empty state
  // nested inside other content (e.g. an empty kanban column) rather than
  // occupying a whole page. Defaults to the original full-size treatment.
  size?: "default" | "compact" | undefined;
}

export function EmptyState({ icon, title, description, action, size = "default" }: EmptyStateProps): ReactNode {
  const compact = size === "compact";
  return (
    <div data-icon={icon} className={`flex flex-col items-center text-center ${compact ? "py-3" : "py-6"}`}>
      <h3 className={`font-medium text-text ${compact ? "text-sm" : "text-md"}`}>{title}</h3>
      {description !== undefined && (
        <p className={`mt-1 max-w-sm text-text-secondary ${compact ? "text-xs" : "text-sm"}`}>{description}</p>
      )}
      {action !== undefined && <div className="mt-4">{action}</div>}
    </div>
  );
}
