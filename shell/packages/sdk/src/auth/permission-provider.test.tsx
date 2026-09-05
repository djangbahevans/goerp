import { act, cleanup, render, waitFor } from "@testing-library/react";
import { useContext } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MessageHandler, RealtimeEnvelope } from "../realtime/ws-manager.js";
// vi.mock calls are hoisted above every import in this file (including this
// one) by vitest's transform, so the module under test always sees the
// mocked ./permission-client.js and ../realtime/index.js below.
import { PermissionContext, PermissionProviderForUser } from "./permission-provider.js";
import type { PermissionData } from "./permission-types.js";

// vi.mock's factory runs at hoist time, before any other top-level statement
// in this file — vi.hoisted() is what lets the factory close over state
// that's still visible to the test bodies below.
const { fetchPermissionsMock } = vi.hoisted(() => ({
  fetchPermissionsMock: vi.fn<() => Promise<PermissionData>>(),
}));
vi.mock("./permission-client.js", () => ({
  fetchPermissions: () => fetchPermissionsMock(),
}));

const { channelSubscriptions, subscribeMock, unsubscribeMock } = vi.hoisted(() => {
  const channelSubscriptions = new Map<string, MessageHandler>();
  const unsubscribeMock = vi.fn();
  const subscribeMock = vi.fn((channel: string, handler: MessageHandler) => {
    channelSubscriptions.set(channel, handler);
    return unsubscribeMock;
  });
  return { channelSubscriptions, subscribeMock, unsubscribeMock };
});
vi.mock("../realtime/index.js", () => ({
  tenantChannel: (tenantId: string) => `tenant:${tenantId}`,
  userChannel: (userId: string) => `ui:user:${userId}`,
  wsManager: { subscribe: subscribeMock },
}));

function dataOf(modules: string[]): PermissionData {
  return { permissions: new Set(), fieldAccess: {}, modulesEnabled: new Set(modules) };
}

function ModulesProbe() {
  const value = useContext(PermissionContext);
  return <div data-testid="modules">{value ? [...value.modulesEnabled].sort().join(",") : ""}</div>;
}

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void; reject: (reason: unknown) => void } {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

function fireChannelMessage(channel: string, message: RealtimeEnvelope): void {
  const handler = channelSubscriptions.get(channel);
  if (!handler) throw new Error(`no subscriber registered for channel ${channel}`);
  act(() => handler(message));
}

beforeEach(() => {
  channelSubscriptions.clear();
  subscribeMock.mockClear();
  unsubscribeMock.mockClear();
  fetchPermissionsMock.mockReset();
});

afterEach(() => {
  cleanup();
});

describe("PermissionProviderForUser live refresh", () => {
  it("subscribes to the current tenant's channel while authenticated", () => {
    fetchPermissionsMock.mockResolvedValue(dataOf([]));
    render(
      <PermissionProviderForUser isAuthenticated tenantId="t1" userId={null}>
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    expect(subscribeMock).toHaveBeenCalledWith("tenant:t1", expect.any(Function));
  });

  it("does not subscribe when there is no current tenant", () => {
    fetchPermissionsMock.mockResolvedValue(dataOf([]));
    render(
      <PermissionProviderForUser isAuthenticated tenantId={null} userId={null}>
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    expect(subscribeMock).not.toHaveBeenCalled();
  });

  it("unsubscribes on unmount", () => {
    fetchPermissionsMock.mockResolvedValue(dataOf([]));
    const { unmount } = render(
      <PermissionProviderForUser isAuthenticated tenantId="t1" userId={null}>
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    unmount();
    expect(unsubscribeMock).toHaveBeenCalledTimes(1);
  });

  it("refetches and updates modulesEnabled when a module.installed message arrives", async () => {
    fetchPermissionsMock.mockResolvedValueOnce(dataOf(["sales"]));
    const { getByTestId } = render(
      <PermissionProviderForUser isAuthenticated tenantId="t1" userId={null}>
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    await waitFor(() => expect(getByTestId("modules").textContent).toBe("sales"));

    fetchPermissionsMock.mockResolvedValueOnce(dataOf(["inventory", "sales"]));
    fireChannelMessage("tenant:t1", { channel: "tenant:t1", type: "module.installed" });

    await waitFor(() => expect(getByTestId("modules").textContent).toBe("inventory,sales"));
    expect(fetchPermissionsMock).toHaveBeenCalledTimes(2);
  });

  it("ignores messages of any other type on the same channel", async () => {
    fetchPermissionsMock.mockResolvedValueOnce(dataOf(["sales"]));
    const { getByTestId } = render(
      <PermissionProviderForUser isAuthenticated tenantId="t1" userId={null}>
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    await waitFor(() => expect(getByTestId("modules").textContent).toBe("sales"));

    fireChannelMessage("tenant:t1", { channel: "tenant:t1", type: "some.other.event" });

    expect(fetchPermissionsMock).toHaveBeenCalledTimes(1);
  });

  it("a failed live-refresh keeps the last-known-good data instead of falling back to empty", async () => {
    fetchPermissionsMock.mockResolvedValueOnce(dataOf(["sales"]));
    const { getByTestId } = render(
      <PermissionProviderForUser isAuthenticated tenantId="t1" userId={null}>
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    await waitFor(() => expect(getByTestId("modules").textContent).toBe("sales"));

    fetchPermissionsMock.mockRejectedValueOnce(new Error("network error"));
    await act(async () => {
      fireChannelMessage("tenant:t1", { channel: "tenant:t1", type: "module.installed" });
      await Promise.resolve().then(() => Promise.resolve());
    });

    expect(getByTestId("modules").textContent).toBe("sales");
  });

  it("never lets a slower, already-superseded fetch response overwrite a fresher one", async () => {
    const initialLoad = deferred<PermissionData>();
    const liveRefresh = deferred<PermissionData>();
    fetchPermissionsMock.mockReturnValueOnce(initialLoad.promise).mockReturnValueOnce(liveRefresh.promise);

    const { getByTestId } = render(
      <PermissionProviderForUser isAuthenticated tenantId="t1" userId={null}>
        <ModulesProbe />
      </PermissionProviderForUser>,
    );

    // Trigger the live-refresh fetch while the initial-load fetch is still in flight.
    fireChannelMessage("tenant:t1", { channel: "tenant:t1", type: "module.installed" });
    expect(fetchPermissionsMock).toHaveBeenCalledTimes(2);

    // The later-issued (live-refresh) fetch resolves first, with fresher data.
    await act(async () => liveRefresh.resolve(dataOf(["inventory", "sales"])));
    await waitFor(() => expect(getByTestId("modules").textContent).toBe("inventory,sales"));

    // The earlier-issued (initial-load) fetch resolves after — its stale
    // response must not clobber the fresher state already applied above.
    await act(async () => initialLoad.resolve(dataOf(["sales"])));
    expect(getByTestId("modules").textContent).toBe("inventory,sales");
  });
});

// The request-ordering and failure-handling behavior above is exercised
// through the tenant channel, but both effects share the same refresh
// helper — these tests cover only what's new for the user channel: the
// subscribe/unsubscribe lifecycle and role.changed's own message-type
// filter, not a duplicate of the generic refresh semantics already
// covered above.
describe("PermissionProviderForUser role-change live refresh", () => {
  it("subscribes to the current user's channel while authenticated", () => {
    fetchPermissionsMock.mockResolvedValue(dataOf([]));
    render(
      <PermissionProviderForUser isAuthenticated tenantId={null} userId="u1">
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    expect(subscribeMock).toHaveBeenCalledWith("ui:user:u1", expect.any(Function));
  });

  it("does not subscribe when there is no current user", () => {
    fetchPermissionsMock.mockResolvedValue(dataOf([]));
    render(
      <PermissionProviderForUser isAuthenticated tenantId={null} userId={null}>
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    expect(subscribeMock).not.toHaveBeenCalled();
  });

  it("unsubscribes on unmount", () => {
    fetchPermissionsMock.mockResolvedValue(dataOf([]));
    const { unmount } = render(
      <PermissionProviderForUser isAuthenticated tenantId={null} userId="u1">
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    unmount();
    expect(unsubscribeMock).toHaveBeenCalledTimes(1);
  });

  it("refetches permissions when a role.changed message arrives", async () => {
    fetchPermissionsMock.mockResolvedValueOnce(dataOf(["sales"]));
    const { getByTestId } = render(
      <PermissionProviderForUser isAuthenticated tenantId={null} userId="u1">
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    await waitFor(() => expect(getByTestId("modules").textContent).toBe("sales"));

    fetchPermissionsMock.mockResolvedValueOnce(dataOf(["hr", "sales"]));
    fireChannelMessage("ui:user:u1", { channel: "ui:user:u1", type: "role.changed" });

    await waitFor(() => expect(getByTestId("modules").textContent).toBe("hr,sales"));
    expect(fetchPermissionsMock).toHaveBeenCalledTimes(2);
  });

  it("ignores ui.push and other message types on the same channel", async () => {
    fetchPermissionsMock.mockResolvedValueOnce(dataOf(["sales"]));
    const { getByTestId } = render(
      <PermissionProviderForUser isAuthenticated tenantId={null} userId="u1">
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    await waitFor(() => expect(getByTestId("modules").textContent).toBe("sales"));

    fireChannelMessage("ui:user:u1", { channel: "ui:user:u1", type: "ui.push" });

    expect(fetchPermissionsMock).toHaveBeenCalledTimes(1);
  });
});

// The realistic runtime case: an authenticated session always has both a
// current tenant and a current user, so both channels are subscribed at
// once. Verifies the two subscriptions stay independent of each other.
describe("PermissionProviderForUser with both tenant and user channels active", () => {
  it("subscribes to both channels and reacts to each independently", async () => {
    fetchPermissionsMock.mockResolvedValueOnce(dataOf(["sales"]));
    const { getByTestId } = render(
      <PermissionProviderForUser isAuthenticated tenantId="t1" userId="u1">
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    await waitFor(() => expect(getByTestId("modules").textContent).toBe("sales"));
    expect(subscribeMock).toHaveBeenCalledWith("tenant:t1", expect.any(Function));
    expect(subscribeMock).toHaveBeenCalledWith("ui:user:u1", expect.any(Function));

    fetchPermissionsMock.mockResolvedValueOnce(dataOf(["inventory", "sales"]));
    fireChannelMessage("tenant:t1", { channel: "tenant:t1", type: "module.installed" });
    await waitFor(() => expect(getByTestId("modules").textContent).toBe("inventory,sales"));

    fetchPermissionsMock.mockResolvedValueOnce(dataOf(["hr", "inventory", "sales"]));
    fireChannelMessage("ui:user:u1", { channel: "ui:user:u1", type: "role.changed" });
    await waitFor(() => expect(getByTestId("modules").textContent).toBe("hr,inventory,sales"));

    expect(fetchPermissionsMock).toHaveBeenCalledTimes(3);
  });

  it("unsubscribes both channels independently on unmount", () => {
    fetchPermissionsMock.mockResolvedValue(dataOf([]));
    const { unmount } = render(
      <PermissionProviderForUser isAuthenticated tenantId="t1" userId="u1">
        <ModulesProbe />
      </PermissionProviderForUser>,
    );
    unmount();
    expect(unsubscribeMock).toHaveBeenCalledTimes(2);
  });
});
