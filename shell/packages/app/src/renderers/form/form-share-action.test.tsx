import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import type { UseSharesResult } from "@goerp/sdk/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ShareHeaderAction } from "./form-share-action.js";

const { resolveModelMock, useSharesMock } = vi.hoisted(() => ({ resolveModelMock: vi.fn(), useSharesMock: vi.fn() }));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useShares: useSharesMock };
});
vi.mock("@goerp/sdk/schema", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/schema")>();
  return { ...actual, modelRegistry: { resolve: resolveModelMock } };
});

const emptyShares: UseSharesResult = {
  shares: [],
  isLoading: false,
  isError: false,
  error: null,
  refetch: vi.fn(),
  grant: vi.fn(async () => {}),
  isGranting: false,
  revoke: vi.fn(async () => {}),
  revokingIds: [],
};

beforeEach(() => useSharesMock.mockReturnValue(emptyShares));
afterEach(() => {
  cleanup();
  resolveModelMock.mockReset();
  useSharesMock.mockReset();
});

const permissionValue = createPermissionContextValue({
  permissions: new Set(),
  fieldAccess: {},
  modulesEnabled: new Set(),
});

function renderShare(recordId: string | undefined) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <PermissionContext.Provider value={permissionValue}>
        <ShareHeaderAction resource="contacts.contact" recordId={recordId} />
      </PermissionContext.Provider>
    </QueryClientProvider>,
  );
}

describe("ShareHeaderAction", () => {
  it("shows nothing for a non-Shareable model", async () => {
    resolveModelMock.mockResolvedValue({ shareable: false });
    renderShare("01j");
    await vi.waitFor(() => expect(resolveModelMock).toHaveBeenCalled());
    expect(screen.queryByText("Share")).toBeNull();
  });

  it("shows nothing on a create form (no recordId yet), even for a Shareable model", async () => {
    resolveModelMock.mockResolvedValue({ shareable: true });
    renderShare(undefined);
    await vi.waitFor(() => expect(resolveModelMock).toHaveBeenCalled());
    expect(screen.queryByText("Share")).toBeNull();
  });

  const shareable = { shareable: true, label: "Contact", share_permissions: ["read", "write"] };

  it("shows the Share button for a Shareable model with an open record, and opens the share panel on click", async () => {
    resolveModelMock.mockResolvedValue(shareable);
    renderShare("01j");
    fireEvent.click(await screen.findByRole("button", { name: "Share" }));

    const dialog = screen.getByRole("dialog", { name: "Share Contact" });
    expect(dialog).toBeTruthy();
    expect(screen.getByLabelText("Email")).toBeTruthy();
  });

  it("scopes the panel to the record and the model's accepted access levels", async () => {
    resolveModelMock.mockResolvedValue({ ...shareable, share_permissions: ["read"] });
    renderShare("01j");
    fireEvent.click(await screen.findByRole("button", { name: "Share" }));

    expect(useSharesMock).toHaveBeenCalledWith("contacts.contact", "01j");
    expect(screen.queryByRole("radiogroup")).toBeNull();
    expect(screen.getByText("They'll be able to view this Contact.")).toBeTruthy();
  });

  it("closes on Escape and returns focus to the trigger", async () => {
    resolveModelMock.mockResolvedValue(shareable);
    renderShare("01j");
    const trigger = await screen.findByRole("button", { name: "Share" });
    fireEvent.click(trigger);
    fireEvent.keyDown(document, { key: "Escape" });

    expect(screen.queryByRole("dialog")).toBeNull();
    expect(document.activeElement).toBe(trigger);
  });

  it("closes on a pointer-down outside, but not on one inside the panel", async () => {
    resolveModelMock.mockResolvedValue(shareable);
    renderShare("01j");
    fireEvent.click(await screen.findByRole("button", { name: "Share" }));

    fireEvent.mouseDown(screen.getByLabelText("Email"));
    expect(screen.getByRole("dialog")).toBeTruthy();

    fireEvent.mouseDown(document.body);
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});
