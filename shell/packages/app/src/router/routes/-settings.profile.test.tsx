import type { AuthContextValue, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { toast } from "@goerp/sdk/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter, RouterProvider } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { routeTree } from "../routeTree.gen.js";

const FAKE_TENANT = { id: "t1", slug: "acme", name: "Acme", plan: "pro" };

// Same __root.tsx-renders-CommandPalette-regardless reasoning
// -[_m].$.test.tsx's own FAKE_AUTH comment documents.
function fakeAuth(user: CurrentUser, updateProfile = vi.fn(async () => {})): AuthContextValue {
  return {
    state: { status: "authenticated", user, tenant: FAKE_TENANT },
    isAuthenticated: true,
    user,
    tenant: FAKE_TENANT,
    login: async () => {},
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile,
  };
}

async function renderProfilePage(auth: AuthContextValue) {
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: ["/settings/profile"] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthContext.Provider value={auth}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <RouterProvider router={router} />
        </PermissionContext.Provider>
      </AuthContext.Provider>
    </QueryClientProvider>,
  );
}

afterEach(cleanup);

describe("/settings/profile", () => {
  it("pre-fills name and shows the read-only email", async () => {
    const user: CurrentUser = {
      id: "u1",
      email: "ada@example.com",
      name: "Ada Lovelace",
      contactId: null,
      avatarUrl: null,
      roles: [],
      amr: [],
      mfaVerifiedAt: null,
    };
    await renderProfilePage(fakeAuth(user));

    expect((screen.getByLabelText("Full name") as HTMLInputElement).value).toBe("Ada Lovelace");
    expect(screen.getByText("ada@example.com")).toBeTruthy();
  });

  it("saves the edited name, trimmed, with no avatar change", async () => {
    const user: CurrentUser = {
      id: "u1",
      email: "ada@example.com",
      name: "Ada Lovelace",
      contactId: null,
      avatarUrl: null,
      roles: [],
      amr: [],
      mfaVerifiedAt: null,
    };
    const updateProfile = vi.fn(async () => {});
    await renderProfilePage(fakeAuth(user, updateProfile));

    fireEvent.change(screen.getByLabelText("Full name"), { target: { value: "  Grace Hopper  " } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(updateProfile).toHaveBeenCalledWith({ name: "Grace Hopper", avatarId: undefined });
    });
  });

  it("shows an error toast and does not save when the name is blank", async () => {
    const user: CurrentUser = {
      id: "u1",
      email: "ada@example.com",
      name: "Ada Lovelace",
      contactId: null,
      avatarUrl: null,
      roles: [],
      amr: [],
      mfaVerifiedAt: null,
    };
    const updateProfile = vi.fn(async () => {});
    const toastError = vi.spyOn(toast, "error").mockImplementation(() => {});
    await renderProfilePage(fakeAuth(user, updateProfile));

    fireEvent.change(screen.getByLabelText("Full name"), { target: { value: "   " } });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    expect(toastError).toHaveBeenCalledWith("Name is required.");
    expect(updateProfile).not.toHaveBeenCalled();
    toastError.mockRestore();
  });

  it("shows an error toast when the save request fails", async () => {
    const user: CurrentUser = {
      id: "u1",
      email: "ada@example.com",
      name: "Ada Lovelace",
      contactId: null,
      avatarUrl: null,
      roles: [],
      amr: [],
      mfaVerifiedAt: null,
    };
    const updateProfile = vi.fn(async () => {
      throw new Error("network down");
    });
    const toastError = vi.spyOn(toast, "error").mockImplementation(() => {});
    await renderProfilePage(fakeAuth(user, updateProfile));

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(toastError).toHaveBeenCalledWith("Couldn't save your profile. Try again.");
    });
    toastError.mockRestore();
  });

  it("sends an explicit clear signal after removing the current avatar", async () => {
    const user: CurrentUser = {
      id: "u1",
      email: "ada@example.com",
      name: "Ada Lovelace",
      contactId: null,
      avatarUrl: "https://example.com/avatar.png",
      roles: [],
      amr: [],
      mfaVerifiedAt: null,
    };
    const updateProfile = vi.fn(async () => {});
    await renderProfilePage(fakeAuth(user, updateProfile));

    fireEvent.click(screen.getByRole("button", { name: "Remove avatar" }));
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    // "" (not undefined) — undefined means "avatar untouched" and would
    // be dropped from the PATCH body entirely (auth-client.ts), which is
    // exactly the bug this test guards: removing the avatar and saving
    // must be distinguishable from never touching it (goerp#819 review).
    await waitFor(() => {
      expect(updateProfile).toHaveBeenCalledWith({ name: "Ada Lovelace", avatarId: "" });
    });
  });

  it("does not send avatarId at all when the avatar was never touched", async () => {
    const user: CurrentUser = {
      id: "u1",
      email: "ada@example.com",
      name: "Ada Lovelace",
      contactId: null,
      avatarUrl: "https://example.com/avatar.png",
      roles: [],
      amr: [],
      mfaVerifiedAt: null,
    };
    const updateProfile = vi.fn(async () => {});
    await renderProfilePage(fakeAuth(user, updateProfile));

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(updateProfile).toHaveBeenCalledWith({ name: "Ada Lovelace", avatarId: undefined });
    });
  });

  it("shows a success toast after a successful save", async () => {
    const user: CurrentUser = {
      id: "u1",
      email: "ada@example.com",
      name: "Ada Lovelace",
      contactId: null,
      avatarUrl: null,
      roles: [],
      amr: [],
      mfaVerifiedAt: null,
    };
    const toastSuccess = vi.spyOn(toast, "success").mockImplementation(() => {});
    await renderProfilePage(fakeAuth(user));

    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => {
      expect(toastSuccess).toHaveBeenCalledWith("Profile updated.");
    });
    toastSuccess.mockRestore();
  });
});
