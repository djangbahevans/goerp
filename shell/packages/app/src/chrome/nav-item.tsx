import { Icon } from "@goerp/sdk/components";
import { defaultParseSearch, useRouter } from "@tanstack/react-router";
import type { CSSProperties, MouseEvent, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import { navItemParams } from "./nav-active.js";
import { NavBadge } from "./nav-badge.js";
import type { NavigationItem } from "./navigation-types.js";

const TOOLTIP_DELAY_MS = 500;

type NavTarget = { to: string; search?: Record<string, unknown>; state?: Record<string, unknown> };

// insetInlineStart is a native logical CSS property (applied via inline
// style, not a generated Tailwind utility), so it already flips correctly
// under dir="rtl" with no separate RTL handling needed.
const ACTIVE_BAR_STYLE: CSSProperties = {
  position: "absolute",
  insetInlineStart: 0,
  top: 0,
  bottom: 0,
  width: "2px",
  backgroundColor: "var(--color-primary)",
};

const TOOLTIP_STYLE: CSSProperties = {
  position: "absolute",
  insetInlineStart: "100%",
  top: "50%",
  transform: "translateY(-50%)",
  marginInlineStart: "var(--space-2)",
  padding: "var(--space-1) var(--space-2)",
  borderRadius: "var(--radius-control)",
  backgroundColor: "var(--color-surface)",
  color: "var(--color-text)",
  boxShadow: "var(--shadow-md)",
  zIndex: "var(--z-tooltip)",
  whiteSpace: "nowrap",
  pointerEvents: "none",
};

const BASE_CLASSES =
  "flex items-center gap-2 rounded-control px-3 py-2 text-sm focus-visible:shadow-focus focus-visible:outline-none";

// chrome-sidebar.md: collapsed-mode label tooltip, 500ms hover/focus delay.
// No existing hover-after-delay component in the codebase to reuse and no
// @radix-ui/react-tooltip dependency yet — hand-rolled rather than adding a
// new dependency for one component's affordance.
export function NavItem({
  item,
  collapsed,
  active = false,
}: {
  item: NavigationItem;
  collapsed: boolean;
  active?: boolean;
}): ReactNode {
  const router = useRouter();
  // The router's own parse, so `filter[is_customer]=true` is the same search a typed URL gives.
  const target: NavTarget = {
    to: item.path,
    ...(item.search ? { search: defaultParseSearch(`?${new URLSearchParams(item.search)}`) } : {}),
    ...(item.defaultFilters ? { state: { navDefaultFilters: item.defaultFilters } } : {}),
  };
  // The link's own address carries the defaults as ordinary filters, so a new tab, a copied link or
  // a bookmark opens the same list; a click carries them in history state, where a saved default can outrank them.
  const href = item.external
    ? item.path
    : (router.buildLocation as unknown as (target: NavTarget) => { href: string })({
        to: item.path,
        search: defaultParseSearch(`?${new URLSearchParams(navItemParams(item))}`),
      }).href;

  function open(event: MouseEvent<HTMLAnchorElement>): void {
    if (
      event.defaultPrevented ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    ) {
      return;
    }
    event.preventDefault();
    void (router.navigate as unknown as (target: NavTarget) => Promise<void>)(target);
  }

  const [tooltipVisible, setTooltipVisible] = useState(false);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

  function scheduleTooltip(): void {
    if (!collapsed) return;
    timeoutRef.current = setTimeout(() => setTooltipVisible(true), TOOLTIP_DELAY_MS);
  }

  function cancelTooltip(): void {
    clearTimeout(timeoutRef.current);
    setTooltipVisible(false);
  }

  // Navigating away (a click, or Enter while a tooltip's 500ms delay is
  // still pending) unmounts this component without firing onMouseLeave —
  // without this, the pending timer would still call setTooltipVisible on
  // an unmounted component.
  useEffect(() => () => clearTimeout(timeoutRef.current), []);

  const rowContent = (isActive: boolean): ReactNode => (
    <>
      {isActive && <span aria-hidden="true" style={ACTIVE_BAR_STYLE} />}
      {collapsed ? (
        // The badge overlays the icon's own corner, so it needs a
        // relative ancestor sized to the icon — not the full-width
        // row (which would anchor it to the row's edge instead).
        <span className="relative inline-flex">
          <Icon name={item.icon} size={16} aria-hidden="true" />
          {item.badgeCountRoute && <NavBadge route={item.badgeCountRoute} collapsed />}
        </span>
      ) : (
        <>
          <Icon name={item.icon} size={16} aria-hidden="true" />
          <span className="flex-1">{item.label}</span>
          {item.badgeCountRoute && <NavBadge route={item.badgeCountRoute} />}
        </>
      )}
    </>
  );

  return (
    <div className="relative">
      {item.external ? (
        // manifest-spec.md §12 NavItem.external — an external URL has no
        // router "active" concept, so isActive is always false here.
        <a
          href={item.path}
          target="_blank"
          rel="noopener noreferrer"
          aria-label={collapsed ? item.label : undefined}
          onMouseEnter={scheduleTooltip}
          onMouseLeave={cancelTooltip}
          onFocus={scheduleTooltip}
          onBlur={cancelTooltip}
          className={`${BASE_CLASSES} text-text-secondary hover:bg-surface-hover`}
        >
          {rowContent(false)}
        </a>
      ) : (
        // The active item is chosen across the whole tree (nav-active.ts), so this is a plain
        // anchor rather than a Link, whose own path-only check would also mark its siblings.
        <a
          href={href}
          onClick={open}
          aria-current={active ? "page" : undefined}
          aria-label={collapsed ? item.label : undefined}
          onMouseEnter={scheduleTooltip}
          onMouseLeave={cancelTooltip}
          onFocus={scheduleTooltip}
          onBlur={cancelTooltip}
          className={`${BASE_CLASSES} ${active ? "bg-primary-subtle font-medium text-primary" : "text-text-secondary hover:bg-surface-hover"}`}
        >
          {rowContent(active)}
        </a>
      )}
      {collapsed && tooltipVisible && (
        <span role="tooltip" style={TOOLTIP_STYLE}>
          {item.label}
        </span>
      )}
    </div>
  );
}
