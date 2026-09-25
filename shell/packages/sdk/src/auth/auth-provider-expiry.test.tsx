import { act, cleanup, render, waitFor } from "@testing-library/react";
import { useContext } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AuthContextValue } from "./types.js";

const ME_BODY = {
  user: {
    id: "u1",
    email: "ada@example.com",
    contact_id: null,
    name: "Ada",
    avatar_url: null,
    roles: [],
    amr: ["pwd"],
    mfa_verified_at: null,
    theme: "system",
    locale: null,
    timezone: null,
    date_format: null,
  },
  tenant: {
    id: "t1",
    slug: "acme",
    name: "Acme",
    plan: "pro",
    default_locale: "en",
    default_timezone: "UTC",
    available_locales: ["en"],
  },
};

function json(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    statusText: "",
    headers: new Headers(),
    json: async () => body,
  } as Response;
}

// The auth machine is a module singleton, so every test gets a fresh module graph.
async function mountSignedIn(meBody: unknown = ME_BODY) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => (url === "/auth/me" ? json(200, meBody) : json(200, {}))),
  );
  const provider = await import("./auth-provider.js");
  const { authMachine } = await import("./auth-machine.js");
  let auth: AuthContextValue | null = null;
  function Capture() {
    auth = useContext(provider.AuthContext);
    return null;
  }
  render(
    <provider.AuthProvider>
      <Capture />
    </provider.AuthProvider>,
  );
  await waitFor(() => expect(auth?.isAuthenticated).toBe(true));
  return {
    machine: authMachine,
    auth: () => auth as unknown as AuthContextValue,
  };
}

beforeEach(() => {
  vi.resetModules();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("AuthProvider on session expiry", () => {
  it("keeps the user and tenant it expired for, while not authenticated", async () => {
    const { machine, auth } = await mountSignedIn();

    act(() => {
      machine.transition({ type: "session_expired" });
    });

    expect(auth().isAuthenticated).toBe(false);
    expect(auth().state).toMatchObject({ status: "unauthenticated", sessionExpired: true });
    expect(auth().user?.id).toBe("u1");
    expect(auth().tenant?.id).toBe("t1");
  });

  it("clears them on a sign-out", async () => {
    const { auth } = await mountSignedIn();

    await act(async () => {
      await auth().logout();
    });

    expect(auth().state).toEqual({ status: "unauthenticated" });
    expect(auth().user).toBeNull();
    expect(auth().tenant).toBeNull();
  });
});

describe("AuthProvider session preferences", () => {
  it("applies the profile's theme and locale when the session loads", async () => {
    window.localStorage.setItem("goerp-theme", "light");
    await mountSignedIn({ ...ME_BODY, user: { ...ME_BODY.user, theme: "dark", locale: "fr" } });

    expect(window.localStorage.getItem("goerp-theme")).toBe("dark");
    expect(document.documentElement.getAttribute("data-theme")).toBe("dark");
    expect(document.documentElement.getAttribute("lang")).toBe("fr");
  });

  it("falls back to the tenant's default locale when the user inherits", async () => {
    await mountSignedIn({
      ...ME_BODY,
      user: { ...ME_BODY.user, locale: null },
      tenant: { ...ME_BODY.tenant, default_locale: "ar" },
    });

    expect(document.documentElement.getAttribute("lang")).toBe("ar");
    expect(document.documentElement.getAttribute("dir")).toBe("rtl");
  });
});

describe("AuthProvider.updatePreferences", () => {
  it("resolves once the save succeeds even if re-reading the session fails", async () => {
    const { auth } = await mountSignedIn();
    vi.stubGlobal(
      "fetch",
      vi.fn(async (_url: string, init?: RequestInit) =>
        init?.method === "PATCH" ? json(204, undefined) : json(500, {}),
      ),
    );

    await expect(auth().updatePreferences({ theme: "dark" })).resolves.toBeUndefined();
    expect(auth().isAuthenticated).toBe(true);
  });
});
