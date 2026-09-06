import type { ReactElement, ReactNode } from "react";
import { Children } from "react";

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

  return (
    <div>
      <div role="tablist" className="flex gap-2 border-border border-b">
        {items.map((item) => (
          <button
            key={item.id}
            type="button"
            role="tab"
            aria-selected={item.id === activeId}
            data-selected={item.id === activeId}
            data-icon={item.icon}
            disabled={item.disabled}
            onClick={() => onChange(item.id)}
          >
            {item.badge !== undefined ? `${item.label} ${item.badge}` : item.label}
          </button>
        ))}
      </div>
      <div role="tabpanel">{activePanel}</div>
    </div>
  );
}
