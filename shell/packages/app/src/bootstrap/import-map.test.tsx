import { afterEach, describe, expect, it, vi } from "vitest";
import { updateImportMap, watchImportMap } from "./import-map.js";

afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  document.head.innerHTML = "";
});

describe("updateImportMap", () => {
  const current = { imports: { react: "/assets/react-old.js", "@goerp/sdk": "/assets/sdk-old.js" } };

  it("reloads when a shared entry changes, is added, or is removed", () => {
    for (const imports of [
      { ...current.imports, react: "/assets/react-new.js" },
      { ...current.imports, "react-dom": "/assets/react-dom.js" },
      { react: current.imports.react },
    ]) {
      const reload = vi.fn();
      updateImportMap(current, { imports }, reload);
      expect(reload).toHaveBeenCalledOnce();
    }
  });

  it("keeps the document for an unchanged map regardless of property order", () => {
    const reload = vi.fn();
    updateImportMap(
      current,
      { imports: { "@goerp/sdk": current.imports["@goerp/sdk"], react: current.imports.react } },
      reload,
    );
    expect(reload).not.toHaveBeenCalled();
  });
});

describe("watchImportMap", () => {
  it("checks on visibility changes and periodically, and cleans up on disposal", async () => {
    vi.useFakeTimers();
    document.head.innerHTML =
      '<script id="goerp-import-map" type="importmap">{"imports":{"react":"/assets/react.js"}}</script>';
    const fetchMap = vi.fn<typeof fetch>(async () => new Response('{"imports":{"react":"/assets/react.js"}}'));
    vi.stubGlobal("fetch", fetchMap);
    const stop = watchImportMap();
    document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchMap).toHaveBeenCalledTimes(2);
    expect(fetchMap).toHaveBeenCalledWith("/__goerp_import_map", expect.objectContaining({ cache: "no-store" }));
    stop();
    document.dispatchEvent(new Event("visibilitychange"));
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fetchMap).toHaveBeenCalledTimes(2);
    expect(fetchMap.mock.calls[0]?.[1]?.signal?.aborted).toBe(true);
  });
});
