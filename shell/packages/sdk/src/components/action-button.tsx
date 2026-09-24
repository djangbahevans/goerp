import type { ReactNode } from "react";
import { useOptionalPermission } from "../auth/use-permission.js";
import { Button } from "./button.js";
import type { ButtonSize } from "./button-styles.js";
import type { IconNameLike } from "./icon.js";

export type ActionButtonVariant = "primary" | "secondary" | "ghost" | "danger";
export type ActionButtonSize = ButtonSize;

export interface ActionButtonProps {
  permission?: string | undefined;
  onClick: () => void;
  loading?: boolean | undefined;
  disabled?: boolean | undefined;
  variant?: ActionButtonVariant | undefined;
  size?: ActionButtonSize | undefined;
  // Lucide icon name (manifest-spec.md's Action.icon), shown before
  // `children` — replaced by the loading spinner while `loading`.
  icon?: IconNameLike | undefined;
  children: ReactNode;
}

export function ActionButton({
  permission,
  onClick,
  loading,
  disabled,
  variant,
  size,
  icon,
  children,
}: ActionButtonProps): ReactNode {
  const allowed = useOptionalPermission(permission);
  if (!allowed) return null;

  return (
    <Button variant={variant} size={size} icon={icon} loading={loading} disabled={disabled} onClick={() => onClick()}>
      {children}
    </Button>
  );
}
