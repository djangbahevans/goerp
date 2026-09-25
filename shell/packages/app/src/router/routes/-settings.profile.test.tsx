import type { AuthContextValue, CurrentUser } from "@goerp/sdk/auth";
import { AuthContext, createPermissionContextValue, PermissionContext, permissionDataRef } from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryHistory, createRouter } from "@tanstack/react-router";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthRouterProvider } from "../auth-router-provider.js";
import { routeTree } from "../routeTree.gen.js";

const FAKE_TENANT = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en"],
};

// Same __root.tsx-renders-CommandPalette-regardless reasoning
// -[_m].$.test.tsx's own FAKE_AUTH comment documents.
function fakeAuth(
  user: CurrentUser,
  updateProfile = vi.fn(async () => {}),
  changePassword: AuthContextValue["changePassword"] = vi.fn(async () => {}),
): AuthContextValue {
  return {
    state: { status: "authenticated", user, tenant: FAKE_TENANT },
    isAuthenticated: true,
    user,
    tenant: FAKE_TENANT,
    login: async () => {},
    logout: async () => {},
    submitMFA: async () => {},
    updateProfile,
    updatePreferences: vi.fn(async () => {}),
    changePassword,
    reloadSession: async () => {},
  };
}

async function renderProfilePage(auth: AuthContextValue, entry = "/settings/profile") {
  const router = createRouter({
    routeTree,
    context: { auth },
    history: createMemoryHistory({ initialEntries: [entry] }),
  });
  await router.load();
  render(
    <QueryClientProvider client={new QueryClient()}>
      <AuthContext.Provider value={auth}>
        <PermissionContext.Provider value={createPermissionContextValue(permissionDataRef.current)}>
          <AuthRouterProvider router={router} />
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
      mfaSetupRequired: false,
      theme: "system" as const,
      locale: null,
      timezone: null,
      dateFormat: null,
    };
    await renderProfilePage(fakeAuth(user));

    expect((screen.getByLabelText("Full name") as HTMLInputElement).value).toBe("Ada Lovelace");
    expect(screen.getByText("ada@example.com")).toBeTruthy();
  });

  it("renders inside the Settings rail with Profile as the current page", async () => {
    const user: CurrentUser = {
      id: "u1",
      email: "ada@example.com",
      name: "Ada Lovelace",
      contactId: null,
      avatarUrl: null,
      roles: [],
      amr: [],
      mfaVerifiedAt: null,
      mfaSetupRequired: false,
      theme: "system" as const,
      locale: null,
      timezone: null,
      dateFormat: null,
    };
    await renderProfilePage(fakeAuth(user));

    const nav = screen.getByRole("navigation", { name: "Settings" });
    expect(within(nav).getByRole("link", { name: "Profile" }).getAttribute("aria-current")).toBe("page");
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
      mfaSetupRequired: false,
      theme: "system" as const,
      locale: null,
      timezone: null,
      dateFormat: null,
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
      mfaSetupRequired: false,
      theme: "system" as const,
      locale: null,
      timezone: null,
      dateFormat: null,
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
      mfaSetupRequired: false,
      theme: "system" as const,
      locale: null,
      timezone: null,
      dateFormat: null,
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
      mfaSetupRequired: false,
      theme: "system" as const,
      locale: null,
      timezone: null,
      dateFormat: null,
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
      mfaSetupRequired: false,
      theme: "system" as const,
      locale: null,
      timezone: null,
      dateFormat: null,
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
      mfaSetupRequired: false,
      theme: "system" as const,
      locale: null,
      timezone: null,
      dateFormat: null,
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

const ADA: CurrentUser = {
  id: "u1",
  email: "ada@example.com",
  name: "Ada Lovelace",
  contactId: null,
  avatarUrl: null,
  roles: [],
  amr: [],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  theme: "system" as const,
  locale: null,
  timezone: null,
  dateFormat: null,
};

function fillPasswords(current: string, next: string, confirm: string) {
  fireEvent.change(screen.getByLabelText("Current password"), { target: { value: current } });
  fireEvent.change(screen.getByLabelText("New password"), { target: { value: next } });
  fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: confirm } });
}

describe("/settings/profile change password", () => {
  it("submits, clears the fields and toasts on success", async () => {
    const changePassword = vi.fn(async () => {});
    const toastSuccess = vi.spyOn(toast, "success").mockImplementation(() => {});
    await renderProfilePage(fakeAuth(ADA, undefined, changePassword));

    fillPasswords("old passphrase here", "a brand new passphrase", "a brand new passphrase");
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    await waitFor(() => {
      expect(changePassword).toHaveBeenCalledWith({
        currentPassword: "old passphrase here",
        newPassword: "a brand new passphrase",
      });
    });
    await waitFor(() => {
      expect(toastSuccess).toHaveBeenCalledWith("Password updated. Other sessions were signed out.");
    });
    expect((screen.getByLabelText("Current password") as HTMLInputElement).value).toBe("");
    expect((screen.getByLabelText("New password") as HTMLInputElement).value).toBe("");
    toastSuccess.mockRestore();
  });

  it("validates empty and mismatched fields without calling the API", async () => {
    const changePassword = vi.fn(async () => {});
    await renderProfilePage(fakeAuth(ADA, undefined, changePassword));

    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByText("Enter your current password.")).toBeTruthy();

    fillPasswords("old passphrase here", "a brand new passphrase", "something else");
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByText("Passwords don't match.")).toBeTruthy();
    expect(changePassword).not.toHaveBeenCalled();
  });

  it("flags a mismatched confirmation on blur", async () => {
    await renderProfilePage(fakeAuth(ADA));

    fillPasswords("", "a brand new passphrase", "something else");
    fireEvent.blur(screen.getByLabelText("Confirm new password"));

    expect(await screen.findByText("Passwords don't match.")).toBeTruthy();
  });

  it("shows a wrong current password inline", async () => {
    const changePassword = vi.fn(async () => {
      throw new AppError({ code: "invalid_password", message: "current password is incorrect", httpStatus: 401 });
    });
    await renderProfilePage(fakeAuth(ADA, undefined, changePassword));

    fillPasswords("wrong", "a brand new passphrase", "a brand new passphrase");
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByText("Current password is incorrect.")).toBeTruthy();
  });

  it("shows the server's policy message under the new password", async () => {
    const changePassword = vi.fn(async () => {
      throw new AppError({ code: "auth.password_too_weak", message: "password is too common", httpStatus: 422 });
    });
    await renderProfilePage(fakeAuth(ADA, undefined, changePassword));

    fillPasswords("old passphrase here", "schmetterling", "schmetterling");
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByText("Password is too common.")).toBeTruthy();
  });

  it("focuses the current password field when opened at #change-password", async () => {
    await renderProfilePage(fakeAuth(ADA), "/settings/profile#change-password");

    await waitFor(() => {
      expect(document.activeElement).toBe(screen.getByLabelText("Current password"));
    });
  });
});
