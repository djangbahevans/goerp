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

export function TabPanel({ children }: TabPanelProps): ReactNode {
  return children;
}

export interface TabsProps {
  items: TabItem[];
  activeId: string;
  onChange: (id: string) => void;
  children: ReactElement<TabPanelProps> | ReactElement<TabPanelProps>[];
}

export function Tabs({ items, activeId, onChange, children }: TabsProps): ReactNode {
  const panels = Children.toArray(children) as ReactElement<TabPanelProps>[];
  const activePanel = panels.find((panel) => panel.props.id === activeId);
  const tabRefsRef = useRef<Map<string, HTMLButtonElement> | null>(null);
  tabRefsRef.current ??= new Map();
  const tabRefs = tabRefsRef.current;

  const handleTabListKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    if (items.every((item) => item.disabled)) return;
    event.preventDefault();
    const direction = event.key === "ArrowRight" ? 1 : -1;
    // DOM focus can differ from the externally controlled active tab.
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
      <div
        role="tablist"
        className="flex gap-4 overflow-x-auto border-border border-b"
        onKeyDown={handleTabListKeyDown}
      >
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
              // An active disabled tab remains focusable as the tablist entry point.
              aria-disabled={item.disabled}
              data-selected={selected}
              data-icon={item.icon}
              tabIndex={selected ? 0 : -1}
              onClick={() => {
                if (item.disabled) return;
                onChange(item.id);
              }}
              className={`-mb-px flex shrink-0 items-center gap-1.5 whitespace-nowrap border-b-2 font-medium text-sm transition-colors duration-(--duration-fast) ease-out focus-visible:shadow-focus focus-visible:outline-none aria-disabled:cursor-not-allowed aria-disabled:opacity-50 max-md:min-h-11 max-md:min-w-11 max-md:justify-center ${
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
