import {
  AuthContext,
  type AuthContextValue,
  type CurrentTenant,
  type CurrentUser,
  createPermissionContextValue,
  PermissionContext,
} from "@goerp/sdk/auth";
import { AppError } from "@goerp/sdk/error";
import { localeStore } from "@goerp/sdk/i18n";
import { toast } from "@goerp/sdk/notifications";
import { themeStore } from "@goerp/sdk/react";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppearancePage } from "./appearance-page.js";

// jsdom doesn't implement scrollIntoView; Radix Select and CodeSelect call it.
Element.prototype.scrollIntoView = vi.fn();

const USER: CurrentUser = {
  id: "u1",
  email: "ada@example.com",
  contactId: null,
  name: "Ada",
  avatarUrl: null,
  roles: [],
  amr: ["pwd"],
  mfaVerifiedAt: null,
  mfaSetupRequired: false,
  theme: "system",
  locale: null,
  timezone: null,
  dateFormat: null,
};

const TENANT: CurrentTenant = {
  id: "t1",
  slug: "acme",
  name: "Acme",
  plan: "pro",
  defaultLocale: "en",
  defaultTimezone: "UTC",
  availableLocales: ["en", "fr"],
};

const permissionValue = createPermissionContextValue({
  permissions: new Set<string>(),
  fieldAccess: {},
  modulesEnabled: new Set<string>(),
});

function renderPage({
  user = USER,
  updatePreferences = vi.fn(async () => {}),
  reload = vi.fn(),
}: {
  user?: CurrentUser;
  updatePreferences?: AuthContextValue["updatePreferences"];
  reload?: () => void;
} = {}) {
  const auth = {
    state: { status: "authenticated", user, tenant: TENANT },
    isAuthenticated: true,
    user,
    tenant: TENANT,
    updatePreferences,
  } as unknown as AuthContextValue;
  render(
    <AuthContext.Provider value={auth}>
      <PermissionContext.Provider value={permissionValue}>
        <AppearancePage reload={reload} />
      </PermissionContext.Provider>
    </AuthContext.Provider>,
  );
  return { updatePreferences, reload };
}

function field(label: string): HTMLElement {
  const element = screen.getByText(label, { selector: "label" }).parentElement;
  if (!element) throw new Error(`no field labelled ${label}`);
  return element;
}

beforeEach(() => {
  themeStore.setPreference("light");
  localeStore.setLocale("en");
});
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("AppearancePage", () => {
  it("shows the organisation defaults when the user inherits", () => {
    renderPage();
    expect(screen.getByRole("combobox", { name: "Language" }).textContent).toContain("Organisation default (English)");
    expect((screen.getByRole("combobox", { name: "Timezone" }) as HTMLInputElement).value).toBe(
      "Organisation default (UTC)",
    );
    expect(screen.getByRole("combobox", { name: "Date format" }).textContent).toContain("Automatic (from language)");
    expect(field("Timezone").textContent).toMatch(/UTC — \d/);
  });

  it("shows the user's own choices", () => {
    renderPage({ user: { ...USER, locale: "fr", timezone: "Africa/Accra", dateFormat: "iso" } });
    expect(screen.getByRole("combobox", { name: "Language" }).textContent).toContain("Français");
    expect((screen.getByRole("combobox", { name: "Timezone" }) as HTMLInputElement).value).toBe("Africa/Accra");
    expect(screen.getByRole("combobox", { name: "Date format" }).textContent).toMatch(/^\d{4}-\d{2}-\d{2}/);
    expect(field("Timezone").textContent).toMatch(/Africa\/Accra — \d/);
  });

  it("applies a theme immediately and saves it", async () => {
    const { updatePreferences } = renderPage();

    fireEvent.click(screen.getByRole("radio", { name: "Dark" }));

    expect(themeStore.getPreference()).toBe("dark");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    await waitFor(() => expect(updatePreferences).toHaveBeenCalledWith({ theme: "dark" }));
  });

  it("reverts the theme and shows a toast when the save fails", async () => {
    const error = vi.spyOn(toast, "error");
    renderPage({
      updatePreferences: vi.fn(async () => {
        throw new AppError({
          code: "invalid_preference",
          message: "bad",
          httpStatus: 422,
          details: { field: "theme" },
        });
      }),
    });

    fireEvent.click(screen.getByRole("radio", { name: "Dark" }));

    await waitFor(() => expect(themeStore.getPreference()).toBe("light"));
    expect(error).toHaveBeenCalledWith("Couldn't save your theme. Try again.");
  });

  it("saves a language, applies it to the document and reloads", async () => {
    const { updatePreferences, reload } = renderPage();

    fireEvent.click(screen.getByRole("combobox", { name: "Language" }));
    fireEvent.click(await screen.findByRole("option", { name: "Français" }));

    await waitFor(() => expect(reload).toHaveBeenCalled());
    expect(updatePreferences).toHaveBeenCalledWith({ locale: "fr" });
    expect(document.documentElement.getAttribute("lang")).toBe("fr");
  });

  it("sends null for the organisation default language", async () => {
    const { updatePreferences } = renderPage({ user: { ...USER, locale: "fr" } });

    fireEvent.click(screen.getByRole("combobox", { name: "Language" }));
    fireEvent.click(await screen.findByRole("option", { name: "Organisation default (English)" }));

    await waitFor(() => expect(updatePreferences).toHaveBeenCalledWith({ locale: null }));
  });

  it("reverts the language without reloading when the save fails", async () => {
    vi.spyOn(toast, "error");
    const { reload } = renderPage({
      updatePreferences: vi.fn(async () => {
        throw new Error("offline");
      }),
    });

    fireEvent.click(screen.getByRole("combobox", { name: "Language" }));
    fireEvent.click(await screen.findByRole("option", { name: "Français" }));

    await waitFor(() =>
      expect(screen.getByRole("combobox", { name: "Language" }).textContent).toContain("Organisation default"),
    );
    expect(reload).not.toHaveBeenCalled();
  });

  it("saves a timezone, and null for the organisation default", async () => {
    const { updatePreferences } = renderPage();
    const input = screen.getByRole("combobox", { name: "Timezone" });

    fireEvent.focus(input);
    fireEvent.change(input, { target: { value: "accra" } });
    fireEvent.click(await screen.findByRole("option", { name: "Africa/Accra" }));
    await waitFor(() => expect(updatePreferences).toHaveBeenCalledWith({ timezone: "Africa/Accra" }));
    expect(field("Timezone").textContent).toMatch(/Africa\/Accra — \d/);

    fireEvent.click(screen.getByRole("button", { name: "Clear Africa/Accra" }));
    await waitFor(() => expect(updatePreferences).toHaveBeenLastCalledWith({ timezone: null }));
  });

  it("labels each date format with today's date and saves the choice", async () => {
    const { updatePreferences } = renderPage();

    fireEvent.click(screen.getByRole("combobox", { name: "Date format" }));
    const iso = await screen.findByRole("option", { name: /^\d{4}-\d{2}-\d{2}$/ });
    fireEvent.click(iso);

    await waitFor(() => expect(updatePreferences).toHaveBeenCalledWith({ dateFormat: "iso" }));
  });

  it("doesn't let an earlier failed save revert a later choice", async () => {
    const error = vi.spyOn(toast, "error");
    let failFirst: (reason: Error) => void = () => {};
    const updatePreferences = vi
      .fn()
      .mockImplementationOnce(
        () =>
          new Promise<void>((_, reject) => {
            failFirst = reject;
          }),
      )
      .mockImplementationOnce(async () => {});
    renderPage({ updatePreferences });

    fireEvent.click(screen.getByRole("radio", { name: "Dark" }));
    fireEvent.click(screen.getByRole("radio", { name: "System" }));
    await waitFor(() => expect(updatePreferences).toHaveBeenCalledTimes(2));
    failFirst(new Error("offline"));
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(themeStore.getPreference()).toBe("system");
    expect(error).not.toHaveBeenCalled();
  });
});
