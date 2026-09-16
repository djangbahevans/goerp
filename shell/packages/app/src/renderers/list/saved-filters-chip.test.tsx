import { toast } from "@goerp/sdk/notifications";
import type { UseSavedFiltersResult } from "@goerp/sdk/react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { SavedFiltersChip } from "./saved-filters-chip.js";
import type { ListStateHandle } from "./use-list-state.js";

const { useSavedFiltersMock } = vi.hoisted(() => ({
  useSavedFiltersMock: vi.fn(
    (): UseSavedFiltersResult => ({
      filters: [],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    }),
  ),
}));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useSavedFilters: useSavedFiltersMock };
});

function fakeListState(overrides: Partial<ListStateHandle> = {}): ListStateHandle {
  return {
    filter: {},
    sort: undefined,
    groupBy: undefined,
    setFilter: vi.fn(),
    setFilters: vi.fn(),
    setSort: vi.fn(),
    setGroupBy: vi.fn(),
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
  useSavedFiltersMock.mockReset();
  useSavedFiltersMock.mockImplementation(() => ({
    filters: [],
    isLoading: false,
    save: vi.fn(),
    remove: vi.fn(),
    setDefault: vi.fn(),
  }));
});

describe("SavedFiltersChip", () => {
  it("is closed until the trigger is clicked", () => {
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    expect(screen.queryByRole("dialog")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
    expect(screen.getByRole("dialog", { name: "Saved filters" })).toBeTruthy();
  });

  it("shows a loading skeleton while the fetch is pending", () => {
    useSavedFiltersMock.mockReturnValue({
      filters: [],
      isLoading: true,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
    expect(screen.queryByText("No saved filters yet.")).toBeNull();
  });

  it("shows an empty state when there are no saved filters", () => {
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
    expect(screen.getByText("No saved filters yet.")).toBeTruthy();
  });

  it("lists each saved filter and omits the set-default control on the already-default row", () => {
    useSavedFiltersMock.mockReturnValue({
      filters: [
        {
          id: "f1",
          viewName: "contacts_list",
          label: "My Open Orders",
          queryString: "?filter[is_active]=true",
          isDefault: true,
        },
        {
          id: "f2",
          viewName: "contacts_list",
          label: "Archived",
          queryString: "?filter[is_active]=false",
          isDefault: false,
        },
      ],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));

    expect(screen.getByRole("button", { name: "My Open Orders" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Archived" })).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Set 'My Open Orders' as default" })).toBeNull();
    expect(screen.getByRole("button", { name: "Set 'Archived' as default" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Delete 'My Open Orders'" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Delete 'Archived'" })).toBeTruthy();
  });

  it("applying a filter fully replaces filter/sort/group-by, clearing anything not in the target", () => {
    useSavedFiltersMock.mockReturnValue({
      filters: [
        {
          id: "f1",
          viewName: "contacts_list",
          label: "My Open Orders",
          queryString: "?filter[is_active]=false&sort=-name&group_by=state",
          isDefault: false,
        },
      ],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });
    const listState = fakeListState({ filter: { type: "person" }, sort: "-created_at", groupBy: "region" });
    render(<SavedFiltersChip viewName="contacts_list" listState={listState} />);
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
    fireEvent.click(screen.getByRole("button", { name: "My Open Orders" }));

    expect(listState.setFilters).toHaveBeenCalledWith({ is_active: false, type: undefined });
    expect(listState.setSort).toHaveBeenCalledWith("-name");
    expect(listState.setGroupBy).toHaveBeenCalledWith("state");
  });

  it("applying a filter with no sort/group_by still clears any currently-set sort/group-by", () => {
    useSavedFiltersMock.mockReturnValue({
      filters: [
        {
          id: "f1",
          viewName: "contacts_list",
          label: "Just a filter",
          queryString: "?filter[is_active]=true",
          isDefault: false,
        },
      ],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });
    const listState = fakeListState({ sort: "-created_at", groupBy: "region" });
    render(<SavedFiltersChip viewName="contacts_list" listState={listState} />);
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
    fireEvent.click(screen.getByRole("button", { name: "Just a filter" }));

    expect(listState.setSort).toHaveBeenCalledWith(undefined);
    expect(listState.setGroupBy).toHaveBeenCalledWith(undefined);
  });

  it("closes the panel and returns focus to the trigger after applying", () => {
    useSavedFiltersMock.mockReturnValue({
      filters: [
        {
          id: "f1",
          viewName: "contacts_list",
          label: "My Filter",
          queryString: "?filter[is_active]=true",
          isDefault: false,
        },
      ],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault: vi.fn(),
    });
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    const trigger = screen.getByRole("button", { name: "Saved filters" });
    fireEvent.click(trigger);
    fireEvent.click(screen.getByRole("button", { name: "My Filter" }));

    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("Escape closes the panel and returns focus to the trigger", () => {
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    const trigger = screen.getByRole("button", { name: "Saved filters" });
    fireEvent.click(trigger);
    const dialog = screen.getByRole("dialog", { name: "Saved filters" });
    fireEvent.keyDown(dialog, { key: "Escape" });

    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("calls setDefault with the filter's id", () => {
    const setDefault = vi.fn(async () => {});
    useSavedFiltersMock.mockReturnValue({
      filters: [
        {
          id: "f1",
          viewName: "contacts_list",
          label: "Archived",
          queryString: "?filter[is_active]=false",
          isDefault: false,
        },
      ],
      isLoading: false,
      save: vi.fn(),
      remove: vi.fn(),
      setDefault,
    });
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
    fireEvent.click(screen.getByRole("button", { name: "Set 'Archived' as default" }));

    expect(setDefault).toHaveBeenCalledWith("f1");
  });

  it("calls remove with the filter's id", () => {
    const remove = vi.fn(async () => {});
    useSavedFiltersMock.mockReturnValue({
      filters: [
        {
          id: "f1",
          viewName: "contacts_list",
          label: "Archived",
          queryString: "?filter[is_active]=false",
          isDefault: false,
        },
      ],
      isLoading: false,
      save: vi.fn(),
      remove,
      setDefault: vi.fn(),
    });
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete 'Archived'" }));

    expect(remove).toHaveBeenCalledWith("f1");
  });

  it("saving the current filter opens the AlertDialog, calls save with the entered name, and toasts on success", async () => {
    const save = vi.fn(async () => {});
    const toastSuccess = vi.spyOn(toast, "success").mockImplementation(() => {});
    useSavedFiltersMock.mockReturnValue({ filters: [], isLoading: false, save, remove: vi.fn(), setDefault: vi.fn() });
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
    fireEvent.click(screen.getByRole("button", { name: "Save current filter" }));

    expect(screen.getByRole("alertdialog")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "My New Filter" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(save).toHaveBeenCalledWith("My New Filter"));
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Filter saved"));
    toastSuccess.mockRestore();
  });

  it("keeps the save dialog open and does not toast success when the save fails", async () => {
    const save = vi.fn(async () => {
      throw new Error("boom");
    });
    const toastSuccess = vi.spyOn(toast, "success").mockImplementation(() => {});
    useSavedFiltersMock.mockReturnValue({ filters: [], isLoading: false, save, remove: vi.fn(), setDefault: vi.fn() });
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
    fireEvent.click(screen.getByRole("button", { name: "Save current filter" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "My New Filter" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(save).toHaveBeenCalled());
    expect(toastSuccess).not.toHaveBeenCalled();
    expect(screen.getByRole("alertdialog")).toBeTruthy();
    toastSuccess.mockRestore();
  });
});
