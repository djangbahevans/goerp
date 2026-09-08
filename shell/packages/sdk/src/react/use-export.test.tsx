import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useExport } from "./use-export.js";

const { resolveMock, getBlobMock, downloadBlobMock } = vi.hoisted(() => ({
  resolveMock: vi.fn(),
  getBlobMock: vi.fn(),
  downloadBlobMock: vi.fn(),
}));

vi.mock("./action-registry.js", () => ({ actionRegistry: { resolve: resolveMock } }));
vi.mock("../http/api-client.js", () => ({ apiClient: { getBlob: getBlobMock } }));
vi.mock("../http/download-blob.js", () => ({ downloadBlob: downloadBlobMock }));

afterEach(() => {
  resolveMock.mockReset();
  getBlobMock.mockReset();
  downloadBlobMock.mockReset();
});

describe("useExport", () => {
  it("resolves the route, fetches the blob with the given params, and downloads it", async () => {
    resolveMock.mockResolvedValue({ method: "GET", path: "/contacts/export" });
    const blob = new Blob(["a,b"]);
    getBlobMock.mockResolvedValue(blob);

    const { result } = renderHook(() => useExport("contacts.exportContacts", "export.csv"));
    await act(() => result.current.trigger({ format: "csv", ids: ["1", "2"] }));

    expect(resolveMock).toHaveBeenCalledWith("contacts.exportContacts");
    expect(getBlobMock).toHaveBeenCalledWith("/contacts/export", { params: { format: "csv", ids: ["1", "2"] } });
    expect(downloadBlobMock).toHaveBeenCalledWith(blob, "export.csv");
    expect(result.current.isError).toBe(false);
  });

  it("surfaces a failed fetch as an error instead of throwing unhandled", async () => {
    resolveMock.mockResolvedValue({ method: "GET", path: "/contacts/export" });
    getBlobMock.mockRejectedValue(new Error("boom"));

    const { result } = renderHook(() => useExport("contacts.exportContacts", "export.csv"));
    await act(async () => {
      await expect(result.current.trigger()).rejects.toThrow("boom");
    });

    expect(result.current.isError).toBe(true);
    expect(result.current.error?.message).toBe("boom");
    expect(downloadBlobMock).not.toHaveBeenCalled();
  });

  it("sets isPending true only while the export is in flight", async () => {
    resolveMock.mockResolvedValue({ method: "GET", path: "/contacts/export" });
    let resolveBlob: (blob: Blob) => void = () => {};
    getBlobMock.mockReturnValue(new Promise<Blob>((resolve) => (resolveBlob = resolve)));

    const { result } = renderHook(() => useExport("contacts.exportContacts", "export.csv"));
    let pending: Promise<void> = Promise.resolve();
    act(() => {
      pending = result.current.trigger();
    });
    await waitFor(() => expect(result.current.isPending).toBe(true));

    resolveBlob(new Blob(["x"]));
    await act(() => pending);
    expect(result.current.isPending).toBe(false);
  });
});
