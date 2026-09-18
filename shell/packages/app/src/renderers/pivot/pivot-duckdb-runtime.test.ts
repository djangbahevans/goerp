import { beforeEach, describe, expect, it, vi } from "vitest";

const { selectBundleMock, instantiateMock, AsyncDuckDBMock } = vi.hoisted(() => {
  const instantiateMock = vi.fn();
  class FakeAsyncDuckDB {
    instantiate = instantiateMock;
  }
  return { selectBundleMock: vi.fn(), instantiateMock, AsyncDuckDBMock: FakeAsyncDuckDB };
});

vi.mock("@duckdb/duckdb-wasm", () => ({
  selectBundle: selectBundleMock,
  AsyncDuckDB: AsyncDuckDBMock,
  ConsoleLogger: vi.fn(),
  LogLevel: { WARNING: 1 },
}));
vi.mock("@duckdb/duckdb-wasm/dist/duckdb-browser-eh.worker.js?url", () => ({ default: "eh-worker-url" }));
vi.mock("@duckdb/duckdb-wasm/dist/duckdb-browser-mvp.worker.js?url", () => ({ default: "mvp-worker-url" }));
vi.mock("@duckdb/duckdb-wasm/dist/duckdb-eh.wasm?url", () => ({ default: "eh-wasm-url" }));
vi.mock("@duckdb/duckdb-wasm/dist/duckdb-mvp.wasm?url", () => ({ default: "mvp-wasm-url" }));

class FakeWorker {
  constructor(public url: string) {}
}

describe("getDuckDB", () => {
  beforeEach(() => {
    vi.resetModules();
    selectBundleMock.mockReset();
    instantiateMock.mockReset();
    vi.stubGlobal("Worker", FakeWorker);
  });

  it("caches the resolved instance across calls, bootstrapping only once", async () => {
    selectBundleMock.mockResolvedValue({ mainModule: "m", mainWorker: "w", pthreadWorker: null });
    instantiateMock.mockResolvedValue(undefined);
    const { getDuckDB } = await import("./pivot-duckdb-runtime.js");

    const a = await getDuckDB();
    const b = await getDuckDB();

    expect(a).toBe(b);
    expect(selectBundleMock).toHaveBeenCalledTimes(1);
  });

  it("retries from scratch after a failed bootstrap, instead of caching the rejection forever", async () => {
    selectBundleMock
      .mockRejectedValueOnce(new Error("network blip"))
      .mockResolvedValueOnce({ mainModule: "m", mainWorker: "w", pthreadWorker: null });
    instantiateMock.mockResolvedValue(undefined);
    const { getDuckDB } = await import("./pivot-duckdb-runtime.js");

    await expect(getDuckDB()).rejects.toThrow("network blip");
    await expect(getDuckDB()).resolves.toBeDefined();
    expect(selectBundleMock).toHaveBeenCalledTimes(2);
  });
});
