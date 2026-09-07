import type { ReactNode } from "react";
import { useOptionalPermission } from "../auth/use-permission.js";

// manifest-spec.md's Action.style.
export type ActionButtonVariant = "primary" | "secondary" | "ghost" | "danger";

export type ActionButtonSize = "sm" | "md";

export interface ActionButtonProps {
  permission?: string | undefined;
  onClick: () => void;
  loading?: boolean | undefined;
  disabled?: boolean | undefined;
  variant?: ActionButtonVariant | undefined;
  size?: ActionButtonSize | undefined;
  // Lucide icon name (manifest-spec.md's Action.icon) — surfaced as a data
  // attribute rather than rendered; no icon library is wired in yet, same
  // posture as field-renderers.tsx's icon_picker field.
  icon?: string | undefined;
  children: ReactNode;
}

export function ActionButton({
  permission,
  onClick,
  loading = false,
  disabled = false,
  variant = "secondary",
  size = "md",
  icon,
  children,
}: ActionButtonProps): ReactNode {
  const allowed = useOptionalPermission(permission);
  if (!allowed) return null;

  return (
    <button
      type="button"
      data-variant={variant}
      data-size={size}
      data-icon={icon}
      disabled={disabled || loading}
      aria-busy={loading}
      onClick={onClick}
    >
      {children}
    </button>
  );
}
