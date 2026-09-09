import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { sidebarStore, useSidebar } from "./sidebar-store.js";

// A fresh fake store per test — the real module-level sidebarStore is a
// singleton, so testing against it directly would leak state across tests.
function fakeStore(initial = { collapsed: false, expandedGroups: new Set<string>() }) {
  let state = initial;
  const listeners = new Set<(s: typeof initial) => void>();
  return {
    getState: () => state,
    toggleCollapsed: () => {
      state = { ...state, collapsed: !state.collapsed };
      for (const l of listeners) l(state);
    },
    toggleGroup: (key: string) => {
      const expandedGroups = new Set(state.expandedGroups);
      if (expandedGroups.has(key)) expandedGroups.delete(key);
      else expandedGroups.add(key);
      state = { ...state, expandedGroups };
      for (const l of listeners) l(state);
    },
    subscribe: (listener: (s: typeof initial) => void) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
  };
}

afterEach(cleanup);

describe("useSidebar", () => {
  it("returns the store's current state", () => {
    const store = fakeStore({ collapsed: true, expandedGroups: new Set(["sales"]) });
    const { result } = renderHook(() => useSidebar(store));
    expect(result.current.collapsed).toBe(true);
    expect(result.current.expandedGroups.has("sales")).toBe(true);
  });

  it("toggleCollapsed updates every hook instance subscribed to the same store", () => {
    const store = fakeStore();
    const a = renderHook(() => useSidebar(store));
    const b = renderHook(() => useSidebar(store));

    act(() => a.result.current.toggleCollapsed());

    expect(a.result.current.collapsed).toBe(true);
    expect(b.result.current.collapsed).toBe(true);
  });

  it("toggleGroup adds an unexpanded group and removes an already-expanded one", () => {
    const store = fakeStore();
    const { result } = renderHook(() => useSidebar(store));

    act(() => result.current.toggleGroup("sales"));
    expect(result.current.expandedGroups.has("sales")).toBe(true);

    act(() => result.current.toggleGroup("sales"));
    expect(result.current.expandedGroups.has("sales")).toBe(false);
  });
});

describe("sidebarStore (real singleton)", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("persists collapsed and expandedGroups to localStorage", () => {
    sidebarStore.toggleCollapsed();
    sidebarStore.toggleGroup("sales");

    const stored = JSON.parse(window.localStorage.getItem("goerp-sidebar") ?? "{}");
    expect(stored.collapsed).toBe(true);
    expect(stored.expandedGroups).toEqual(["sales"]);

    // Reset the singleton's observable state for other tests in this file.
    sidebarStore.toggleCollapsed();
    sidebarStore.toggleGroup("sales");
  });
});
