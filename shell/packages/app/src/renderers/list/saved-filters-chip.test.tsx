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
      rename: vi.fn(),
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
    rename: vi.fn(),
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
      rename: vi.fn(),
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
      rename: vi.fn(),
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
      rename: vi.fn(),
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
      rename: vi.fn(),
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
      rename: vi.fn(),
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
      rename: vi.fn(),
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
      rename: vi.fn(),
    });
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
    fireEvent.click(screen.getByRole("button", { name: "Delete 'Archived'" }));

    expect(remove).toHaveBeenCalledWith("f1");
  });

  it("saving the current filter opens the AlertDialog, calls save with the entered name, and toasts on success", async () => {
    const save = vi.fn(async () => {});
    const toastSuccess = vi.spyOn(toast, "success").mockImplementation(() => {});
    useSavedFiltersMock.mockReturnValue({
      filters: [],
      isLoading: false,
      save,
      remove: vi.fn(),
      setDefault: vi.fn(),
      rename: vi.fn(),
    });
    render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
    fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
    const panel = screen.getByRole("dialog", { name: "Saved filters" });
    fireEvent.click(screen.getByRole("button", { name: "Save current filter" }));

    expect(screen.getByRole("alertdialog")).toBeTruthy();
    fireEvent.mouseDown(screen.getByLabelText("Name"));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "My New Filter" } });
    fireEvent.mouseDown(screen.getByRole("button", { name: "Save" }));
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(save).toHaveBeenCalledWith("My New Filter"));
    await waitFor(() => expect(toastSuccess).toHaveBeenCalledWith("Filter saved"));
    expect(document.body.contains(panel)).toBe(true);
    toastSuccess.mockRestore();
  });

  it("keeps the save dialog open and does not toast success when the save fails", async () => {
    const save = vi.fn(async () => {
      throw new Error("boom");
    });
    const toastSuccess = vi.spyOn(toast, "success").mockImplementation(() => {});
    useSavedFiltersMock.mockReturnValue({
      filters: [],
      isLoading: false,
      save,
      remove: vi.fn(),
      setDefault: vi.fn(),
      rename: vi.fn(),
    });
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

  describe("rename", () => {
    function pendingRename() {
      let resolve!: () => void;
      const promise = new Promise<void>((done) => {
        resolve = done;
      });
      return { promise, resolve };
    }

    const archived = {
      id: "f1",
      viewName: "contacts_list",
      label: "Archived",
      queryString: "?filter[is_active]=false",
      isDefault: false,
    };

    function renderWithRename(rename: UseSavedFiltersResult["rename"]) {
      useSavedFiltersMock.mockReturnValue({
        filters: [archived],
        isLoading: false,
        save: vi.fn(),
        remove: vi.fn(),
        setDefault: vi.fn(),
        rename,
      });
      render(<SavedFiltersChip viewName="contacts_list" listState={fakeListState()} />);
      fireEvent.click(screen.getByRole("button", { name: "Saved filters" }));
      fireEvent.click(screen.getByRole("button", { name: "Rename 'Archived'" }));
      return screen.getByRole("textbox", { name: "Rename 'Archived'" }) as HTMLInputElement;
    }

    it("orders a row's tab stops apply, rename, set default, delete", () => {
      renderWithRename(vi.fn());
      fireEvent.keyDown(screen.getByRole("textbox"), { key: "Escape" });
      const names = Array.from(
        screen.getByRole("dialog").querySelectorAll("button"),
        (b) => b.getAttribute("aria-label") ?? b.textContent,
      );
      expect(names.slice(0, 4)).toEqual([
        "Archived",
        "Rename 'Archived'",
        "Set 'Archived' as default",
        "Delete 'Archived'",
      ]);
    });

    it("opens a focused input holding the current label", () => {
      const input = renderWithRename(vi.fn());
      expect(input.value).toBe("Archived");
      expect(document.activeElement).toBe(input);
    });

    it("Enter saves the new label and returns focus to the Rename button", async () => {
      const rename = vi.fn(async () => {});
      const input = renderWithRename(rename);
      fireEvent.change(input, { target: { value: "Old stuff" } });
      fireEvent.keyDown(input, { key: "Enter" });

      await waitFor(() => expect(screen.queryByRole("textbox")).toBeNull());
      expect(rename).toHaveBeenCalledExactlyOnceWith("f1", "Old stuff");
      expect(document.activeElement).toBe(screen.getByRole("button", { name: "Rename 'Archived'" }));
    });

    it("submits only once while a rename is pending", async () => {
      const { promise, resolve } = pendingRename();
      const rename = vi.fn(() => promise);
      const input = renderWithRename(rename);
      fireEvent.change(input, { target: { value: "Old stuff" } });
      fireEvent.keyDown(input, { key: "Enter" });
      fireEvent.keyDown(input, { key: "Enter" });
      fireEvent.blur(input);

      expect(rename).toHaveBeenCalledExactlyOnceWith("f1", "Old stuff");
      expect(input.readOnly).toBe(true);
      resolve();
      await waitFor(() => expect(screen.queryByRole("textbox")).toBeNull());
    });

    it("does not submit Enter used to compose text", () => {
      const rename = vi.fn(async () => {});
      const input = renderWithRename(rename);
      fireEvent.change(input, { target: { value: "Old stuff" } });
      fireEvent.keyDown(input, { key: "Enter", isComposing: true });

      expect(rename).not.toHaveBeenCalled();
      expect(screen.getByRole("textbox")).toBe(input);
    });

    it("does not steal focus when a pending Enter save finishes after focus leaves the input", async () => {
      const { promise, resolve } = pendingRename();
      const input = renderWithRename(vi.fn(() => promise));
      fireEvent.change(input, { target: { value: "Old stuff" } });
      fireEvent.keyDown(input, { key: "Enter" });
      const saveButton = screen.getByRole("button", { name: "Save current filter" });
      saveButton.focus();
      resolve();

      await waitFor(() => expect(screen.queryByRole("textbox")).toBeNull());
      expect(document.activeElement).toBe(saveButton);
    });

    it("cancels a pending editor without a late completion stealing focus", async () => {
      const { promise, resolve } = pendingRename();
      const input = renderWithRename(vi.fn(() => promise));
      fireEvent.change(input, { target: { value: "Old stuff" } });
      fireEvent.keyDown(input, { key: "Enter" });
      fireEvent.keyDown(input, { key: "Escape" });
      const renameButton = screen.getByRole("button", { name: "Rename 'Archived'" });
      expect(document.activeElement).toBe(renameButton);
      expect(renameButton.getAttribute("aria-disabled")).toBe("true");
      fireEvent.click(renameButton);
      expect(screen.queryByRole("textbox")).toBeNull();
      const saveButton = screen.getByRole("button", { name: "Save current filter" });
      saveButton.focus();
      resolve();

      await waitFor(() => expect(renameButton.getAttribute("aria-disabled")).toBeNull());
      expect(document.activeElement).toBe(saveButton);
      fireEvent.click(renameButton);
      expect(screen.getByRole("textbox")).toBeTruthy();
    });

    it("Escape cancels without saving or closing the panel", () => {
      const rename = vi.fn(async () => {});
      const input = renderWithRename(rename);
      fireEvent.change(input, { target: { value: "Old stuff" } });
      fireEvent.keyDown(input, { key: "Escape" });

      expect(rename).not.toHaveBeenCalled();
      expect(screen.queryByRole("textbox")).toBeNull();
      expect(screen.getByRole("dialog", { name: "Saved filters" })).toBeTruthy();
      expect(document.activeElement).toBe(screen.getByRole("button", { name: "Rename 'Archived'" }));
    });

    it("blurring with a changed label saves it", async () => {
      const rename = vi.fn(async () => {});
      const input = renderWithRename(rename);
      fireEvent.change(input, { target: { value: "Old stuff" } });
      fireEvent.blur(input);

      await waitFor(() => expect(screen.queryByRole("textbox")).toBeNull());
      expect(rename).toHaveBeenCalledExactlyOnceWith("f1", "Old stuff");
    });

    it("blurring without a change cancels", () => {
      const rename = vi.fn(async () => {});
      const input = renderWithRename(rename);
      fireEvent.blur(input);

      expect(rename).not.toHaveBeenCalled();
      expect(screen.queryByRole("textbox")).toBeNull();
    });

    it("keeps the input open with an error for an empty label", () => {
      const rename = vi.fn(async () => {});
      const input = renderWithRename(rename);
      fireEvent.change(input, { target: { value: "  " } });
      fireEvent.keyDown(input, { key: "Enter" });

      expect(rename).not.toHaveBeenCalled();
      expect(screen.getByRole("alert").textContent).toBe("Enter a name.");
      expect(input.getAttribute("aria-invalid")).toBe("true");
    });

    it("keeps the input open with the error under it when the save fails", async () => {
      const rename = vi.fn(async () => {
        throw new Error("boom");
      });
      const input = renderWithRename(rename);
      fireEvent.change(input, { target: { value: "Old stuff" } });
      fireEvent.keyDown(input, { key: "Enter" });

      await waitFor(() => expect(screen.getByRole("alert").textContent).toBe("boom"));
      expect(screen.getByRole("textbox")).toBe(input);
    });
  });
});
