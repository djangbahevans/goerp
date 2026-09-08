import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadBlob } from "./download-blob.js";

afterEach(() => {
  vi.restoreAllMocks();
});

describe("downloadBlob", () => {
  it("clicks a temporary anchor pointed at an object URL, then revokes it", () => {
    const createSpy = vi.spyOn(URL, "createObjectURL");
    const revokeSpy = vi.spyOn(URL, "revokeObjectURL");
    const clickSpy = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});

    downloadBlob(new Blob(["a,b,c"], { type: "text/csv" }), "export.csv");

    expect(createSpy).toHaveBeenCalledTimes(1);
    expect(createSpy.mock.calls[0]?.[0]).toBeInstanceOf(Blob);
    expect(clickSpy).toHaveBeenCalledTimes(1);
    expect(clickSpy.mock.instances[0]).toMatchObject({
      download: "export.csv",
      href: createSpy.mock.results[0]?.value,
    });
    expect(revokeSpy).toHaveBeenCalledWith(createSpy.mock.results[0]?.value);
  });
});
