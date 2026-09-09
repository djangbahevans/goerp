import { useSyncExternalStore } from "react";

// shell-architecture.md §16's SidebarState, persisted in localStorage
// (activeItem is deliberately excluded — it's derived from router location,
// per the doc's own comment, not stored here).
export interface SidebarStoreState {
  collapsed: boolean;
  expandedGroups: Set<string>;
}

interface StoredSidebarState {
  collapsed: boolean;
  expandedGroups: string[];
}

const STORAGE_KEY = "goerp-sidebar";
type Listener = (state: SidebarStoreState) => void;

function hasDocument(): boolean {
  return typeof document !== "undefined" && typeof window !== "undefined";
}

function readStoredState(): SidebarStoreState | null {
  if (!hasDocument()) return null;
  const raw = window.localStorage.getItem(STORAGE_KEY);
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw) as Partial<StoredSidebarState>;
    if (typeof parsed.collapsed !== "boolean" || !Array.isArray(parsed.expandedGroups)) return null;
    return { collapsed: parsed.collapsed, expandedGroups: new Set(parsed.expandedGroups) };
  } catch {
    return null;
  }
}

// Module-level singleton, same shape as use-theme.ts's ThemeStore — every
// browser-global access is guarded so constructing it (at this module's own
// scope) never throws for a consumer of an unrelated hook.
class SidebarStore {
  private state: SidebarStoreState = readStoredState() ?? { collapsed: false, expandedGroups: new Set() };
  private readonly listeners = new Set<Listener>();

  getState = (): SidebarStoreState => {
    return this.state;
  };

  private commit(state: SidebarStoreState): void {
    this.state = state;
    if (hasDocument()) {
      const toStore: StoredSidebarState = { collapsed: state.collapsed, expandedGroups: [...state.expandedGroups] };
      window.localStorage.setItem(STORAGE_KEY, JSON.stringify(toStore));
    }
    for (const listener of this.listeners) listener(state);
  }

  toggleCollapsed(): void {
    this.commit({ ...this.state, collapsed: !this.state.collapsed });
  }

  toggleGroup(key: string): void {
    const expandedGroups = new Set(this.state.expandedGroups);
    if (expandedGroups.has(key)) expandedGroups.delete(key);
    else expandedGroups.add(key);
    this.commit({ ...this.state, expandedGroups });
  }

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
}

export const sidebarStore = new SidebarStore();

export interface UseSidebarResult {
  collapsed: boolean;
  expandedGroups: Set<string>;
  toggleCollapsed: () => void;
  toggleGroup: (key: string) => void;
}

export type SidebarStoreLike = Pick<SidebarStore, "getState" | "subscribe" | "toggleCollapsed" | "toggleGroup">;

// Matches use-theme.ts's useTheme(store) — useSyncExternalStore over a
// useState+useEffect pair avoids a stale-value gap between first paint and
// the subscribing effect.
export function useSidebar(store: SidebarStoreLike = sidebarStore): UseSidebarResult {
  const state = useSyncExternalStore(store.subscribe, store.getState);
  return {
    collapsed: state.collapsed,
    expandedGroups: state.expandedGroups,
    toggleCollapsed: () => store.toggleCollapsed(),
    toggleGroup: (key) => store.toggleGroup(key),
  };
}
