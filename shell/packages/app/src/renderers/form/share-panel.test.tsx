import { createPermissionContextValue, PermissionContext } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import type { RecordShare, UseSharesResult } from "@goerp/sdk/react";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SharePanel, type SharePanelProps } from "./share-panel.js";

const { useSharesMock } = vi.hoisted(() => ({ useSharesMock: vi.fn() }));
vi.mock("@goerp/sdk/react", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@goerp/sdk/react")>();
  return { ...actual, useShares: useSharesMock };
});

const ada: RecordShare = {
  id: "s1",
  sharedWithUserId: "u2",
  sharedWithEmail: "ada@example.com",
  permission: "read",
  expiresAt: null,
  createdAt: "2026-09-19T10:00:00Z",
};

function handle(overrides: Partial<UseSharesResult> = {}): UseSharesResult {
  return {
    shares: [],
    isLoading: false,
    isError: false,
    error: null,
    refetch: vi.fn(),
    grant: vi.fn(async () => {}),
    isGranting: false,
    revoke: vi.fn(async () => {}),
    revokingIds: [],
    ...overrides,
  };
}

const permissionValue = createPermissionContextValue({
  permissions: new Set(),
  fieldAccess: {},
  modulesEnabled: new Set(),
});

function renderPanel(overrides: Partial<SharePanelProps> = {}) {
  return render(
    <PermissionContext.Provider value={permissionValue}>
      <SharePanel
        resource="sales.order"
        recordId="o1"
        label="Order"
        permissions={["read", "write"]}
        headingId="share-heading"
        {...overrides}
      />
    </PermissionContext.Provider>,
  );
}

beforeEach(() => useSharesMock.mockReturnValue(handle()));
afterEach(() => {
  cleanup();
  useSharesMock.mockReset();
});

describe("SharePanel", () => {
  it("names itself by its heading and focuses the email input", () => {
    renderPanel();
    expect(screen.getByRole("heading", { name: "Share Order" }).getAttribute("id")).toBe("share-heading");
    expect(document.activeElement).toBe(screen.getByLabelText("Email"));
  });

  describe("access levels", () => {
    it("offers both levels as a labelled Access group, defaulting to the first", () => {
      renderPanel({ permissions: ["read", "write"] });
      const group = screen.getByRole("radiogroup", { name: "Access" });
      expect((within(group).getByLabelText("Can view") as HTMLInputElement).checked).toBe(true);
      expect((within(group).getByLabelText("Can edit") as HTMLInputElement).checked).toBe(false);
    });

    it("offers only the levels the model accepts, in declared order", () => {
      renderPanel({ permissions: ["write", "read"] });
      const group = screen.getByRole("radiogroup", { name: "Access" });
      expect((within(group).getByLabelText("Can edit") as HTMLInputElement).checked).toBe(true);
    });

    it("renders no Access control for a single level, and states what it grants", () => {
      renderPanel({ permissions: ["read"] });
      expect(screen.queryByRole("radiogroup")).toBeNull();
      expect(screen.getByText("They'll be able to view this Order.")).toBeTruthy();
    });

    it("replaces the form with a notice when the model accepts no level", () => {
      renderPanel({ permissions: [] });
      expect(screen.queryByLabelText("Email")).toBeNull();
      expect(screen.getByText("Sharing isn't enabled for Order records.")).toBeTruthy();
    });
  });

  describe("the shares list", () => {
    it("shows a skeleton while loading, and keeps the grant form usable", () => {
      useSharesMock.mockReturnValue(handle({ isLoading: true }));
      const { container } = renderPanel();
      expect(container.querySelector('[data-skeleton="lines"]')).toBeTruthy();
      expect(screen.getByLabelText("Email")).toBeTruthy();
    });

    it("says so when the record isn't shared yet", () => {
      renderPanel();
      expect(screen.getByText("Not shared with anyone yet.")).toBeTruthy();
    });

    it("renders each share's email, access badge and expiry", () => {
      const expiresAt = "2026-10-01T22:59:59.999Z";
      useSharesMock.mockReturnValue(
        handle({
          shares: [
            { ...ada, expiresAt },
            { ...ada, id: "s2", sharedWithEmail: "bo@example.com", permission: "write" },
          ],
        }),
      );
      renderPanel();
      const list = within(screen.getByRole("list"));
      expect(list.getByText("ada@example.com")).toBeTruthy();
      expect(list.getByText("Can view")).toBeTruthy();
      expect(list.getByText("bo@example.com")).toBeTruthy();
      expect(list.getByText("Can edit")).toBeTruthy();
      const formatted = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(expiresAt));
      expect(list.getByText(`Expires ${formatted}`)).toBeTruthy();
      expect(list.getAllByText(/^Expires /)).toHaveLength(1);
    });

    it("shows an unknown recipient as 'Unknown user', keeping the id in the title", () => {
      useSharesMock.mockReturnValue(handle({ shares: [{ ...ada, sharedWithEmail: null }] }));
      renderPanel();
      expect(screen.getByText("Unknown user").getAttribute("title")).toBe("u2");
    });

    it("shows a load failure with its message and a working Retry", () => {
      const refetch = vi.fn();
      useSharesMock.mockReturnValue(
        handle({
          isError: true,
          error: new AppError({ code: "permission_denied", message: "you do not have access", httpStatus: 403 }),
          refetch,
        }),
      );
      renderPanel();
      const alert = screen.getByRole("alert");
      expect(within(alert).getByText("Couldn't load who this is shared with.")).toBeTruthy();
      expect(within(alert).getByText("you do not have access")).toBeTruthy();
      fireEvent.click(within(alert).getByRole("button", { name: "Retry" }));
      expect(refetch).toHaveBeenCalled();
    });
  });

  describe("granting access", () => {
    function fill(email: string) {
      fireEvent.change(screen.getByLabelText("Email"), { target: { value: email } });
    }

    it("sends the trimmed email and the chosen level, without an expiry", async () => {
      const grant = vi.fn(async () => {});
      useSharesMock.mockReturnValue(handle({ grant }));
      renderPanel();
      fill("  bo@example.com ");
      fireEvent.click(within(screen.getByRole("radiogroup", { name: "Access" })).getByLabelText("Can edit"));
      fireEvent.click(screen.getByRole("button", { name: "Share" }));

      await waitFor(() =>
        expect(grant).toHaveBeenCalledWith({ userEmail: "bo@example.com", permission: "write", expiresAt: undefined }),
      );
    });

    it("sends the chosen expiry as the end of that local day", async () => {
      const grant = vi.fn(async () => {});
      useSharesMock.mockReturnValue(handle({ grant }));
      renderPanel();
      fill("bo@example.com");
      fireEvent.change(screen.getByLabelText("Expires (optional)"), { target: { value: "2026-10-01" } });
      fireEvent.click(screen.getByRole("button", { name: "Share" }));

      await waitFor(() =>
        expect(grant).toHaveBeenCalledWith({
          userEmail: "bo@example.com",
          permission: "read",
          expiresAt: new Date(2026, 9, 1, 23, 59, 59, 999).toISOString(),
        }),
      );
    });

    it("submits on Enter in the email input", async () => {
      const grant = vi.fn(async () => {});
      useSharesMock.mockReturnValue(handle({ grant }));
      renderPanel();
      fill("bo@example.com");
      fireEvent.keyDown(screen.getByLabelText("Email"), { key: "Enter" });
      await waitFor(() => expect(grant).toHaveBeenCalledTimes(1));
    });

    it("clears the email, keeps the level, announces the share and refocuses the email on success", async () => {
      useSharesMock.mockReturnValue(handle());
      renderPanel();
      fill("bo@example.com");
      fireEvent.click(within(screen.getByRole("radiogroup", { name: "Access" })).getByLabelText("Can edit"));
      fireEvent.click(screen.getByRole("button", { name: "Share" }));

      await waitFor(() => expect(screen.getByRole("status").textContent).toBe("Shared with bo@example.com."));
      expect((screen.getByLabelText("Email") as HTMLInputElement).value).toBe("");
      expect((screen.getByLabelText("Can edit") as HTMLInputElement).checked).toBe(true);
      expect(document.activeElement).toBe(screen.getByLabelText("Email"));
    });

    it("refuses an expiry before today without calling the engine", () => {
      const grant = vi.fn(async () => {});
      useSharesMock.mockReturnValue(handle({ grant }));
      renderPanel();
      fill("bo@example.com");
      fireEvent.change(screen.getByLabelText("Expires (optional)"), { target: { value: "2020-01-01" } });
      fireEvent.click(screen.getByRole("button", { name: "Share" }));
      expect(screen.getByText("Choose today or a later date.")).toBeTruthy();
      expect(grant).not.toHaveBeenCalled();
      fireEvent.change(screen.getByLabelText("Expires (optional)"), { target: { value: "2099-01-01" } });
      expect(screen.queryByText("Choose today or a later date.")).toBeNull();
    });

    it("focuses the email input when validation refuses the submit", () => {
      renderPanel();
      (document.activeElement as HTMLElement).blur();
      fireEvent.click(screen.getByRole("button", { name: "Share" }));
      expect(document.activeElement).toBe(screen.getByLabelText("Email"));
    });

    it("refuses an empty email without calling the engine", () => {
      const grant = vi.fn(async () => {});
      useSharesMock.mockReturnValue(handle({ grant }));
      renderPanel();
      fireEvent.click(screen.getByRole("button", { name: "Share" }));
      expect(screen.getByText("Enter an email address.")).toBeTruthy();
      expect(grant).not.toHaveBeenCalled();
    });

    it("says an existing recipient's access and expiry will be replaced, before submitting", () => {
      const expiresAt = "2026-10-01T22:59:59.999Z";
      useSharesMock.mockReturnValue(handle({ shares: [{ ...ada, expiresAt }] }));
      renderPanel();
      expect(screen.queryByText(/Already shared/)).toBeNull();
      fill("ADA@example.com");
      const formatted = new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(expiresAt));
      expect(
        screen.getByText(
          `Already shared: Can view, expires ${formatted}. Sharing again replaces their access and expiry.`,
        ),
      ).toBeTruthy();
      fill("bo@example.com");
      expect(screen.queryByText(/Already shared/)).toBeNull();
    });

    it("updates an existing recipient's access instead of refusing, ignoring case, and announces the update", async () => {
      const grant = vi.fn(async () => {});
      useSharesMock.mockReturnValue(handle({ grant, shares: [ada] }));
      renderPanel();
      fill("ADA@example.com");
      fireEvent.click(within(screen.getByRole("radiogroup", { name: "Access" })).getByLabelText("Can edit"));
      fireEvent.click(screen.getByRole("button", { name: "Share" }));

      await waitFor(() =>
        expect(grant).toHaveBeenCalledWith({ userEmail: "ADA@example.com", permission: "write", expiresAt: undefined }),
      );
      await waitFor(() => expect(screen.getByRole("status").textContent).toBe("Updated access for ADA@example.com."));
    });

    it("shows recipient_not_found on the email field", async () => {
      const grant = vi.fn(async () => {
        throw new AppError({ code: "recipient_not_found", message: "no user with that email", httpStatus: 400 });
      });
      useSharesMock.mockReturnValue(handle({ grant }));
      renderPanel();
      fill("nobody@example.com");
      fireEvent.click(screen.getByRole("button", { name: "Share" }));
      expect(await screen.findByText("No user with that email address.")).toBeTruthy();
      expect((screen.getByLabelText("Email") as HTMLInputElement).value).toBe("nobody@example.com");
    });

    it("shows any other failure as a form-level alert carrying the engine's message", async () => {
      const grant = vi.fn(async () => {
        throw new AppError({
          code: "permission_denied",
          message: "you do not have access to this record",
          httpStatus: 403,
        });
      });
      useSharesMock.mockReturnValue(handle({ grant }));
      renderPanel();
      fill("bo@example.com");
      fireEvent.click(screen.getByRole("button", { name: "Share" }));
      const alert = await screen.findByRole("alert");
      expect(alert.textContent).toBe("you do not have access to this record");
    });

    it("clears a previous error on the next submit", async () => {
      const grant = vi
        .fn<() => Promise<void>>()
        .mockRejectedValueOnce(new AppError({ code: "recipient_not_found", message: "x", httpStatus: 400 }))
        .mockResolvedValueOnce(undefined);
      useSharesMock.mockReturnValue(handle({ grant }));
      renderPanel();
      fill("nobody@example.com");
      fireEvent.click(screen.getByRole("button", { name: "Share" }));
      await screen.findByText("No user with that email address.");
      fireEvent.click(screen.getByRole("button", { name: "Share" }));
      await waitFor(() => expect(screen.queryByText("No user with that email address.")).toBeNull());
    });

    it("disables the form and marks Share busy while a grant is in flight", () => {
      useSharesMock.mockReturnValue(handle({ isGranting: true }));
      renderPanel();
      expect((screen.getByLabelText("Email") as HTMLInputElement).disabled).toBe(true);
      expect(screen.getByRole("button", { name: "Share" }).getAttribute("aria-busy")).toBe("true");
    });
  });

  describe("revoking access", () => {
    it("names each Revoke button for its recipient and revokes that share", async () => {
      const revoke = vi.fn(async () => {});
      useSharesMock.mockReturnValue(
        handle({ revoke, shares: [ada, { ...ada, id: "s2", sharedWithEmail: "bo@example.com" }] }),
      );
      renderPanel();
      fireEvent.click(screen.getByRole("button", { name: "Revoke access for bo@example.com" }));
      await waitFor(() => expect(revoke).toHaveBeenCalledWith("s2"));
      expect(screen.getByRole("button", { name: "Revoke access for ada@example.com" })).toBeTruthy();
    });

    it("dims the row and marks Revoke busy while it is being revoked", () => {
      useSharesMock.mockReturnValue(handle({ shares: [ada], revokingIds: ["s1"] }));
      renderPanel();
      expect(screen.getByRole("button", { name: "Revoke access for ada@example.com" }).getAttribute("aria-busy")).toBe(
        "true",
      );
      expect(screen.getByText("ada@example.com").closest("li")?.className).toContain("opacity-50");
    });

    it("shows a failed revoke inline under that row, and clears it on the next attempt", async () => {
      const revoke = vi
        .fn<() => Promise<void>>()
        .mockRejectedValueOnce(new AppError({ code: "internal_error", message: "boom", httpStatus: 500 }))
        .mockResolvedValueOnce(undefined);
      useSharesMock.mockReturnValue(handle({ revoke, shares: [ada] }));
      renderPanel();
      const button = screen.getByRole("button", { name: "Revoke access for ada@example.com" });
      fireEvent.click(button);
      expect(await screen.findByText("Couldn't revoke access. boom")).toBeTruthy();
      fireEvent.click(button);
      await waitFor(() => expect(screen.queryByText("Couldn't revoke access. boom")).toBeNull());
    });
  });
});
