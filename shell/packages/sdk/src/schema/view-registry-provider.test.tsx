import { act, cleanup, fireEvent, render, waitFor } from "@testing-library/react";
import { useContext } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { MetaSchema } from "./types.js";
import {
  useRefreshViewRegistry,
  useViewRegistryStatus,
  ViewRegistryContext,
  ViewRegistryProviderForTenant,
} from "./view-registry-provider.js";

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

function RefreshProbe() {
  const refresh = useRefreshViewRegistry();
  const registry = useContext(ViewRegistryContext);
  return (
    <button type="button" onClick={refresh}>
      {registry?.getBundleSHA256("crm") ?? "empty"}
    </button>
  );
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
  it("refreshes the live registry after a presentation write", async () => {
    const schema = (hash: string) =>
      ({
        modules: {
          crm: {
            routes: [],
            views: [],
            navigation: [],
            models: {},
            error_codes: [],
            notification_types: [],
            view_extensions: [],
            view_extension_definitions: [],
            frontend: { bundle_url: "/crm.js", bundle_sha256: hash },
          },
        },
      }) as unknown as MetaSchema;
    getSchemaMock.mockResolvedValueOnce(schema("base")).mockResolvedValueOnce(schema("updated"));
    const { getByRole, findByRole } = render(
      <ViewRegistryProviderForTenant isAuthenticated tenantId="t1">
        <RefreshProbe />
      </ViewRegistryProviderForTenant>,
    );
    fireEvent.click(await findByRole("button", { name: "base" }));
    await findByRole("button", { name: "updated" });
    expect(getSchemaMock).toHaveBeenCalledTimes(2);
    expect(getByRole("button").textContent).toBe("updated");
  });
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
