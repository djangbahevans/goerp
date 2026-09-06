import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { booleanFilterState, ListFilters } from "./list-filters.js";
import type { ListFilter } from "./list-view-types.js";

afterEach(cleanup);

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
  it("renders nothing when the view declares no boolean filters", () => {
    const filters: ListFilter[] = [{ field: "type", label: "Type", type: "select" }];
    const { container } = render(<ListFilters filters={filters} values={{}} onChange={vi.fn()} />);
    expect(container.textContent).toBe("");
  });

  it("renders a select per boolean filter, initialized from its current value", () => {
    const filters: ListFilter[] = [{ field: "is_active", label: "Active", type: "boolean" }];
    render(<ListFilters filters={filters} values={{ is_active: true }} onChange={vi.fn()} />);

    expect(screen.getByText("Active")).toBeTruthy();
    expect((screen.getByRole("combobox") as HTMLSelectElement).value).toBe("true");
  });

  it("calls onChange with true/false/undefined when the select changes", () => {
    const filters: ListFilter[] = [{ field: "is_active", label: "Active", type: "boolean" }];
    const onChange = vi.fn();
    render(<ListFilters filters={filters} values={{}} onChange={onChange} />);

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "true" } });
    expect(onChange).toHaveBeenCalledWith("is_active", true);

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "false" } });
    expect(onChange).toHaveBeenCalledWith("is_active", false);

    fireEvent.change(screen.getByRole("combobox"), { target: { value: "any" } });
    expect(onChange).toHaveBeenCalledWith("is_active", undefined);
  });

  it("ignores non-boolean filters even when mixed with boolean ones", () => {
    const filters: ListFilter[] = [
      { field: "type", label: "Type", type: "select" },
      { field: "is_active", label: "Active", type: "boolean" },
    ];
    render(<ListFilters filters={filters} values={{}} onChange={vi.fn()} />);

    expect(screen.queryByText("Type")).toBeNull();
    expect(screen.getByText("Active")).toBeTruthy();
  });
});
