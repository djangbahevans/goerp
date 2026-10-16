import type { AuthState } from "@goerp/sdk/auth";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useClearQueriesOnSignOut } from "./use-clear-queries-on-sign-out.js";

const { auth } = vi.hoisted(() => ({ auth: { state: { status: "checking" } as AuthState } }));
vi.mock("@goerp/sdk/auth", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@goerp/sdk/auth")>()),
  useAuth: () => auth,
}));

const user = { id: "u1" } as Extract<AuthState, { status: "authenticated" }>["user"];
const tenant = { id: "t1" } as Extract<AuthState, { status: "authenticated" }>["tenant"];
const KEY = ["notifications", "feed"];
let queryClient: QueryClient;

function renderAt(state: AuthState) {
  auth.state = state;
  const hook = renderHook(() => useClearQueriesOnSignOut(), {
    wrapper: ({ children }: { children: ReactNode }) => (
      <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
    ),
  });
  return (next: AuthState) => {
    auth.state = next;
    hook.rerender();
  };
}

beforeEach(() => {
  queryClient = new QueryClient();
  queryClient.setQueryData(KEY, ["first user's notification"]);
});

afterEach(cleanup);

it("clears the query cache once sign-out completes", () => {
  const moveTo = renderAt({ status: "authenticated", user, tenant });

  moveTo({ status: "logging_out" });
  expect(queryClient.getQueryData(KEY)).toBeDefined();

  moveTo({ status: "unauthenticated" });
  expect(queryClient.getQueryData(KEY)).toBeUndefined();
});

it("keeps the cache when the session expires", () => {
  const moveTo = renderAt({ status: "authenticated", user, tenant });

  moveTo({ status: "unauthenticated", sessionExpired: true, user, tenant });

  expect(queryClient.getQueryData(KEY)).toBeDefined();
});

it("keeps queries started before anyone signs in", () => {
  const moveTo = renderAt({ status: "checking" });

  moveTo({ status: "unauthenticated" });

  expect(queryClient.getQueryData(KEY)).toBeDefined();
});
