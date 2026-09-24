import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MetaSchema } from "./types.js";
import { useViewRegistryStatus, ViewRegistryProviderForTenant } from "./view-registry-provider.js";

const { getSchemaMock } = vi.hoisted(() => ({ getSchemaMock: vi.fn<() => Promise<MetaSchema>>() }));
vi.mock("./schema-registry.js", () => ({
  schemaRegistry: { getSchema: () => getSchemaMock(), invalidate: () => {} },
}));
vi.mock("../realtime/ws-manager.js", () => ({
  tenantChannel: (tenantId: string) => `tenant:${tenantId}`,
  userChannel: (userId: string) => `ui:user:${userId}`,
  wsManager: { subscribe: () => () => {} },
}));

const EMPTY_SCHEMA = { models: {}, views: {}, routes: [], navigation: [], modules: {} } as unknown as MetaSchema;

function LoadedProbe() {
  return <div data-testid="loaded">{useViewRegistryStatus()}</div>;
}

function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((res) => {
    resolve = res;
  });
  return { promise, resolve };
}

beforeEach(() => {
  getSchemaMock.mockReset();
});
afterEach(cleanup);

describe("useViewRegistryStatus", () => {
  it("is loading until the first schema fetch settles, then ready", async () => {
    const pending = deferred<MetaSchema>();
    getSchemaMock.mockReturnValue(pending.promise);
    const { getByTestId } = render(
      <ViewRegistryProviderForTenant isAuthenticated tenantId="t1">
        <LoadedProbe />
      </ViewRegistryProviderForTenant>,
    );

    expect(getByTestId("loaded").textContent).toBe("loading");
    await act(async () => pending.resolve(EMPTY_SCHEMA));
    expect(getByTestId("loaded").textContent).toBe("ready");
  });

  it("is error after a failed fetch", async () => {
    getSchemaMock.mockRejectedValue(new Error("network"));
    const { getByTestId } = render(
      <ViewRegistryProviderForTenant isAuthenticated tenantId="t1">
        <LoadedProbe />
      </ViewRegistryProviderForTenant>,
    );
    await waitFor(() => expect(getByTestId("loaded").textContent).toBe("error"));
  });

  it("is loading while unauthenticated", () => {
    const { getByTestId } = render(
      <ViewRegistryProviderForTenant isAuthenticated={false} tenantId={null}>
        <LoadedProbe />
      </ViewRegistryProviderForTenant>,
    );
    expect(getByTestId("loaded").textContent).toBe("loading");
    expect(getSchemaMock).not.toHaveBeenCalled();
  });

  it("defaults to ready outside a provider", () => {
    const { getByTestId } = render(<LoadedProbe />);
    expect(getByTestId("loaded").textContent).toBe("ready");
  });
});
