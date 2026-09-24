import type { ComponentPropsWithRef, MouseEvent, ReactNode } from "react";
import { BUTTON_ICON_SIZE, type ButtonSize, type ButtonVariant, buttonClassName } from "./button-styles.js";
import { Icon, type IconNameLike } from "./icon.js";
import { Spinner } from "./spinner.js";

export type { ButtonSize, ButtonVariant };

interface ButtonOwnProps {
  variant?: ButtonVariant | undefined;
  size?: ButtonSize | undefined;
  disabled?: boolean | undefined;
  // Lucide icon name shown before `children`.
  icon?: IconNameLike | undefined;
  fullWidth?: boolean | undefined;
  children: ReactNode;
}

export type ButtonAsButtonProps = ButtonOwnProps &
  Omit<ComponentPropsWithRef<"button">, keyof ButtonOwnProps | "className" | "style" | "type"> & {
    type?: "button" | "submit" | "reset" | undefined;
    loading?: boolean | undefined;
    href?: never;
  };

export type ButtonAsLinkProps = ButtonOwnProps &
  Omit<ComponentPropsWithRef<"a">, keyof ButtonOwnProps | "className" | "style" | "href"> & {
    href: string | undefined;
  };

export type ButtonProps = ButtonAsButtonProps | ButtonAsLinkProps;

function ButtonContent({
  icon,
  size,
  loading,
  children,
}: {
  icon: IconNameLike | undefined;
  size: ButtonSize;
  loading: boolean;
  children: ReactNode;
}): ReactNode {
  return (
    <>
      {loading ? (
        <Spinner size={BUTTON_ICON_SIZE[size]} />
      ) : (
        icon && <Icon name={icon} size={BUTTON_ICON_SIZE[size]} className="shrink-0" aria-hidden="true" />
      )}
      {children}
    </>
  );
}

// docs/components/button.md. Link mode keys off the presence of `href`, not
// its value: TanStack Router's createLink passes `href: undefined` for a
// disabled link.
export function Button(props: ButtonProps): ReactNode {
  if (isLinkProps(props)) return <LinkButton {...props} />;
  return <NativeButton {...props} />;
}

function isLinkProps(props: ButtonProps): props is ButtonAsLinkProps {
  return "href" in props;
}

function NativeButton({
  variant = "secondary",
  size = "md",
  type = "button",
  loading = false,
  disabled = false,
  icon,
  fullWidth = false,
  onClick,
  children,
  className: _className,
  style: _style,
  ...rest
}: ButtonAsButtonProps & { className?: unknown; style?: unknown }): ReactNode {
  // Loading keeps the button enabled so it keeps focus (a submit just
  // pressed); clicks are swallowed instead, preventDefault stopping a
  // second form submission.
  const handleClick = (event: MouseEvent<HTMLButtonElement>) => {
    if (loading) {
      event.preventDefault();
      return;
    }
    onClick?.(event);
  };
  return (
    <button
      {...rest}
      type={type}
      data-variant={variant}
      data-size={size}
      data-disabled={disabled ? "true" : undefined}
      disabled={disabled}
      aria-busy={loading || undefined}
      aria-disabled={loading || undefined}
      onClick={handleClick}
      className={buttonClassName(variant, size, fullWidth)}
    >
      <ButtonContent icon={icon} size={size} loading={loading}>
        {children}
      </ButtonContent>
    </button>
  );
}

function LinkButton({
  variant = "secondary",
  size = "md",
  disabled = false,
  icon,
  fullWidth = false,
  href,
  onClick,
  children,
  className: _className,
  style: _style,
  ...rest
}: ButtonAsLinkProps & { className?: unknown; style?: unknown }): ReactNode {
  return (
    <a
      {...rest}
      href={disabled ? undefined : href}
      onClick={disabled ? undefined : onClick}
      data-variant={variant}
      data-size={size}
      data-disabled={disabled ? "true" : undefined}
      aria-disabled={disabled || undefined}
      className={buttonClassName(variant, size, fullWidth)}
    >
      <ButtonContent icon={icon} size={size} loading={false}>
        {children}
      </ButtonContent>
    </a>
  );
}
