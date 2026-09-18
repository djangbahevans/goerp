import { describe, expect, it, vi } from "vitest";
import { loadPivotWasmDataset } from "./pivot-wasm-dataset.js";

const { getDuckDBMock } = vi.hoisted(() => ({ getDuckDBMock: vi.fn() }));
vi.mock("./pivot-duckdb-runtime.js", () => ({ getDuckDB: getDuckDBMock }));

function fakeTable(rows: Record<string, unknown>[]) {
  return { toArray: () => rows.map((row) => ({ toJSON: () => row })) };
}

describe("loadPivotWasmDataset", () => {
  it("registers each page as its own virtual file and queries them together via read_parquet", async () => {
    const query = vi.fn(async (_sql: string) => fakeTable([]));
    const registerFileBuffer = vi.fn(async () => undefined);
    const dropFile = vi.fn(async () => undefined);
    const close = vi.fn(async () => undefined);
    getDuckDBMock.mockResolvedValue({
      connect: vi.fn(async () => ({ query, close })),
      registerFileBuffer,
      dropFile,
    });

    const dataset = await loadPivotWasmDataset([new Uint8Array([1]), new Uint8Array([2])]);
    expect(registerFileBuffer).toHaveBeenCalledTimes(2);

    await dataset.aggregate(["region"], [], [{ field: "id", aggregation: "count" }]);
    const sql = query.mock.calls[0]?.[0] ?? "";
    expect(sql).toContain("read_parquet([");
    expect(sql.match(/\.parquet/g)).toHaveLength(2);

    await dataset.dispose();
    expect(close).toHaveBeenCalled();
    expect(dropFile).toHaveBeenCalledTimes(2);
  });

  it("maps queried rows back into a PivotResponse, only registering files once at load time", async () => {
    const query = vi.fn(async () => fakeTable([{ region: "east", id_count: 2n, __grouping_region: false }]));
    const registerFileBuffer = vi.fn(async () => undefined);
    getDuckDBMock.mockResolvedValue({
      connect: vi.fn(async () => ({ query, close: vi.fn(async () => undefined) })),
      registerFileBuffer,
      dropFile: vi.fn(async () => undefined),
    });

    const dataset = await loadPivotWasmDataset([new Uint8Array([1])]);
    const result = await dataset.aggregate(["region"], [], [{ field: "id", aggregation: "count" }]);
    await dataset.aggregate(["region"], [], [{ field: "id", aggregation: "count" }]);

    expect(result).toEqual({ cells: [{ row: ["east"], column: [], values: { id_count: 2 } }] });
    expect(registerFileBuffer).toHaveBeenCalledTimes(1);
    expect(query).toHaveBeenCalledTimes(2);
  });

  it("returns no cells and skips the query when both rows and columns are empty", async () => {
    const query = vi.fn();
    getDuckDBMock.mockResolvedValue({
      connect: vi.fn(async () => ({ query, close: vi.fn(async () => undefined) })),
      registerFileBuffer: vi.fn(async () => undefined),
      dropFile: vi.fn(async () => undefined),
    });

    const dataset = await loadPivotWasmDataset([]);
    const result = await dataset.aggregate([], [], [{ field: "id", aggregation: "count" }]);

    expect(result).toEqual({ cells: [] });
    expect(query).not.toHaveBeenCalled();
  });
});
