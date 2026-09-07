export type ActionButtonVariant = "primary" | "secondary" | "ghost" | "danger";
export type ActionButtonSize = "sm" | "md";

const SIZE_CLASSES: Record<ActionButtonSize, string> = {
  md: "h-9 px-3 text-sm",
  sm: "h-7 px-2 text-sm",
};

const VARIANT_CLASSES: Record<ActionButtonVariant, string> = {
  primary: "bg-primary text-text-inverse hover:bg-primary-hover active:bg-primary-active",
  secondary:
    "bg-surface text-text border border-border hover:bg-surface-hover hover:border-border-strong active:bg-surface-active",
  ghost: "bg-transparent text-text hover:bg-surface-hover active:bg-surface-active",
  danger: "bg-danger text-text-inverse hover:bg-danger-hover active:bg-danger-active",
};

// Shared between ActionButton and AlertDialog's cancel/confirm buttons
// (docs/components/alert-dialog.md "Tokens Used") — as a className builder
// rather than mounting <ActionButton> itself, since ActionButton's
// useOptionalPermission throws outside a PermissionContext.Provider
// regardless of whether `permission` is set, and AlertDialog has no
// permission concept in its own API and must stay usable without one.
export function actionButtonClassName(variant: ActionButtonVariant, size: ActionButtonSize): string {
  return [
    "inline-flex items-center justify-center gap-2 rounded-control font-medium",
    "transition-colors ease-out duration-(--duration-fast)",
    "focus-visible:outline-none focus-visible:shadow-focus",
    "disabled:cursor-not-allowed data-[disabled=true]:opacity-50",
    SIZE_CLASSES[size],
    VARIANT_CLASSES[variant],
  ].join(" ");
}
