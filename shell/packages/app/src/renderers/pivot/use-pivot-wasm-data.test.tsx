import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { usePivotWasmData } from "./use-pivot-wasm-data.js";

const { fetchAllParquetPagesMock, loadPivotWasmDatasetMock, resolveMock } = vi.hoisted(() => ({
  fetchAllParquetPagesMock: vi.fn(),
  loadPivotWasmDatasetMock: vi.fn(),
  resolveMock: vi.fn(),
}));

vi.mock("@goerp/sdk", () => ({
  apiClient: {},
  fetchAllParquetPages: fetchAllParquetPagesMock,
}));
vi.mock("@goerp/sdk/schema", () => ({
  resourceRegistry: { resolve: resolveMock },
}));
vi.mock("./pivot-wasm-dataset.js", () => ({
  loadPivotWasmDataset: loadPivotWasmDatasetMock,
}));

function fakeDataset(cells: unknown[] = []) {
  return {
    aggregate: vi.fn(async () => ({ cells })),
    dispose: vi.fn(async () => undefined),
  };
}

describe("usePivotWasmData", () => {
  beforeEach(() => {
    fetchAllParquetPagesMock.mockReset().mockResolvedValue([new Uint8Array([1])]);
    loadPivotWasmDatasetMock.mockReset();
    resolveMock.mockReset().mockResolvedValue({ listPath: "/sales/orders" });
  });

  it("is disabled (no fetch, immediate empty cells) when both rows and columns are empty", async () => {
    const { result } = renderHook(() =>
      usePivotWasmData("sales.order", { rows: [], columns: [], values: [{ field: "id", aggregation: "count" }] }),
    );

    await waitFor(() => expect(result.current.isLoading).toBe(false));
    expect(result.current.data).toEqual({ cells: [] });
    expect(fetchAllParquetPagesMock).not.toHaveBeenCalled();
  });

  it("fetches pages, loads a dataset, and aggregates on first render", async () => {
    const dataset = fakeDataset([{ row: ["east"], column: [], values: { id_count: 1 } }]);
    loadPivotWasmDatasetMock.mockResolvedValue(dataset);

    const { result } = renderHook(() =>
      usePivotWasmData("sales.order", {
        rows: ["region"],
        columns: [],
        values: [{ field: "id", aggregation: "count" }],
        filter: { confirmed: true },
      }),
    );

    expect(result.current.isLoading).toBe(true);
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    expect(resolveMock).toHaveBeenCalledWith("sales.order");
    expect(fetchAllParquetPagesMock).toHaveBeenCalledWith(
      "/sales/orders",
      { filter: { confirmed: true } },
      expect.anything(),
    );
    expect(dataset.aggregate).toHaveBeenCalledWith(["region"], [], [{ field: "id", aggregation: "count" }]);
    expect(result.current.data).toEqual({ cells: [{ row: ["east"], column: [], values: { id_count: 1 } }] });
  });

  it("re-aggregates without a new fetch when only rows/columns/values change", async () => {
    const dataset = fakeDataset();
    loadPivotWasmDatasetMock.mockResolvedValue(dataset);

    const { result, rerender } = renderHook(
      (props: { rows: string[] }) =>
        usePivotWasmData("sales.order", {
          rows: props.rows,
          columns: [],
          values: [{ field: "id", aggregation: "count" }],
        }),
      { initialProps: { rows: ["region"] } },
    );
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    rerender({ rows: ["state"] });
    await waitFor(() => expect(dataset.aggregate).toHaveBeenCalledTimes(2));

    expect(fetchAllParquetPagesMock).toHaveBeenCalledTimes(1);
    expect(loadPivotWasmDatasetMock).toHaveBeenCalledTimes(1);
    expect(dataset.aggregate).toHaveBeenNthCalledWith(2, ["state"], [], [{ field: "id", aggregation: "count" }]);
  });

  it("reloads the dataset (disposing the old one) when the filter changes", async () => {
    const datasetA = fakeDataset();
    const datasetB = fakeDataset();
    loadPivotWasmDatasetMock.mockResolvedValueOnce(datasetA).mockResolvedValueOnce(datasetB);

    const { result, rerender } = renderHook(
      (props: { filter: Record<string, boolean> }) =>
        usePivotWasmData("sales.order", {
          rows: ["region"],
          columns: [],
          values: [{ field: "id", aggregation: "count" }],
          filter: props.filter,
        }),
      { initialProps: { filter: { confirmed: true } } },
    );
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    rerender({ filter: { confirmed: false } });
    await waitFor(() => expect(loadPivotWasmDatasetMock).toHaveBeenCalledTimes(2));

    expect(fetchAllParquetPagesMock).toHaveBeenCalledTimes(2);
    expect(datasetA.dispose).toHaveBeenCalled();
  });

  it("surfaces a fetch failure as isError/error", async () => {
    fetchAllParquetPagesMock.mockRejectedValue(new Error("network down"));

    const { result } = renderHook(() =>
      usePivotWasmData("sales.order", {
        rows: ["region"],
        columns: [],
        values: [{ field: "id", aggregation: "count" }],
      }),
    );

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error?.message).toBe("network down");
  });

  it("refetch() reloads the dataset even when nothing else changed", async () => {
    const datasetA = fakeDataset();
    const datasetB = fakeDataset();
    loadPivotWasmDatasetMock.mockResolvedValueOnce(datasetA).mockResolvedValueOnce(datasetB);

    const { result } = renderHook(() =>
      usePivotWasmData("sales.order", {
        rows: ["region"],
        columns: [],
        values: [{ field: "id", aggregation: "count" }],
      }),
    );
    await waitFor(() => expect(result.current.isLoading).toBe(false));

    result.current.refetch();
    await waitFor(() => expect(loadPivotWasmDatasetMock).toHaveBeenCalledTimes(2));
    expect(datasetA.dispose).toHaveBeenCalled();
  });
});
