import type { ComponentPropsWithRef, ReactNode } from "react";
import { BUTTON_ICON_SIZE, type ButtonSize, type IconButtonVariant, iconButtonClassName } from "./button-styles.js";
import { Icon, type IconNameLike } from "./icon.js";

export type { IconButtonVariant };

export interface IconButtonProps
  extends Omit<
    ComponentPropsWithRef<"button">,
    "children" | "className" | "style" | "type" | "aria-label" | "aria-pressed"
  > {
  icon: IconNameLike;
  label: string;
  variant?: IconButtonVariant | undefined;
  size?: ButtonSize | undefined;
  // Toggle state; omit for a plain action so no aria-pressed is rendered.
  pressed?: boolean | undefined;
  type?: "button" | "submit" | "reset" | undefined;
  disabled?: boolean | undefined;
}

export function IconButton({
  icon,
  label,
  variant = "ghost",
  size = "md",
  pressed,
  type = "button",
  disabled = false,
  className: _className,
  style: _style,
  ...rest
}: IconButtonProps & { className?: unknown; style?: unknown }): ReactNode {
  return (
    <button
      {...rest}
      type={type}
      aria-label={label}
      aria-pressed={pressed}
      data-variant={variant}
      data-size={size}
      data-disabled={disabled ? "true" : undefined}
      disabled={disabled}
      className={iconButtonClassName(variant, size, pressed === true)}
    >
      <Icon name={icon} size={BUTTON_ICON_SIZE[size]} aria-hidden="true" />
    </button>
  );
}
