import { Icon } from "@goerp/sdk/components";
import { Link } from "@tanstack/react-router";
import { type CSSProperties, type ReactNode, useEffect, useId, useRef } from "react";
import { useMediaQuery } from "./use-media-query.js";

export interface SectionNavItem {
  to: string;
  label: string;
  icon?: string;
}

export interface SectionNavGroup {
  heading?: string;
  items: SectionNavItem[];
}

export type SectionNavLayout = "wide" | "narrow";

export interface SectionNavProps {
  label: string;
  groups: SectionNavGroup[];
  children: ReactNode;
  // Forces a layout instead of following the viewport; for stories and tests.
  layout?: SectionNavLayout | undefined;
}

// components/section-nav.md: Tailwind's default `md` breakpoint.
const WIDE_QUERY = "(min-width: 768px)";

const ITEM_CLASSES =
  "relative flex items-center gap-2 whitespace-nowrap rounded-control px-3 py-2 text-sm focus-visible:shadow-focus focus-visible:outline-none";

// Inline logical properties so the bar flips under dir="rtl", as in nav-item.tsx.
const START_BAR_STYLE: CSSProperties = {
  position: "absolute",
  insetInlineStart: 0,
  top: 0,
  bottom: 0,
  width: "2px",
  backgroundColor: "var(--color-primary)",
};

const BOTTOM_BAR_STYLE: CSSProperties = {
  position: "absolute",
  insetInline: 0,
  bottom: 0,
  height: "2px",
  backgroundColor: "var(--color-primary)",
};

function SectionNavLink({ item, layout }: { item: SectionNavItem; layout: SectionNavLayout }): ReactNode {
  return (
    <Link
      to={item.to}
      className={ITEM_CLASSES}
      inactiveProps={{ className: "text-text-secondary hover:bg-surface-hover" }}
      activeProps={{ className: "bg-primary-subtle font-medium text-primary" }}
    >
      {({ isActive }) => (
        <>
          {isActive && <span aria-hidden="true" style={layout === "wide" ? START_BAR_STYLE : BOTTOM_BAR_STYLE} />}
          {item.icon && (
            // Fixed box: the dynamic icon loads after mount, and a zero-width
            // placeholder would shift the row after the active item scrolls into view.
            <span className="inline-flex size-4 shrink-0" aria-hidden="true">
              <Icon name={item.icon} size={16} />
            </span>
          )}
          <span>{item.label}</span>
        </>
      )}
    </Link>
  );
}

function WideRail({ groups }: { groups: SectionNavGroup[] }): ReactNode {
  const idPrefix = useId();
  return (
    <div className="sticky top-0 flex flex-col gap-4 px-2 py-4">
      {groups.map((group, index) => {
        const headingId = `${idPrefix}-group-${index}`;
        return (
          <div key={group.heading ?? index}>
            {group.heading && (
              <p id={headingId} className="mb-2 px-3 font-medium text-sm text-text-secondary">
                {group.heading}
              </p>
            )}
            <ul aria-labelledby={group.heading ? headingId : undefined}>
              {group.items.map((item) => (
                <li key={item.to}>
                  <SectionNavLink item={item} layout="wide" />
                </li>
              ))}
            </ul>
          </div>
        );
      })}
    </div>
  );
}

function NarrowRow({ groups }: { groups: SectionNavGroup[] }): ReactNode {
  const listRef = useRef<HTMLUListElement>(null);

  // The active link can start off-screen in a long row; bring it into view once.
  useEffect(() => {
    const active = listRef.current?.querySelector<HTMLElement>('[aria-current="page"]');
    active?.scrollIntoView?.({ block: "nearest", inline: "nearest" });
  }, []);

  return (
    <ul ref={listRef} className="flex gap-1 overflow-x-auto px-4 py-2">
      {groups.flatMap((group) =>
        group.items.map((item) => (
          <li key={item.to} className="shrink-0">
            <SectionNavLink item={item} layout="narrow" />
          </li>
        )),
      )}
    </ul>
  );
}

// components/section-nav.md: the sub-navigation rail for multi-page shell
// sections (/settings, /admin), rendered next to the section's content.
export function SectionNav({ label, groups, children, layout }: SectionNavProps): ReactNode {
  const wideViewport = useMediaQuery(WIDE_QUERY, true);
  const resolved: SectionNavLayout = layout ?? (wideViewport ? "wide" : "narrow");
  const visibleGroups = groups.filter((group) => group.items.length > 0);

  if (resolved === "narrow") {
    return (
      <div className="flex min-h-full flex-col">
        <nav aria-label={label} className="border-border border-b bg-surface">
          <NarrowRow groups={visibleGroups} />
        </nav>
        <div className="min-w-0 flex-1">{children}</div>
      </div>
    );
  }

  return (
    <div className="flex min-h-full">
      <nav aria-label={label} className="w-[224px] shrink-0 border-border border-e bg-surface">
        <WideRail groups={visibleGroups} />
      </nav>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}
