import { Link } from "@tanstack/react-router";
import type { CSSProperties, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import { NavBadge } from "./nav-badge.js";
import type { NavigationItem } from "./navigation-types.js";

const TOOLTIP_DELAY_MS = 500;

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
export function NavItem({ item, collapsed }: { item: NavigationItem; collapsed: boolean }): ReactNode {
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

  const Icon = item.icon;

  return (
    <div className="relative">
      {/* Link computes active/current state from real router state (its
          own STATIC_ACTIVE_PROPS sets aria-current/data-status internally)
          — activeProps/inactiveProps are mutually exclusive per render, so
          the default/active color pair never fights over one className. */}
      <Link
        to={item.path}
        aria-label={collapsed ? item.label : undefined}
        onMouseEnter={scheduleTooltip}
        onMouseLeave={cancelTooltip}
        onFocus={scheduleTooltip}
        onBlur={cancelTooltip}
        className={BASE_CLASSES}
        inactiveProps={{ className: "text-text-secondary hover:bg-surface-hover" }}
        activeProps={{ className: "bg-primary-subtle font-medium text-primary" }}
      >
        {({ isActive }) => (
          <>
            {isActive && <span aria-hidden="true" style={ACTIVE_BAR_STYLE} />}
            {collapsed ? (
              // The badge overlays the icon's own corner, so it needs a
              // relative ancestor sized to the icon — not the full-width
              // row (which would anchor it to the row's edge instead).
              <span className="relative inline-flex">
                <Icon size={16} aria-hidden="true" />
                {item.badgeCountRoute && <NavBadge route={item.badgeCountRoute} collapsed />}
              </span>
            ) : (
              <>
                <Icon size={16} aria-hidden="true" />
                <span className="flex-1">{item.label}</span>
                {item.badgeCountRoute && <NavBadge route={item.badgeCountRoute} />}
              </>
            )}
          </>
        )}
      </Link>
      {collapsed && tooltipVisible && (
        <span role="tooltip" style={TOOLTIP_STYLE}>
          {item.label}
        </span>
      )}
    </div>
  );
}
