import type { ReactNode } from "react";
import { useOptionalPermission } from "../auth/use-permission.js";
import {
  type ActionButtonSize,
  type ActionButtonVariant,
  actionButtonClassName,
} from "./action-button-styles.js";

export type { ActionButtonVariant, ActionButtonSize };

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

const SPINNER_DIMENSION: Record<ActionButtonSize, number> = { sm: 14, md: 16 };

function LoadingSpinner({ size }: { size: ActionButtonSize }): ReactNode {
  const dimension = SPINNER_DIMENSION[size];
  return (
    <svg
      aria-hidden="true"
      width={dimension}
      height={dimension}
      viewBox="0 0 24 24"
      fill="none"
      className="animate-spin"
      style={{ animationDuration: "var(--duration-slower)" }}
    >
      <circle cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="3" strokeOpacity="0.25" />
      <path d="M22 12a10 10 0 0 0-10-10" stroke="currentColor" strokeWidth="3" strokeLinecap="round" />
    </svg>
  );
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
      // Keys the opacity-dimmed disabled look off the `disabled` prop
      // specifically, not off the rendered native `disabled` attribute
      // below (also set while `loading`) — a loading button is busy, not
      // inactive, and must stay at full contrast while aria-busy is true.
      data-disabled={disabled ? "true" : undefined}
      disabled={disabled || loading}
      aria-busy={loading}
      onClick={onClick}
      className={actionButtonClassName(variant, size)}
    >
      {loading ? <LoadingSpinner size={size} /> : null}
      {children}
    </button>
  );
}
