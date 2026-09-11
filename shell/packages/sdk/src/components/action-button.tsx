import type { ReactNode } from "react";
import { useOptionalPermission } from "../auth/use-permission.js";
import { type ActionButtonSize, type ActionButtonVariant, actionButtonClassName } from "./action-button-styles.js";
import { Icon } from "./icon.js";
import { Spinner } from "./spinner.js";

export type { ActionButtonSize, ActionButtonVariant };

export interface ActionButtonProps {
  permission?: string | undefined;
  onClick: () => void;
  loading?: boolean | undefined;
  disabled?: boolean | undefined;
  variant?: ActionButtonVariant | undefined;
  size?: ActionButtonSize | undefined;
  // Lucide icon name (manifest-spec.md's Action.icon), shown before
  // `children` — replaced by the loading spinner while `loading`.
  icon?: string | undefined;
  children: ReactNode;
}

// Shared by the spinner and the icon, so `loading` swaps one for the
// other at the same size instead of visibly resizing the button.
const ICON_DIMENSION: Record<ActionButtonSize, number> = { sm: 14, md: 16 };

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
      {loading ? (
        <Spinner size={ICON_DIMENSION[size]} />
      ) : (
        icon && <Icon name={icon} size={ICON_DIMENSION[size]} className="shrink-0" aria-hidden="true" />
      )}
      {children}
    </button>
  );
}
