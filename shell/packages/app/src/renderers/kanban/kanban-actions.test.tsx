import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ListAction } from "../list/list-view-types.js";
import { resolveKanbanActionItems, useKanbanRouteAction } from "./kanban-actions.js";

const { dispatchMock, actionRegistryResolveMock, resolveViewPathMock } = vi.hoisted(() => ({
  dispatchMock: vi.fn(),
  actionRegistryResolveMock: vi.fn(),
  resolveViewPathMock: vi.fn(),
}));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, dispatch: dispatchMock, actionRegistry: { resolve: actionRegistryResolveMock } };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, viewPathRegistry: { resolve: resolveViewPathMock } };
});

afterEach(() => {
  vi.restoreAllMocks();
  dispatchMock.mockReset();
  actionRegistryResolveMock.mockReset();
  resolveViewPathMock.mockReset();
});

function wrapper() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

describe("useKanbanRouteAction", () => {
  it("resolves the named route and dispatches through the same registry/dispatch mechanics useAction() uses", async () => {
    actionRegistryResolveMock.mockResolvedValue({ method: "POST", path: "/crm/leads/{id}/mark-won" });
    dispatchMock.mockResolvedValue({ ok: true });

    const { result } = renderHook(() => useKanbanRouteAction(), { wrapper: wrapper() });
    result.current.mutate({ route: "crm.markWon", variables: "lead-1" });

    await waitFor(() => expect(dispatchMock).toHaveBeenCalled());
    expect(actionRegistryResolveMock).toHaveBeenCalledWith("crm.markWon");
  });
});

describe("resolveKanbanActionItems", () => {
  const navigate = vi.fn();

  it("resolves a route action into an item that mutates with the record id as the sole variable", () => {
    const routeAction = { mutate: vi.fn() } as unknown as ReturnType<typeof useKanbanRouteAction>;
    const actions: ListAction[] = [{ label: "Mark Won", type: "route", route: "crm.markWon" }];

    const items = resolveKanbanActionItems(actions, "crm", navigate, routeAction, "lead-1");
    expect(items).toHaveLength(1);
    items[0]?.onClick?.();
    expect(routeAction.mutate).toHaveBeenCalledWith({ route: "crm.markWon", variables: "lead-1" });
  });

  it("resolves a create action into an item that navigates to the resolved view path", async () => {
    resolveViewPathMock.mockResolvedValue("/leads/new");
    const routeAction = { mutate: vi.fn() } as unknown as ReturnType<typeof useKanbanRouteAction>;
    const actions: ListAction[] = [{ label: "New Lead", type: "create", view: "leads_form" }];

    const items = resolveKanbanActionItems(actions, "crm", navigate, routeAction);
    items[0]?.onClick?.();

    await waitFor(() => expect(navigate).toHaveBeenCalled());
  });

  it("resolves a url action into an item that opens the url", () => {
    const openSpy = vi.spyOn(window, "open").mockImplementation(() => null);
    const routeAction = { mutate: vi.fn() } as unknown as ReturnType<typeof useKanbanRouteAction>;
    const actions: ListAction[] = [{ label: "Docs", type: "url", url: "https://example.com" }];

    const items = resolveKanbanActionItems(actions, "crm", navigate, routeAction);
    items[0]?.onClick?.();

    expect(openSpy).toHaveBeenCalledWith("https://example.com", "_blank", "noopener,noreferrer");
  });

  it("drops action types outside list-actions.tsx's own supported depth", () => {
    const routeAction = { mutate: vi.fn() } as unknown as ReturnType<typeof useKanbanRouteAction>;
    const actions: ListAction[] = [
      { label: "Export", type: "export" },
      { label: "Custom", type: "custom" },
    ];

    expect(resolveKanbanActionItems(actions, "crm", navigate, routeAction)).toEqual([]);
  });
});
