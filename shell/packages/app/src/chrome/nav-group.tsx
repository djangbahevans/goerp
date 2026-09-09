import { ChevronRight } from "lucide-react";
import type { CSSProperties, ReactNode } from "react";
import { NavItem } from "./nav-item.js";
import type { NavigationGroup } from "./navigation-types.js";

// rotate-90 generates no CSS in this project's @tailwindcss/vite setup
// (confirmed empirically, goerp#729) — inline style instead, same pattern
// as user-menu.tsx's chevronStyle.
function chevronStyle(expanded: boolean): CSSProperties {
  return { transform: expanded ? "rotate(90deg)" : undefined };
}

// ms-4/border-s/ps-2 (shell-architecture.md §16's indent guide) all
// generate no CSS either — logical margin/border/padding, same goerp#729
// bug. insetInlineStart-style logical properties applied via inline style
// work fine and flip correctly under dir="rtl" on their own.
const INDENT_STYLE: CSSProperties = {
  marginInlineStart: "var(--space-4)",
  borderInlineStart: "1px solid var(--color-border)",
  paddingInlineStart: "var(--space-2)",
};

export function NavGroupSection({
  group,
  collapsed,
  expanded,
  onToggle,
}: {
  group: NavigationGroup;
  collapsed: boolean;
  expanded: boolean;
  onToggle: (key: string) => void;
}): ReactNode {
  const Icon = group.icon;

  return (
    // biome-ignore lint/a11y/useSemanticElements: role="group" labels a nav section, not a form's field grouping — <fieldset> would be the wrong element here.
    <div role="group" aria-labelledby={`nav-group-${group.key}`}>
      <button
        id={`nav-group-${group.key}`}
        type="button"
        onClick={() => onToggle(group.key)}
        aria-expanded={expanded}
        aria-label={collapsed ? group.label : undefined}
        className="flex w-full items-center gap-2 rounded-control px-3 py-2 text-sm text-text-secondary hover:bg-surface-hover"
      >
        <Icon size={16} aria-hidden="true" />
        {!collapsed && (
          <>
            <span className="flex-1">{group.label}</span>
            <ChevronRight
              size={14}
              aria-hidden="true"
              className="transition-transform duration-(--duration-base)"
              style={chevronStyle(expanded)}
            />
          </>
        )}
      </button>
      {expanded && (
        // Collapsed mode has no room for the nesting guide (56px rail minus
        // this indent left an icon column too narrow for its own content,
        // confirmed empirically in Storybook) — there's also no group label
        // visible to indent away from, so the guide has nothing to indicate.
        <div style={collapsed ? undefined : INDENT_STYLE}>
          {group.children.map((item) => (
            <NavItem key={item.key} item={item} collapsed={collapsed} />
          ))}
        </div>
      )}
    </div>
  );
}
