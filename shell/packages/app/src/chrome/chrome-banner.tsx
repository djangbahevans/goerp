import { IconButton } from "@goerp/sdk/components";
import { Link } from "@tanstack/react-router";
import { Info, type LucideIcon, TriangleAlert } from "lucide-react";
import type { ReactNode } from "react";

export type ChromeBannerTone = "warning" | "info";

export interface ChromeBannerAction {
  label: string;
  to: string;
  hash?: string | undefined;
}

export interface ChromeBannerProps {
  tone: ChromeBannerTone;
  icon?: LucideIcon | undefined;
  children: ReactNode;
  action?: ChromeBannerAction | undefined;
  onDismiss?: (() => void) | undefined;
  dismissLabel?: string | undefined;
}

const TONE_CLASSES: Record<ChromeBannerTone, { row: string; icon: string }> = {
  warning: { row: "bg-warning-subtle", icon: "text-warning" },
  info: { row: "bg-info-subtle", icon: "text-info" },
};

const DEFAULT_ICONS: Record<ChromeBannerTone, LucideIcon> = { warning: TriangleAlert, info: Info };

// components/chrome-banner.md: a full-width session notice rendered by
// ChromeBanners between ChromeHeader and <main>. Below 576px of banner width
// (a container query, since the sidebar takes its share of the viewport)
// the action wraps below the message while the dismiss button stays top-end.
export function ChromeBanner({
  tone,
  icon,
  children,
  action,
  onDismiss,
  dismissLabel = "Dismiss",
}: ChromeBannerProps): ReactNode {
  const ToneIcon = icon ?? DEFAULT_ICONS[tone];
  const classes = TONE_CLASSES[tone];

  return (
    <div
      role="status"
      className={`@container flex items-start gap-2 border-border border-b px-4 py-2 text-sm text-text ${classes.row}`}
    >
      <ToneIcon size={16} aria-hidden="true" className={`mt-0.5 shrink-0 ${classes.icon}`} />
      <div className="flex min-w-0 flex-1 flex-col gap-1 @xl:flex-row @xl:items-baseline @xl:gap-3">
        <p className="min-w-0">{children}</p>
        {action && (
          <Link
            to={action.to}
            {...(action.hash !== undefined && { hash: action.hash })}
            className="shrink-0 rounded-control text-primary hover:underline focus-visible:shadow-focus focus-visible:outline-none"
          >
            {action.label}
          </Link>
        )}
      </div>
      {onDismiss && <IconButton icon="x" label={dismissLabel} size="sm" onClick={onDismiss} />}
    </div>
  );
}
