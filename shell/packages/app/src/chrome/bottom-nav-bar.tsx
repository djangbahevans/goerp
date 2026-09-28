import { IconButton, MODAL_OVERLAY_CLASSES } from "@goerp/sdk/components";
import { useUnreadCount } from "@goerp/sdk/notifications";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { Link, useRouterState } from "@tanstack/react-router";
import { Bell, CalendarCheck, House, type LucideIcon, Menu, Search } from "lucide-react";
import { type MouseEvent, type ReactNode, useRef, useState } from "react";
import { useDueActivityCount } from "../activities/use-due-activity-count.js";
import { openCommandPalette } from "./command-palette-control.js";
import { NavGroupSection } from "./nav-group.js";
import type { NavigationGroup } from "./navigation-types.js";
import { NotificationSheet } from "./notification-sheet.js";
import type { SidebarStoreLike } from "./sidebar-store.js";
import { useSidebar } from "./sidebar-store.js";
import { useNavigationTree } from "./use-navigation-tree.js";

const BADGE_CAP = 99;

// The full cell is the hit area, 56px tall and a fifth of the width — at
// least 44×44px down to a 220px-wide viewport (shell-architecture.md §23).
const ITEM_CLASSES =
  "relative flex min-h-11 min-w-11 flex-1 flex-col items-center justify-center gap-0.5 text-xs focus-visible:shadow-focus focus-visible:outline-none";
const INACTIVE_CLASSES = "text-text-secondary hover:text-text";
const ACTIVE_CLASSES = "text-primary";

const SHEET_CLASSES =
  "fixed inset-x-0 top-8 bottom-0 z-(--z-modal) flex flex-col rounded-t-structural bg-surface shadow-lg focus:outline-none data-[state=open]:animate-[bottom-sheet-content-show_var(--duration-base)_ease-out] data-[state=closed]:animate-[bottom-sheet-content-hide_var(--duration-fast)_ease-in] motion-reduce:data-[state=open]:animate-[fade-in_var(--duration-base)_ease-out] motion-reduce:data-[state=closed]:animate-[fade-out_var(--duration-fast)_ease-in]";

function capped(count: number): string {
  return count > BADGE_CAP ? `${BADGE_CAP}+` : String(count);
}

function ItemContent({ icon: IconGlyph, label, badge }: { icon: LucideIcon; label: string; badge?: number }) {
  return (
    <>
      <span className="relative inline-flex">
        <IconGlyph size={20} aria-hidden="true" />
        {badge !== undefined && badge > 0 && (
          <span
            aria-hidden="true"
            className="-top-1.5 -inset-e-2.5 absolute min-w-4 rounded-full bg-primary px-1 text-center font-medium text-[10px] text-text-inverse leading-4"
          >
            {capped(badge)}
          </span>
        )}
      </span>
      <span aria-hidden="true">{label}</span>
    </>
  );
}

function withCount(label: string, count: number, noun: string): string {
  return count > 0 ? `${label}, ${capped(count)} ${noun}` : label;
}

export interface BottomNavBarProps {
  // Storybook/tests only, as on ChromeSidebar.
  tree?: NavigationGroup[] | undefined;
  store?: SidebarStoreLike | undefined;
}

// chrome-sidebar.md "Bottom bar": below 768px, the rail's replacement —
// five fixed destinations, with every module's navigation under More.
export function BottomNavBar({ tree, store }: BottomNavBarProps): ReactNode {
  const pathname = useRouterState({ select: (s) => (s.resolvedLocation ?? s.location).pathname });
  const dueActivities = useDueActivityCount();
  const { count: unread } = useUnreadCount();
  const [notificationsOpen, setNotificationsOpen] = useState(false);

  const homeCurrent = pathname === "/";
  const activitiesCurrent = pathname === "/activities" || pathname.startsWith("/activities/");

  return (
    <>
      <nav
        aria-label="Main"
        className="fixed inset-x-0 bottom-0 z-(--z-sticky) flex h-[calc(var(--bottom-nav-height)+env(safe-area-inset-bottom))] border-border border-t bg-surface pb-[env(safe-area-inset-bottom)]"
      >
        <Link
          to="/"
          aria-label="Home"
          aria-current={homeCurrent ? "page" : undefined}
          className={`${ITEM_CLASSES} ${homeCurrent ? ACTIVE_CLASSES : INACTIVE_CLASSES}`}
        >
          <ItemContent icon={House} label="Home" />
        </Link>
        <button
          type="button"
          aria-label="Search"
          onClick={openCommandPalette}
          className={`${ITEM_CLASSES} ${INACTIVE_CLASSES}`}
        >
          <ItemContent icon={Search} label="Search" />
        </button>
        <Link
          to="/activities"
          aria-label={withCount("Activities", dueActivities, "due")}
          aria-current={activitiesCurrent ? "page" : undefined}
          className={`${ITEM_CLASSES} ${activitiesCurrent ? ACTIVE_CLASSES : INACTIVE_CLASSES}`}
        >
          <ItemContent icon={CalendarCheck} label="Activities" badge={dueActivities} />
        </Link>
        <button
          type="button"
          aria-label={withCount("Notifications", unread, "unread")}
          aria-expanded={notificationsOpen}
          onClick={() => setNotificationsOpen((open) => !open)}
          className={`${ITEM_CLASSES} ${INACTIVE_CLASSES}`}
        >
          <ItemContent icon={Bell} label="Notifications" badge={unread} />
        </button>
        <MoreSheet tree={tree} store={store} />
      </nav>
      <NotificationSheet open={notificationsOpen} onClose={() => setNotificationsOpen(false)} />
    </>
  );
}

// chrome-sidebar.md "More": the full navigation tree, rendered as the
// expanded rail, in a modal sheet. The More button is the dialog's Trigger,
// so Radix returns focus to it on close, unless a destination was chosen.
function MoreSheet({ tree: treeOverride, store }: BottomNavBarProps): ReactNode {
  const [open, setOpen] = useState(false);
  const { expandedGroups, toggleGroup } = useSidebar(store);
  const tree = useNavigationTree(treeOverride);
  const headingRef = useRef<HTMLHeadingElement>(null);
  const navigated = useRef(false);

  // Choosing a destination closes the sheet; group toggles are buttons, not links.
  const closeOnNavigate = (event: MouseEvent<HTMLDivElement>) => {
    if (event.target instanceof Element && event.target.closest("a[href]")) {
      navigated.current = true;
      setOpen(false);
    }
  };

  return (
    <DialogPrimitive.Root open={open} onOpenChange={setOpen}>
      <DialogPrimitive.Trigger className={`${ITEM_CLASSES} ${INACTIVE_CLASSES}`} aria-label="More">
        <ItemContent icon={Menu} label="More" />
      </DialogPrimitive.Trigger>
      <DialogPrimitive.Portal>
        <DialogPrimitive.Overlay className={MODAL_OVERLAY_CLASSES} />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className={SHEET_CLASSES}
          onOpenAutoFocus={(event) => {
            event.preventDefault();
            headingRef.current?.focus();
          }}
          // A navigation moves focus to <main> (ChromeLayout); returning it to More would undo that.
          onCloseAutoFocus={(event) => {
            if (navigated.current) event.preventDefault();
            navigated.current = false;
          }}
        >
          <div className="flex items-center justify-between border-border border-b p-4">
            <DialogPrimitive.Title
              ref={headingRef}
              tabIndex={-1}
              className="font-semibold text-lg text-text focus:outline-none"
            >
              Menu
            </DialogPrimitive.Title>
            <DialogPrimitive.Close asChild>
              <IconButton icon="x" label="Close" size="sm" />
            </DialogPrimitive.Close>
          </div>
          {/* biome-ignore lint/a11y/noStaticElementInteractions: a delegated listener for the links inside, which carry their own keyboard handling. */}
          {/* biome-ignore lint/a11y/useKeyWithClickEvents: Enter on a link fires this same click. */}
          <div
            className="min-h-0 flex-1 overflow-y-auto p-2 pb-[env(safe-area-inset-bottom)]"
            onClick={closeOnNavigate}
          >
            {tree.map((group) => (
              <NavGroupSection
                key={group.key}
                group={group}
                collapsed={false}
                expanded={expandedGroups.has(group.key)}
                onToggle={toggleGroup}
              />
            ))}
          </div>
        </DialogPrimitive.Content>
      </DialogPrimitive.Portal>
    </DialogPrimitive.Root>
  );
}
