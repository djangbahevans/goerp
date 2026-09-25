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
  },
  tenant: { id: "t1", slug: "acme", name: "Acme", plan: "pro" },
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
async function mountSignedIn() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => (url === "/auth/me" ? json(200, ME_BODY) : json(200, {}))),
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
