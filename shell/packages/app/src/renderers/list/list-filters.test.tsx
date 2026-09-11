import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { booleanFilterState, ListFilters } from "./list-filters.js";
import type { ListFilter } from "./list-view-types.js";

// RelationFilterInput's label resolution uses react-query — every render
// needs a provider, not just the relation/tags/user_select-specific ones.
const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
function Providers({ children }: { children: ReactNode }) {
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

const { resolveMetadataMock, getMock } = vi.hoisted(() => ({
  resolveMetadataMock: vi.fn(async () => ({
    listRoute: "GET /contacts",
    labelField: "display_name",
    searchParam: "q",
  })),
  getMock: vi.fn(async () => ({
    data: [
      { id: "1", display_name: "Acme Corp" },
      { id: "2", display_name: "Beta Inc" },
    ],
    meta: { cursor: null, hasMore: false },
  })),
}));
vi.mock("@goerp/sdk", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk")>();
  return { ...actual, apiClient: { ...actual.apiClient, get: getMock } };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, resourceMetadataRegistry: { resolve: resolveMetadataMock } };
});

afterEach(() => {
  cleanup();
  resolveMetadataMock.mockClear();
  getMock.mockClear();
});

describe("booleanFilterState", () => {
  it("maps true/'true' to 'true', false/'false' to 'false', and anything else to 'any'", () => {
    expect(booleanFilterState(true)).toBe("true");
    expect(booleanFilterState("true")).toBe("true");
    expect(booleanFilterState(false)).toBe("false");
    expect(booleanFilterState("false")).toBe("false");
    expect(booleanFilterState(undefined)).toBe("any");
    expect(booleanFilterState("other")).toBe("any");
  });
});

describe("ListFilters", () => {
  it("renders nothing when the view declares no filters", () => {
    const { container } = render(
      <Providers>
        <ListFilters filters={[]} values={{}} onChange={vi.fn()} />
      </Providers>,
    );
    expect(container.textContent).toBe("");
  });

  it("renders every declared filter, not just boolean ones", () => {
    const filters: ListFilter[] = [
      { field: "type", label: "Type", type: "select", options: [{ value: "person", label: "Person" }] },
      { field: "is_active", label: "Active", type: "boolean" },
    ];
    render(
      <Providers>
        <ListFilters filters={filters} values={{}} onChange={vi.fn()} />
      </Providers>,
    );

    expect(screen.getByText("Type")).toBeTruthy();
    expect(screen.getByText("Active")).toBeTruthy();
  });

  it("boolean: renders a select per boolean filter, initialized from its current value", () => {
    const filters: ListFilter[] = [{ field: "is_active", label: "Active", type: "boolean" }];
    render(
      <Providers>
        <ListFilters filters={filters} values={{ is_active: true }} onChange={vi.fn()} />
      </Providers>,
    );

    expect(screen.getByText("Active")).toBeTruthy();
    expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe("true");
  });

  it("boolean: calls onChange with true/false/undefined when the select changes", () => {
    const filters: ListFilter[] = [{ field: "is_active", label: "Active", type: "boolean" }];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{}} onChange={onChange} />
      </Providers>,
    );

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "true" } });
    expect(onChange).toHaveBeenCalledWith("is_active", true);

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "false" } });
    expect(onChange).toHaveBeenCalledWith("is_active", false);

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "any" } });
    expect(onChange).toHaveBeenCalledWith("is_active", undefined);
  });

  it("text: wraps a non-empty value in {like}, and clears to undefined on an empty value", () => {
    const filters: ListFilter[] = [{ field: "name", label: "Name", type: "text" }];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{ name: { like: "acme" } }} onChange={onChange} />
      </Providers>,
    );

    const input = screen.getByDisplayValue("acme");
    fireEvent.change(input, { target: { value: "beta" } });
    expect(onChange).toHaveBeenCalledWith("name", { like: "beta" });

    fireEvent.change(input, { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith("name", undefined);
  });

  it("select: single-value by default, sends the raw option value", () => {
    const filters: ListFilter[] = [
      { field: "type", label: "Type", type: "select", options: [{ value: "person", label: "Person" }] },
    ];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{}} onChange={onChange} />
      </Providers>,
    );

    fireEvent.click(screen.getByRole("combobox"));
    fireEvent.click(screen.getByRole("option", { name: "Person" }));
    expect(onChange).toHaveBeenCalledWith("type", "person");
  });

  it("select with multiple:true and multi_select both render a multi-value picker", () => {
    const filters: ListFilter[] = [
      { field: "type", label: "Type", type: "select", multiple: true, options: [{ value: "person", label: "Person" }] },
      { field: "tags", label: "Tags", type: "multi_select", options: [{ value: "vip", label: "VIP" }] },
    ];
    render(
      <Providers>
        <ListFilters filters={filters} values={{}} onChange={vi.fn()} />
      </Providers>,
    );
    expect(screen.getAllByRole("combobox")).toHaveLength(2);
  });

  it("radio: includes an Any option and reports the selected option's value", () => {
    const filters: ListFilter[] = [
      {
        field: "state",
        label: "State",
        type: "radio",
        options: [
          { value: "draft", label: "Draft" },
          { value: "done", label: "Done" },
        ],
      },
    ];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{ state: "draft" }} onChange={onChange} />
      </Providers>,
    );

    expect((screen.getByRole("radio", { name: "Draft" }) as HTMLInputElement).checked).toBe(true);
    fireEvent.click(screen.getByRole("radio", { name: "Done" }));
    expect(onChange).toHaveBeenCalledWith("state", "done");
    fireEvent.click(screen.getByRole("radio", { name: "Any" }));
    expect(onChange).toHaveBeenCalledWith("state", undefined);
  });

  it("date: sends an ISO date string and clears to undefined", () => {
    const filters: ListFilter[] = [{ field: "due_date", label: "Due", type: "date" }];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{ due_date: "2026-01-15" }} onChange={onChange} />
      </Providers>,
    );

    const input = screen.getByDisplayValue("2026-01-15");
    fireEvent.change(input, { target: { value: "2026-02-01" } });
    expect(onChange).toHaveBeenCalledWith("due_date", "2026-02-01");

    fireEvent.change(input, { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith("due_date", undefined);
  });

  it("daterange: reports {gte,lte}, keeping the other bound when only one changes", () => {
    const filters: ListFilter[] = [{ field: "created_at", label: "Created", type: "daterange" }];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{ created_at: { gte: "2026-01-01" } }} onChange={onChange} />
      </Providers>,
    );

    fireEvent.change(screen.getByLabelText("Created to"), { target: { value: "2026-02-01" } });
    expect(onChange).toHaveBeenCalledWith("created_at", { gte: "2026-01-01", lte: "2026-02-01" });
  });

  it("daterange: clearing both bounds reports undefined", () => {
    const filters: ListFilter[] = [{ field: "created_at", label: "Created", type: "daterange" }];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{ created_at: { gte: "2026-01-01" } }} onChange={onChange} />
      </Providers>,
    );

    fireEvent.change(screen.getByLabelText("Created from"), { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith("created_at", undefined);
  });

  it("number: parses to a numeric FilterValue", () => {
    const filters: ListFilter[] = [{ field: "amount", label: "Amount", type: "number" }];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{}} onChange={onChange} />
      </Providers>,
    );

    fireEvent.change(screen.getByRole("spinbutton"), { target: { value: "42" } });
    expect(onChange).toHaveBeenCalledWith("amount", 42);
  });

  it("number: clearing an existing value reports undefined", () => {
    const filters: ListFilter[] = [{ field: "amount", label: "Amount", type: "number" }];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{ amount: 42 }} onChange={onChange} />
      </Providers>,
    );

    fireEvent.change(screen.getByRole("spinbutton"), { target: { value: "" } });
    expect(onChange).toHaveBeenCalledWith("amount", undefined);
  });

  it("number_range: reports {gte,lte} string bounds", () => {
    const filters: ListFilter[] = [{ field: "amount", label: "Amount", type: "number_range" }];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{}} onChange={onChange} />
      </Providers>,
    );

    fireEvent.change(screen.getByLabelText("Amount min"), { target: { value: "10" } });
    expect(onChange).toHaveBeenCalledWith("amount", { gte: "10" });
  });

  it("country_select: reports the ISO code", async () => {
    const filters: ListFilter[] = [{ field: "country", label: "Country", type: "country_select" }];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{}} onChange={onChange} />
      </Providers>,
    );

    fireEvent.focus(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByRole("option", { name: /United States/ }));
    expect(onChange).toHaveBeenCalledWith("country", "US");
  });

  it("relation: single-select by default, resolving the id's display label via the registry", async () => {
    const filters: ListFilter[] = [
      { field: "customer_id", label: "Customer", type: "relation", resource: "sales.customer" },
    ];
    render(
      <Providers>
        <ListFilters filters={filters} values={{ customer_id: "1" }} onChange={vi.fn()} />
      </Providers>,
    );

    expect((await screen.findByRole("combobox")) as HTMLInputElement).toBeTruthy();
    await waitFor(() => expect((screen.getByRole("combobox") as HTMLInputElement).value).toBe("Acme Corp"));
  });

  it("relation: writing back sends a bare id string, not the RelationValue object", async () => {
    const filters: ListFilter[] = [
      { field: "customer_id", label: "Customer", type: "relation", resource: "sales.customer" },
    ];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{}} onChange={onChange} />
      </Providers>,
    );

    fireEvent.focus(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByRole("option", { name: "Acme Corp" }));
    expect(onChange).toHaveBeenCalledWith("customer_id", "1");
  });

  it("relation with no resource: renders nothing", () => {
    const filters: ListFilter[] = [{ field: "customer_id", label: "Customer", type: "relation" }];
    const { container } = render(
      <Providers>
        <ListFilters filters={filters} values={{}} onChange={vi.fn()} />
      </Providers>,
    );
    expect(container.textContent).toBe("");
  });

  it("tags: always multi-value regardless of the declared `multiple` field, sending an id array", async () => {
    const filters: ListFilter[] = [{ field: "tag_ids", label: "Tags", type: "tags", resource: "contacts.tag" }];
    const onChange = vi.fn();
    render(
      <Providers>
        <ListFilters filters={filters} values={{}} onChange={onChange} />
      </Providers>,
    );

    fireEvent.focus(screen.getByRole("combobox"));
    fireEvent.click(await screen.findByRole("option", { name: "Acme Corp" }));
    expect(onChange).toHaveBeenCalledWith("tag_ids", ["1"]);
  });

  it("user_select: reuses the same relation-picker wiring as relation", async () => {
    const filters: ListFilter[] = [{ field: "owner_id", label: "Owner", type: "user_select", resource: "auth.user" }];
    render(
      <Providers>
        <ListFilters filters={filters} values={{}} onChange={vi.fn()} />
      </Providers>,
    );
    expect(await screen.findByRole("combobox")).toBeTruthy();
  });
});
