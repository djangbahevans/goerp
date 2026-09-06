import type { ReactElement, ReactNode } from "react";
import { Children, useState } from "react";

export interface TabPanelProps {
  label: string;
  children: ReactNode;
}

// Renders as its own children when active — Tabs picks which one to mount.
export function TabPanel({ children }: TabPanelProps): ReactNode {
  return children;
}

export interface TabsProps {
  children: ReactElement<TabPanelProps> | ReactElement<TabPanelProps>[];
}

// Same role="tablist"/"tab"/"tabpanel" pattern as form-tabs.tsx's
// manifest-driven FormTab renderer, for a hand-written view's own tabs.
export function Tabs({ children }: TabsProps): ReactNode {
  const panels = Children.toArray(children) as ReactElement<TabPanelProps>[];
  const [active, setActive] = useState(0);
  const activePanel = panels[active];

  return (
    <div>
      <div role="tablist" className="flex gap-2 border-border border-b">
        {panels.map((panel, i) => (
          <button
            key={panel.props.label}
            type="button"
            role="tab"
            aria-selected={i === active}
            data-selected={i === active}
            onClick={() => setActive(i)}
          >
            {panel.props.label}
          </button>
        ))}
      </div>
      <div role="tabpanel">{activePanel}</div>
    </div>
  );
}
