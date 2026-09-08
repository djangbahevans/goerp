import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { useBulkAction } from "@goerp/sdk/react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { BulkActions } from "./bulk-actions.js";
import type { BulkAction } from "./list-view-types.js";

const { useActionMock, useExportMock, resolveComponentMock } = vi.hoisted(() => ({
  useActionMock: vi.fn(),
  useExportMock: vi.fn(),
  resolveComponentMock: vi.fn(),
}));

vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useAction: useActionMock, useExport: useExportMock };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, componentRegistry: { resolve: resolveComponentMock } };
});

beforeEach(() => {
  useActionMock.mockReturnValue({ mutate: vi.fn(), isPending: false, isError: false, error: null });
  useExportMock.mockReturnValue({ trigger: vi.fn(async () => {}), isPending: false, isError: false, error: null });
});

afterEach(() => {
  cleanup();
  useActionMock.mockReset();
  useExportMock.mockReset();
  resolveComponentMock.mockReset();
});

function permissionWrapper(permissions: string[]) {
  const value = createPermissionContextValue({
    permissions: new Set(permissions),
    fieldAccess: {},
    modulesEnabled: new Set(),
  });
  return function Wrapper({ children }: { children: ReactNode }) {
    return <PermissionContext.Provider value={value}>{children}</PermissionContext.Provider>;
  };
}

const fullAccess = permissionWrapper(["contacts:contact:write"]);

function renderBulkActions(
  actions: BulkAction[],
  selectedIds: string[],
  clearSelection = vi.fn(),
  Wrapper: ({ children }: { children: ReactNode }) => ReactNode = fullAccess,
) {
  return render(
    <Wrapper>
      <BulkActions actions={actions} selectedIds={selectedIds} clearSelection={clearSelection} />
    </Wrapper>,
  );
}

describe("BulkActions", () => {
  it("renders nothing when no rows are selected", () => {
    const { container } = renderBulkActions([{ label: "Add Tag", type: "custom", component: "BulkTagAction" }], []);
    expect(container.innerHTML).toBe("");
  });

  it("renders only the in-scope action types (custom/route/export)", () => {
    const actions: BulkAction[] = [
      { label: "Add Tag", type: "custom", component: "BulkTagAction" },
      { label: "Archive", type: "route", route: "contacts.archive" },
      { label: "Export Selected", type: "export", route: "contacts.exportContacts" },
      { label: "Import", type: "import" },
    ];
    renderBulkActions(actions, ["1"]);

    expect(screen.getByText("Add Tag")).toBeTruthy();
    expect(screen.getByText("Archive")).toBeTruthy();
    expect(screen.getByText("Export Selected")).toBeTruthy();
    expect(screen.queryByText("Import")).toBeNull();
  });

  it("hides an action the current user lacks permission for", () => {
    const actions: BulkAction[] = [
      { label: "Add Tag", type: "custom", component: "BulkTagAction", permission: "contacts:contact:write" },
    ];
    renderBulkActions(actions, ["1"], vi.fn(), permissionWrapper([]));
    expect(screen.queryByText("Add Tag")).toBeNull();
  });

  it("hides an action when selection count is below min_selected", () => {
    const actions: BulkAction[] = [{ label: "Add Tag", type: "custom", component: "BulkTagAction", min_selected: 2 }];
    renderBulkActions(actions, ["1"]);
    expect(screen.queryByText("Add Tag")).toBeNull();
  });

  it("hides an action when selection count exceeds max_selected", () => {
    const actions: BulkAction[] = [{ label: "Add Tag", type: "custom", component: "BulkTagAction", max_selected: 1 }];
    renderBulkActions(actions, ["1", "2"]);
    expect(screen.queryByText("Add Tag")).toBeNull();
  });

  describe("custom", () => {
    function TestPanel() {
      const { selectedIds, selectedCount, onComplete, onCancel } = useBulkAction();
      return (
        <div>
          <span>{selectedCount} selected</span>
          <span>{selectedIds.join(",")}</span>
          <button type="button" onClick={onComplete}>
            Complete
          </button>
          <button type="button" onClick={onCancel}>
            Cancel
          </button>
        </div>
      );
    }

    it("mounts the registered component with selection context after the button is clicked", () => {
      resolveComponentMock.mockReturnValue(TestPanel);
      const actions: BulkAction[] = [{ label: "Add Tag", type: "custom", component: "BulkTagAction" }];
      renderBulkActions(actions, ["1", "2"]);

      fireEvent.click(screen.getByText("Add Tag"));

      expect(resolveComponentMock).toHaveBeenCalledWith("BulkTagAction");
      expect(screen.getByText("2 selected")).toBeTruthy();
      expect(screen.getByText("1,2")).toBeTruthy();
      expect(screen.queryByText("Add Tag")).toBeNull();
    });

    it("onComplete clears the selection and closes the panel", () => {
      resolveComponentMock.mockReturnValue(TestPanel);
      const clearSelection = vi.fn();
      const actions: BulkAction[] = [{ label: "Add Tag", type: "custom", component: "BulkTagAction" }];
      renderBulkActions(actions, ["1"], clearSelection);

      fireEvent.click(screen.getByText("Add Tag"));
      fireEvent.click(screen.getByText("Complete"));

      expect(clearSelection).toHaveBeenCalledTimes(1);
    });

    it("onCancel closes the panel without clearing the selection", () => {
      resolveComponentMock.mockReturnValue(TestPanel);
      const clearSelection = vi.fn();
      const actions: BulkAction[] = [{ label: "Add Tag", type: "custom", component: "BulkTagAction" }];
      renderBulkActions(actions, ["1"], clearSelection);

      fireEvent.click(screen.getByText("Add Tag"));
      fireEvent.click(screen.getByText("Cancel"));

      expect(clearSelection).not.toHaveBeenCalled();
      expect(screen.getByText("Add Tag")).toBeTruthy();
    });
  });

  describe("route", () => {
    it("fires the mutation with the selected ids when there's no confirm step", () => {
      const mutate = vi.fn();
      useActionMock.mockReturnValue({ mutate, isPending: false, isError: false, error: null });
      const actions: BulkAction[] = [{ label: "Archive", type: "route", route: "contacts.archive" }];
      renderBulkActions(actions, ["1", "2"]);

      fireEvent.click(screen.getByText("Archive"));
      expect(mutate).toHaveBeenCalledWith({ ids: ["1", "2"] });
    });

    it("gates a destructive action behind AlertDialog when `confirm` is set", () => {
      const mutate = vi.fn();
      useActionMock.mockReturnValue({ mutate, isPending: false, isError: false, error: null });
      const actions: BulkAction[] = [
        {
          label: "Archive",
          type: "route",
          route: "contacts.archive",
          confirm: { title: "Archive contacts?", message: "This cannot be undone.", destructive: true },
        },
      ];
      renderBulkActions(actions, ["1"]);

      fireEvent.click(screen.getByText("Archive"));
      expect(mutate).not.toHaveBeenCalled();
      expect(screen.getByText("Archive contacts?")).toBeTruthy();

      fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
      expect(mutate).toHaveBeenCalledWith({ ids: ["1"] });
    });

    it("clears the selection when the mutation succeeds", () => {
      const clearSelection = vi.fn();
      let onSuccess: (() => void) | undefined;
      useActionMock.mockImplementation((_route: string, options: { onSuccess?: () => void }) => {
        onSuccess = options.onSuccess;
        return { mutate: vi.fn(), isPending: false, isError: false, error: null };
      });
      const actions: BulkAction[] = [{ label: "Archive", type: "route", route: "contacts.archive" }];
      renderBulkActions(actions, ["1"], clearSelection);

      onSuccess?.();
      expect(clearSelection).toHaveBeenCalledTimes(1);
    });
  });

  describe("export", () => {
    it("triggers the export with format and selected ids", () => {
      const trigger = vi.fn(async () => {});
      useExportMock.mockReturnValue({ trigger, isPending: false, isError: false, error: null });
      const actions: BulkAction[] = [
        { label: "Export Selected", type: "export", route: "contacts.exportContacts", format: "xlsx" },
      ];
      renderBulkActions(actions, ["1", "2"]);

      fireEvent.click(screen.getByText("Export Selected"));
      expect(trigger).toHaveBeenCalledWith({ format: "xlsx", ids: ["1", "2"] });
    });

    it("defaults format to csv when unset", () => {
      const trigger = vi.fn(async () => {});
      useExportMock.mockReturnValue({ trigger, isPending: false, isError: false, error: null });
      const actions: BulkAction[] = [{ label: "Export Selected", type: "export", route: "contacts.exportContacts" }];
      renderBulkActions(actions, ["1"]);

      fireEvent.click(screen.getByText("Export Selected"));
      expect(trigger).toHaveBeenCalledWith({ format: "csv", ids: ["1"] });
    });
  });
});
