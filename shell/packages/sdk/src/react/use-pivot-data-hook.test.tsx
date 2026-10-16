import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { usePivotData } from "./use-pivot-data.js";

const { get } = vi.hoisted(() => ({
  get: vi.fn(async () => ({ cells: [{ row: ["east"], column: [], values: { amount_sum: 1 } }] })),
}));

vi.mock("../schema/index.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../schema/index.js")>()),
  resourceRegistry: { resolve: vi.fn(async () => ({ pivotPath: "/orders/pivot" })) },
}));
vi.mock("../http/index.js", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../http/index.js")>()),
  apiClient: { get },
}));

afterEach(cleanup);

function renderFiltered(keepPreviousData: boolean) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={client}>{children}</QueryClientProvider>
  );
  return renderHook(
    ({ region }: { region: string }) =>
      usePivotData("sales.order", {
        rows: ["region"],
        columns: [],
        values: [{ field: "amount", aggregation: "sum" }],
        filter: { region },
        keepPreviousData,
      }),
    { wrapper, initialProps: { region: "east" } },
  );
}

describe("usePivotData keepPreviousData", () => {
  it("keeps returning the previous aggregation while a changed filter loads", async () => {
    const rendered = renderFiltered(true);
    await waitFor(() => expect(rendered.result.current.data?.cells).toHaveLength(1));

    get.mockReturnValueOnce(new Promise(() => {}));
    rendered.rerender({ region: "west" });

    expect(rendered.result.current.isLoading).toBe(false);
    expect(rendered.result.current.isPlaceholderData).toBe(true);
    expect(rendered.result.current.data?.cells).toHaveLength(1);
  });

  it("returns no data while a changed filter loads by default", async () => {
    const rendered = renderFiltered(false);
    await waitFor(() => expect(rendered.result.current.data?.cells).toHaveLength(1));

    get.mockReturnValueOnce(new Promise(() => {}));
    rendered.rerender({ region: "west" });

    expect(rendered.result.current.isLoading).toBe(true);
    expect(rendered.result.current.data).toBeUndefined();
  });
});
