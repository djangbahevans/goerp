import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from "@tanstack/react-router";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Row } from "../list/list-view-types.js";
import { FormSectionRenderer, sectionLayoutColumns, sectionListColumns } from "./form-sections.js";
import type { FormSection } from "./form-view-types.js";

const permissions = createPermissionContextValue({
  permissions: new Set(),
  fieldAccess: {
    "contacts.contact": { email: { read: true, write: true } },
    "contacts.address": { city: { read: true, write: true } },
  },
  modulesEnabled: new Set(),
});

const { resolveModelMock, useInfiniteListMock } = vi.hoisted(() => ({
  resolveModelMock: vi.fn(),
  useInfiniteListMock: vi.fn(),
}));
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, modelRegistry: { resolve: resolveModelMock } };
});
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useInfiniteList: useInfiniteListMock, useRelationLabels: vi.fn(() => new Map()) };
});

afterEach(() => {
  cleanup();
  resolveModelMock.mockReset();
  useInfiniteListMock.mockReset();
});

describe("sectionLayoutColumns/sectionListColumns", () => {
  it("narrows the shared `columns` wire key by section type", () => {
    expect(sectionLayoutColumns({ type: "fields", columns: 3 })).toBe(3);
    expect(sectionLayoutColumns({ type: "fields" })).toBe(2);
    expect(sectionListColumns({ type: "sub_list", columns: [{ field: "name" }] })).toEqual([{ field: "name" }]);
    expect(sectionListColumns({ type: "fields", columns: 3 })).toEqual([]);
  });
});

async function renderSection(section: FormSection, record: Row = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rootRoute = createRootRoute();
  const indexRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: "/",
    component: () => (
      <QueryClientProvider client={client}>
        <PermissionContext.Provider value={permissions}>
          <FormSectionRenderer
            section={section}
            resource="contacts.contact"
            module="contacts"
            record={record}
            recordId="01j"
            onChange={vi.fn()}
            formReadonly={false}
          />
        </PermissionContext.Provider>
      </QueryClientProvider>
    ),
  });
  const router = createRouter({
    routeTree: rootRoute.addChildren([indexRoute]),
    history: createMemoryHistory({ initialEntries: ["/"] }),
  });
  await router.load();
  render(<RouterProvider router={router} />);
}

describe("FormSectionRenderer", () => {
  beforeEach(() => {
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [], meta: { cursor: null, hasMore: false } }] },
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    });
  });

  it('"fields": renders a labeled field per FormField, wrapped in a SectionCard heading', async () => {
    await renderSection(
      { type: "fields", label: "Contact Info", fields: [{ field: "email", type: "email" }] },
      { email: "a@b.com" },
    );
    expect(screen.getByRole("heading", { name: "Contact Info" })).toBeTruthy();
    expect(screen.getByDisplayValue("a@b.com")).toBeTruthy();
  });

  it('"header": renders the field grid directly, with no SectionCard wrapper', async () => {
    await renderSection(
      { type: "header", label: "Identity", fields: [{ field: "email", type: "email" }] },
      { email: "a@b.com" },
    );
    expect(screen.getByDisplayValue("a@b.com")).toBeTruthy();
    expect(screen.queryByRole("heading")).toBeNull();
  });

  it('"sub_list" with inline_key: renders the parent response\'s already-inline rows, no fetch', async () => {
    await renderSection(
      { type: "sub_list", inline_key: "addresses", columns: [{ field: "city", label: "City" }] },
      { addresses: [{ city: "Accra" }, { city: "Kumasi" }] },
    );
    expect(screen.getByText("Accra")).toBeTruthy();
    expect(screen.getByText("Kumasi")).toBeTruthy();
    expect(useInfiniteListMock).not.toHaveBeenCalled();
  });

  it('"sub_list" with inline_key, empty array: renders an EmptyState instead of a bare table', async () => {
    await renderSection(
      { type: "sub_list", label: "Addresses", inline_key: "addresses", columns: [{ field: "city" }] },
      { addresses: [] },
    );
    expect(screen.getByText("No Addresses yet")).toBeTruthy();
    expect(screen.queryByRole("table")).toBeNull();
  });

  it('"sub_list" without inline_key: resolves the One2Many target from the model registry and embeds a list', async () => {
    resolveModelMock.mockResolvedValue({
      name: "contact",
      label: "Contact",
      label_plural: "Contacts",
      shareable: false,
      enabled_ops: [],
      fields: [
        { name: "address_ids", type: "one2many", related_model: "contacts.address", inverse_field: "contact_id" },
      ],
    });
    useInfiniteListMock.mockReturnValue({
      data: { pages: [{ data: [{ id: "a1", city: "Accra" }], meta: { cursor: null, hasMore: false } }] },
      fetchNextPage: vi.fn(),
      hasNextPage: false,
      isFetchingNextPage: false,
      isLoading: false,
      isError: false,
      error: null,
      refetch: vi.fn(),
    });
    await renderSection({ type: "sub_list", field: "address_ids", columns: [{ field: "city" }] });
    expect(await screen.findByRole("table")).toBeTruthy();
    expect(useInfiniteListMock).toHaveBeenCalledWith(
      "contacts.address",
      expect.objectContaining({ filter: { contact_id: "01j" } }),
    );
  });

  it('"sub_list" without inline_key: surfaces an error for a field that isn\'t a resolvable one2many', async () => {
    resolveModelMock.mockResolvedValue({
      name: "contact",
      label: "Contact",
      label_plural: "Contacts",
      shareable: false,
      enabled_ops: [],
      fields: [],
    });
    await renderSection({ type: "sub_list", field: "not_a_field", columns: [] });
    expect(await screen.findByRole("alert")).toBeTruthy();
  });

  it('"custom": falls back to a message naming the unresolvable component', async () => {
    await renderSection({ type: "custom", component: "MySection" });
    expect(screen.getByText(/MySection/)).toBeTruthy();
  });
});
