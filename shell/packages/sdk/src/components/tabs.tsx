import type { KeyboardEvent, ReactElement, ReactNode } from "react";
import { Children, useRef } from "react";

export interface TabItem {
  id: string;
  label: string;
  // Lucide icon name — surfaced as a data attribute rather than rendered;
  // no icon library is wired in yet, same posture as ActionButton's icon.
  icon?: string | undefined;
  badge?: number | undefined;
  disabled?: boolean | undefined;
}

export interface TabPanelProps {
  id: string;
  children: ReactNode;
}

// Renders as its own children when active — Tabs picks which one to mount.
export function TabPanel({ children }: TabPanelProps): ReactNode {
  return children;
}

export interface TabsProps {
  items: TabItem[];
  activeId: string;
  onChange: (id: string) => void;
  children: ReactElement<TabPanelProps> | ReactElement<TabPanelProps>[];
}

// Controlled (activeId/onChange), not internal state — the generic form
// renderer needs to know the active tab to lazily mount its content and to
// read/write it from the URL for deep-linking. Same
// role="tablist"/"tab"/"tabpanel" pattern as form-tabs.tsx's manifest-driven
// FormTab renderer.
export function Tabs({ items, activeId, onChange, children }: TabsProps): ReactNode {
  const panels = Children.toArray(children) as ReactElement<TabPanelProps>[];
  const activePanel = panels.find((panel) => panel.props.id === activeId);
  const tabRefsRef = useRef<Map<string, HTMLButtonElement> | null>(null);
  tabRefsRef.current ??= new Map();
  const tabRefs = tabRefsRef.current;

  // Automatic activation, per the standard role="tablist" pattern: moving
  // the roving focus with an arrow key also switches the active tab
  // (rather than requiring a separate Enter/Space to activate the newly
  // focused one) — disabled tabs are skipped entirely, wrapping at the ends.
  const handleTabListKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    if (items.every((item) => item.disabled)) return;
    event.preventDefault();
    const direction = event.key === "ArrowRight" ? 1 : -1;
    // Steps from wherever DOM focus actually is, not from `activeId` —
    // `activeId` is externally controlled (e.g. synced from the URL for
    // deep-linking) and can change without moving focus, which would
    // otherwise make this step from the wrong tab. Positioned within the
    // full `items` list (not just the enabled ones), so stepping still
    // moves in the requested direction even starting from a disabled-but-
    // focused active tab, rather than both arrow keys collapsing to the
    // same fallback index.
    const currentIndex = items.findIndex((item) => tabRefs.get(item.id) === document.activeElement);
    let cursor = currentIndex === -1 ? (direction === 1 ? -1 : items.length) : currentIndex;
    let next: TabItem | undefined;
    for (let step = 0; step < items.length; step++) {
      cursor = (cursor + direction + items.length) % items.length;
      if (!items[cursor]?.disabled) {
        next = items[cursor];
        break;
      }
    }
    if (!next) return;
    onChange(next.id);
    tabRefs.get(next.id)?.focus();
  };

  return (
    <div>
      <div role="tablist" className="flex gap-4 border-border border-b" onKeyDown={handleTabListKeyDown}>
        {items.map((item) => {
          const selected = item.id === activeId;
          return (
            <button
              key={item.id}
              ref={(el) => {
                if (el) tabRefs.set(item.id, el);
                else tabRefs.delete(item.id);
              }}
              type="button"
              role="tab"
              aria-selected={selected}
              // Not the native `disabled` attribute: that would make a
              // disabled-but-active tab (the only tab with tabIndex=0)
              // completely unfocusable, trapping keyboard focus out of the
              // whole tablist since every other tab is tabIndex=-1.
              aria-disabled={item.disabled}
              data-selected={selected}
              data-icon={item.icon}
              // Roving tabindex: only the active tab is in the native Tab
              // order — arrow keys, not Tab, move between tabs.
              tabIndex={selected ? 0 : -1}
              onClick={() => {
                if (item.disabled) return;
                onChange(item.id);
              }}
              className={`-mb-px flex items-center gap-1.5 border-b-2 font-medium text-sm transition-colors duration-(--duration-fast) ease-out focus-visible:outline-none focus-visible:shadow-focus aria-disabled:cursor-not-allowed aria-disabled:opacity-50 ${
                selected
                  ? "border-primary text-text"
                  : "border-transparent text-text-secondary hover:text-text aria-disabled:hover:text-text-secondary"
              }`}
            >
              {item.label}
              {item.badge !== undefined && (
                <>
                  {" "}
                  <span className="rounded-full bg-primary-subtle px-1.5 py-0.5 text-primary text-xs">
                    {item.badge}
                  </span>
                </>
              )}
            </button>
          );
        })}
      </div>
      <div role="tabpanel">{activePanel}</div>
    </div>
  );
}
