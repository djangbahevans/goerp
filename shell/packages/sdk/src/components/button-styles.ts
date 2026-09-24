export type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "link";
export type ButtonSize = "sm" | "md";
export type IconButtonVariant = "ghost" | "secondary" | "danger";

export const BUTTON_ICON_SIZE: Record<ButtonSize, number> = { sm: 14, md: 16 };

const BASE_CLASSES = [
  "inline-flex items-center gap-2 whitespace-nowrap rounded-control",
  "transition-colors ease-out duration-(--duration-fast) motion-reduce:transition-none",
  "focus-visible:outline-none focus-visible:shadow-focus",
  "data-[disabled=true]:cursor-not-allowed data-[disabled=true]:opacity-50",
].join(" ");

const SIZE_CLASSES: Record<ButtonSize, string> = {
  md: "h-9 px-3",
  sm: "h-7 px-2",
};

const FILLED_VARIANT_CLASSES: Record<Exclude<ButtonVariant, "link">, string> = {
  primary: "bg-primary text-text-inverse hover:bg-primary-hover active:bg-primary-active",
  secondary:
    "bg-surface text-text border border-border hover:bg-surface-hover hover:border-border-strong active:bg-surface-active",
  ghost: "bg-transparent text-text hover:bg-surface-hover active:bg-surface-active",
  danger: "bg-danger text-text-inverse hover:bg-danger-hover active:bg-danger-active",
};

const LINK_CLASSES = "text-primary font-normal underline-offset-2 hover:underline";

export function buttonClassName(variant: ButtonVariant, size: ButtonSize, fullWidth: boolean): string {
  const variantClasses =
    variant === "link"
      ? LINK_CLASSES
      : `justify-center text-sm font-medium ${SIZE_CLASSES[size]} ${FILLED_VARIANT_CLASSES[variant]}`;
  return `${BASE_CLASSES} ${variantClasses}${fullWidth ? " w-full justify-center" : ""}`;
}

const ICON_SIZE_CLASSES: Record<ButtonSize, string> = { md: "size-9", sm: "size-7" };

const ICON_VARIANT_CLASSES: Record<IconButtonVariant, string> = {
  ghost: "bg-transparent text-text-secondary hover:bg-surface-hover hover:text-text active:bg-surface-active",
  secondary: FILLED_VARIANT_CLASSES.secondary,
  danger: FILLED_VARIANT_CLASSES.danger,
};

const PRESSED_CLASSES = "bg-primary-subtle text-primary";

export function iconButtonClassName(variant: IconButtonVariant, size: ButtonSize, pressed: boolean): string {
  return `${BASE_CLASSES} shrink-0 justify-center ${ICON_SIZE_CLASSES[size]} ${
    pressed ? PRESSED_CLASSES : ICON_VARIANT_CLASSES[variant]
  }`;
}
